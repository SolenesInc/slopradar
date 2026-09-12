package report

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SolenesInc/slopradar/internal/model"
)

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

func TestValidateFormatNamesInvalidValue(t *testing.T) {
	if err := ValidateFormat("yaml"); err == nil || err.Error() != `format must be md, json, or text, got "yaml"` {
		t.Fatalf("error = %v", err)
	}
}

func TestEveryRendererPropagatesWriterFailures(t *testing.T) {
	snapshot := model.Snapshot{Buckets: map[model.Bucket]model.Totals{model.Source: {}, model.Tests: {}}}
	difference := model.Diff{Buckets: map[model.Bucket]model.BucketDelta{model.Source: {}, model.Tests: {}}}
	points := []model.TrendPoint{{Buckets: map[model.Bucket]model.Totals{model.Source: {}, model.Tests: {}}}}
	for _, format := range []string{"md", "json", "text"} {
		for name, write := range map[string]func() error{
			"scan":  func() error { return WriteSnapshot(failingWriter{}, format, snapshot, false) },
			"diff":  func() error { return WriteDiff(failingWriter{}, format, difference, false) },
			"trend": func() error { return WriteTrend(failingWriter{}, format, points, false) },
		} {
			if err := write(); err == nil {
				t.Errorf("%s %s ignored writer failure", name, format)
			}
		}
	}
}

func TestRenderersEscapeControlAndMarkdownDelimiters(t *testing.T) {
	function := model.NewFunction("dir/evil|</code>\nnext.go", "run`|<tag>\t", 3, 12, 8, nil)
	function.Bucket = model.Source
	snapshot := model.Snapshot{
		Rev: "rev\nforged", Functions: []model.Function{function},
		Buckets:  map[model.Bucket]model.Totals{model.Source: {}, model.Tests: {}},
		Clones:   []model.ClonePair{{A: model.Range{File: function.File, Start: 1, End: 5}, B: model.Range{File: "other\r.go", Start: 2, End: 6}}},
		Warnings: []string{"warning\nforged"},
	}
	for _, format := range []string{"md", "text"} {
		var output strings.Builder
		if err := WriteSnapshot(&output, format, snapshot, true); err != nil {
			t.Fatal(err)
		}
		got := output.String()
		if strings.Contains(got, "evil|</code>\nnext") || strings.Contains(got, "warning\nforged") || strings.Contains(got, "\x1b[") {
			t.Fatalf("unsafe %s output: %q", format, got)
		}
		if !strings.Contains(got, `\n`) || !strings.Contains(got, `\t`) {
			t.Fatalf("escaped controls missing from %s output: %q", format, got)
		}
	}
}

func TestEmptyDiffMarkdownHasOneHeadlineLine(t *testing.T) {
	result := model.Diff{Buckets: map[model.Bucket]model.BucketDelta{model.Source: {}, model.Tests: {}}}
	var output strings.Builder
	if err := WriteDiff(&output, "md", result, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "```diff\n  No complexity or clone changes detected.\n```") {
		t.Fatalf("output = %q", output.String())
	}
}

func TestDiffTextUsesColorWhenEnabled(t *testing.T) {
	result := model.Diff{Buckets: map[model.Bucket]model.BucketDelta{model.Source: {MassAddedOverCC10: 2, MassRemovedOverCC10: 1}, model.Tests: {}}}
	var plain, colored strings.Builder
	if err := WriteDiff(&plain, "text", result, false); err != nil {
		t.Fatal(err)
	}
	if err := WriteDiff(&colored, "text", result, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(colored.String(), "\x1b[32m+2.000\x1b[0m") || !strings.Contains(colored.String(), "\x1b[31m-1.000\x1b[0m") {
		t.Fatalf("colored output = %q", colored.String())
	}
	stripped := strings.NewReplacer("\x1b[32m", "", "\x1b[31m", "", "\x1b[0m", "").Replace(colored.String())
	if stripped != plain.String() {
		t.Fatalf("stripped colored output differs\nplain:   %q\ncolored: %q", plain.String(), stripped)
	}
}

func TestMarkdownTestOnlyChangeGolden(t *testing.T) {
	function := model.NewFunction("only_test.go", "TestOnly", 1, 12, 9, nil)
	function.Bucket = model.Tests
	result := model.Diff{
		Buckets: map[model.Bucket]model.BucketDelta{
			model.Source: {},
			model.Tests:  {MassAddedOverCC10: function.Mass, CloneLinesTouchedBefore: 4, CloneLinesTouchedAfter: 5},
		},
		Functions: []model.FunctionDelta{{File: function.File, Name: function.Name, After: &function, DeltaMass: function.Mass, Note: "new"}},
	}
	assertMarkdownGolden(t, "test-only.md", result)
}

func TestMarkdownEqualNetCloneChurnGolden(t *testing.T) {
	result := model.Diff{
		Buckets:       map[model.Bucket]model.BucketDelta{model.Source: {}, model.Tests: {}},
		ClonesAdded:   []model.ClonePair{{A: model.Range{File: "a.go", Start: 1, End: 5}, B: model.Range{File: "b.go", Start: 2, End: 6}}},
		ClonesRemoved: []model.ClonePair{{A: model.Range{File: "c.go", Start: 3, End: 7}, B: model.Range{File: "d.go", Start: 4, End: 8}}},
	}
	assertMarkdownGolden(t, "clone-churn.md", result)
}

func assertMarkdownGolden(t *testing.T, name string, result model.Diff) {
	t.Helper()
	var output strings.Builder
	if err := WriteDiff(&output, "md", result, false); err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join("..", "..", "testdata", "report", name))
	if err != nil {
		t.Fatal(err)
	}
	if output.String() != string(want) {
		t.Fatalf("%s differs\n%s", name, output.String())
	}
}
