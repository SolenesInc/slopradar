package model

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
)

const (
	ConfigFile                   = ".slopradar.json"
	MaxFileBytes                 = 2 * 1024 * 1024
	AttnGeneratedTypeScriptBytes = 925234
	AttnLargestHandwrittenBytes  = 256764
)

type Config struct {
	Excludes  []string `json:"excludes"`
	TestGlobs []string `json:"test_globs"`
}

type Classification struct {
	Bucket    Bucket
	Excluded  bool
	Generated bool
}

func ParseConfig(data []byte) (Config, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return Config{}, nil
	}
	var config Config
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", ConfigFile, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Config{}, fmt.Errorf("parse %s: content after the configuration object", ConfigFile)
	}
	return config, nil
}

func Classify(file string, content []byte, config Config) Classification {
	file = path.Clean(strings.TrimPrefix(strings.ReplaceAll(file, "\\", "/"), "./"))
	if excluded(file, config.Excludes) {
		return Classification{Excluded: true}
	}
	classification := Classification{Bucket: Source, Generated: generated(file, content)}
	if isTest(file, config.TestGlobs) {
		classification.Bucket = Tests
	}
	return classification
}

func excluded(file string, additions []string) bool {
	parts := strings.Split(file, "/")
	for _, part := range parts[:len(parts)-1] {
		if strings.HasPrefix(part, ".") || part == "vendor" || part == "node_modules" || part == "dist" || part == "target" || part == "testdata" {
			return true
		}
	}
	return matchesAny(file, additions)
}

func isTest(file string, additions []string) bool {
	base := path.Base(file)
	ext := strings.ToLower(path.Ext(base))
	stem := strings.TrimSuffix(base, path.Ext(base))
	parts := strings.Split(file, "/")
	for _, part := range parts[:len(parts)-1] {
		if part == "__tests__" || part == "tests" {
			return true
		}
	}
	switch ext {
	case ".go":
		if strings.HasSuffix(base, "_test.go") {
			return true
		}
	case ".ts", ".tsx", ".mts", ".cts", ".js", ".jsx", ".mjs", ".cjs":
		if strings.HasSuffix(stem, ".test") || strings.HasSuffix(stem, ".spec") {
			return true
		}
	case ".py":
		if strings.HasPrefix(base, "test_") || strings.HasSuffix(base, "_test.py") {
			return true
		}
	}
	return matchesAny(file, additions)
}

func matchesAny(file string, patterns []string) bool {
	for _, pattern := range patterns {
		pattern = strings.TrimPrefix(strings.ReplaceAll(pattern, "\\", "/"), "./")
		if matched, err := path.Match(pattern, file); err == nil && matched {
			return true
		}
		if strings.HasSuffix(pattern, "/") && strings.HasPrefix(file, pattern) {
			return true
		}
	}
	return false
}

func generated(file string, content []byte) bool {
	content = bytes.TrimPrefix(content, []byte{0xef, 0xbb, 0xbf})
	extension := strings.ToLower(path.Ext(file))
	for {
		comment, rest, ok := leadingComment(bytes.TrimSpace(content), extension)
		if !ok {
			return false
		}
		lower := bytes.ToLower(comment)
		if bytes.Contains(comment, []byte("Code generated ")) && bytes.Contains(comment, []byte(" DO NOT EDIT.")) ||
			bytes.Contains(lower, []byte("@generated")) || bytes.Contains(lower, []byte("linguist-generated")) {
			return true
		}
		content = rest
	}
}

func leadingComment(content []byte, extension string) ([]byte, []byte, bool) {
	hashComment := extension == ".py" && bytes.HasPrefix(content, []byte("#"))
	shebang := bytes.HasPrefix(content, []byte("#!")) && !bytes.HasPrefix(content, []byte("#!["))
	if bytes.HasPrefix(content, []byte("//")) || hashComment || shebang {
		comment, rest, _ := bytes.Cut(content, []byte{'\n'})
		return comment, rest, true
	}
	var opener, closer []byte
	switch {
	case bytes.HasPrefix(content, []byte("/*")):
		if extension == ".rs" {
			end := nestedCommentEnd(content)
			return content[:end], content[end:], true
		}
		opener, closer = []byte("/*"), []byte("*/")
	case bytes.HasPrefix(content, []byte("<!--")):
		opener, closer = []byte("<!--"), []byte("-->")
	default:
		return nil, nil, false
	}
	end := bytes.Index(content[len(opener):], closer)
	if end < 0 {
		return content, nil, true
	}
	end += len(opener) + len(closer)
	return content[:end], content[end:], true
}

func nestedCommentEnd(content []byte) int {
	depth := 1
	for i := 2; i+1 < len(content); i++ {
		switch string(content[i : i+2]) {
		case "/*":
			depth++
			i++
		case "*/":
			depth--
			i++
			if depth == 0 {
				return i + 1
			}
		}
	}
	return len(content)
}
