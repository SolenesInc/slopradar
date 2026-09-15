package docs

import (
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

const commonMarkATXHeadingLevels = 6

var (
	linkPattern    = regexp.MustCompile(`\]\(([^)\s]+)\)`)
	headingPattern = regexp.MustCompile(`^#{1,` + strconv.Itoa(commonMarkATXHeadingLevels) + `}\s+(.+?)\s*$`)
)

func TestMarkdownLinksAndAnchorsResolve(t *testing.T) {
	root := filepath.Join("..")
	tracked := trackedFiles(t, root)
	documents := markdownDocuments(tracked)
	if len(documents) < 2 {
		t.Fatalf("expected the README and the docs, found %v", documents)
	}
	checked := 0
	for _, document := range documents {
		for _, link := range links(t, root, document) {
			target, anchor, _ := strings.Cut(link.target, "#")
			if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") || strings.HasPrefix(target, "mailto:") {
				continue
			}
			checked++
			resolved := document
			if target != "" {
				resolved = path.Join(path.Dir(document), target)
			}
			if _, ok := tracked[resolved]; !ok {
				t.Errorf("%s:%d links to %s, and %s is not a Git-tracked file", document, link.line, link.target, resolved)
				continue
			}
			if anchor == "" {
				continue
			}
			if !strings.HasSuffix(resolved, ".md") {
				t.Errorf("%s:%d links to anchor %q in a non-Markdown file", document, link.line, link.target)
				continue
			}
			if _, ok := anchors(t, root, resolved)[anchor]; !ok {
				t.Errorf("%s:%d links to %s but %s has no heading with that anchor", document, link.line, link.target, resolved)
			}
		}
	}
	if checked == 0 {
		t.Fatal("expected at least one relative link across the documentation")
	}
}

func trackedFiles(t *testing.T, root string) map[string]struct{} {
	t.Helper()
	listing, err := exec.Command("git", "-C", root, "ls-files", "-z").Output()
	if err != nil {
		t.Fatal(err)
	}
	tracked := map[string]struct{}{}
	for _, file := range strings.Split(strings.TrimSuffix(string(listing), "\x00"), "\x00") {
		tracked[file] = struct{}{}
	}
	return tracked
}

func markdownDocuments(tracked map[string]struct{}) []string {
	var documents []string
	for file := range tracked {
		if strings.HasSuffix(file, ".md") && !strings.Contains(file, "testdata/") {
			documents = append(documents, file)
		}
	}
	sort.Strings(documents)
	return documents
}

type link struct {
	line   int
	target string
}

func links(t *testing.T, root, document string) []link {
	t.Helper()
	var found []link
	inFence := false
	for number, text := range lines(t, root, document) {
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

func anchors(t *testing.T, root, document string) map[string]struct{} {
	t.Helper()
	seen := map[string]int{}
	result := map[string]struct{}{}
	inFence := false
	for _, text := range lines(t, root, document) {
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

func lines(t *testing.T, root, document string) []string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(document)))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(string(content), "\n")
}
