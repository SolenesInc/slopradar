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
	classification := Classification{Bucket: Source, Generated: generated(content)}
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

func generated(content []byte) bool {
	for _, line := range bytes.Split(content, []byte{'\n'}) {
		trimmed := bytes.TrimSpace(line)
		if !generatedComment(trimmed) {
			continue
		}
		lower := bytes.ToLower(trimmed)
		if bytes.Contains(trimmed, []byte("Code generated ")) && bytes.Contains(trimmed, []byte(" DO NOT EDIT.")) ||
			bytes.Contains(lower, []byte("@generated")) || bytes.Contains(lower, []byte("linguist-generated")) {
			return true
		}
	}
	return false
}

func generatedComment(line []byte) bool {
	for _, prefix := range [][]byte{[]byte("//"), []byte("#"), []byte("/*"), []byte("*"), []byte("<!--")} {
		if bytes.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}
