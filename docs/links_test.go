package docs

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

var (
	linkPattern    = regexp.MustCompile(`\]\(([^)\s]+)\)`)
	headingPattern = regexp.MustCompile(`^#{1,6}\s+(.+?)\s*$`)
)

func TestMarkdownLinksAndAnchorsResolve(t *testing.T) {
	root := filepath.Join("..")
	documents := markdownDocuments(t, root)
	if len(documents) < 2 {
		t.Fatalf("expected the README and the docs, found %v", documents)
	}
	checked := 0
	for _, document := range documents {
		for _, link := range links(t, document) {
			target, anchor, _ := strings.Cut(link.target, "#")
			if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") || strings.HasPrefix(target, "mailto:") {
				continue
			}
			checked++
			resolved := document
			if target != "" {
				resolved = filepath.Join(filepath.Dir(document), filepath.FromSlash(target))
			}
			if _, err := os.Stat(resolved); err != nil {
				t.Errorf("%s:%d links to %s: %v", document, link.line, link.target, err)
				continue
			}
			if anchor == "" {
				continue
			}
			if !strings.HasSuffix(resolved, ".md") {
				t.Errorf("%s:%d links to anchor %q in a non-Markdown file", document, link.line, link.target)
				continue
			}
			if _, ok := anchors(t, resolved)[anchor]; !ok {
				t.Errorf("%s:%d links to %s but %s has no heading with that anchor", document, link.line, link.target, resolved)
			}
		}
	}
	if checked == 0 {
		t.Fatal("expected at least one relative link across the documentation")
	}
}

func markdownDocuments(t *testing.T, root string) []string {
	t.Helper()
	var documents []string
	err := filepath.WalkDir(root, func(p string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "node_modules", "target", "testdata", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(entry.Name(), ".md") {
			documents = append(documents, p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return documents
}

type link struct {
	line   int
	target string
}

func links(t *testing.T, document string) []link {
	t.Helper()
	var found []link
	inFence := false
	for number, text := range lines(t, document) {
		if strings.HasPrefix(strings.TrimSpace(text), "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		for _, match := range linkPattern.FindAllStringSubmatch(text, -1) {
			found = append(found, link{line: number + 1, target: match[1]})
		}
	}
	return found
}

func anchors(t *testing.T, document string) map[string]struct{} {
	t.Helper()
	seen := map[string]int{}
	result := map[string]struct{}{}
	inFence := false
	for _, text := range lines(t, document) {
		if strings.HasPrefix(strings.TrimSpace(text), "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		match := headingPattern.FindStringSubmatch(text)
		if match == nil {
			continue
		}
		anchor := headingAnchor(match[1])
		if count := seen[anchor]; count > 0 {
			result[anchor+"-"+strconv.Itoa(count)] = struct{}{}
		} else {
			result[anchor] = struct{}{}
		}
		seen[anchor]++
	}
	return result
}

func headingAnchor(heading string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(heading) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-':
			b.WriteRune(r)
		case r == ' ':
			b.WriteRune('-')
		}
	}
	return b.String()
}

func lines(t *testing.T, document string) []string {
	t.Helper()
	content, err := os.ReadFile(document)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(string(content), "\n")
}
