package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/SolenesInc/slopradar/internal/model"
)

func TestScanDirectoryJSONIsDeterministic(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "source.go", "package fixture\n\nfunc run(value bool) {\n\tif value {\n\t\treturn\n\t}\n}\n")
	writeFile(t, dir, "source_test.go", "package fixture\n\nfunc TestRun() {}\n")
	var first bytes.Buffer
	if err := run(context.Background(), []string{"scan", dir, "--format", "json"}, &first); err != nil {
		t.Fatal(err)
	}
	var second bytes.Buffer
	if err := run(context.Background(), []string{"scan", "--format=json", dir}, &second); err != nil {
		t.Fatal(err)
	}
	if first.String() != second.String() {
		t.Fatalf("outputs differ\nfirst: %s\nsecond: %s", first.String(), second.String())
	}
	var snapshot model.Snapshot
	if err := json.Unmarshal(first.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Rev != "directory" || snapshot.Buckets[model.Source].Functions != 1 || snapshot.Buckets[model.Tests].Functions != 1 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestScanArgsRejectsVisibleLimits(t *testing.T) {
	_, _, err := scanArgs([]string{"one", "two"})
	if err == nil || err.Error() != `scan accepts one revision or directory, got "two"` {
		t.Fatalf("error = %v", err)
	}
}

func TestWriteTextIncludesSkipLimitAndAsk(t *testing.T) {
	snapshot := model.Snapshot{
		Rev: "abc", Buckets: map[model.Bucket]model.Totals{model.Source: {}, model.Tests: {}},
		SkippedDetails: []model.SkippedFile{{File: "large.go", MaxBytes: model.MaxFileBytes, AskedBytes: model.MaxFileBytes + 1}},
	}
	var output bytes.Buffer
	writeText(&output, snapshot)
	want := fmt.Sprintf("skipped\tlarge.go\tmax_file_bytes=%d\tasked_bytes=%d\n", model.MaxFileBytes, model.MaxFileBytes+1)
	if !bytes.Contains(output.Bytes(), []byte(want)) {
		t.Fatalf("output = %q, want line %q", output.String(), want)
	}
}

func TestWriteTextIncludesClonePairs(t *testing.T) {
	snapshot := model.Snapshot{
		Rev: "abc",
		Buckets: map[model.Bucket]model.Totals{
			model.Source: {SourceLines: 8, CloneLines: 4, CloneShare: 0.5},
			model.Tests:  {},
		},
		Clones: []model.ClonePair{{
			ID: "stable", A: model.Range{File: "a.go", Start: 2, End: 5}, B: model.Range{File: "b.go", Start: 7, End: 10}, Tokens: 50, Lines: 4,
		}},
	}
	var output bytes.Buffer
	writeText(&output, snapshot)
	want := "stable\ta.go\t2\t5\tb.go\t7\t10\t50\t4\n"
	if !bytes.Contains(output.Bytes(), []byte(want)) {
		t.Fatalf("output = %q, want %q", output.String(), want)
	}
}

func writeFile(t *testing.T, root, name, content string) {
	t.Helper()
	file := filepath.Join(root, name)
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestScanArgsAllowsOptionsOnEitherSide(t *testing.T) {
	firstTarget, firstFormat, firstErr := scanArgs([]string{"HEAD", "--format", "json"})
	secondTarget, secondFormat, secondErr := scanArgs([]string{"--format=json", "HEAD"})
	if firstErr != nil || secondErr != nil || !reflect.DeepEqual([]string{firstTarget, firstFormat}, []string{secondTarget, secondFormat}) {
		t.Fatalf("first = %q %q %v, second = %q %q %v", firstTarget, firstFormat, firstErr, secondTarget, secondFormat, secondErr)
	}
}
