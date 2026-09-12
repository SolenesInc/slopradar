package scan

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/SolenesInc/slopradar/internal/gitread"
	"github.com/SolenesInc/slopradar/internal/model"
)

func TestBlobsBuildsDeterministicBucketsAndSkippedFiles(t *testing.T) {
	root := filepath.Join("..", "..", "testdata")
	pythonSource, err := os.ReadFile(filepath.Join(root, "python", "functions.py"))
	if err != nil {
		t.Fatal(err)
	}
	rustSource, err := os.ReadFile(filepath.Join(root, "rust", "functions.rs"))
	if err != nil {
		t.Fatal(err)
	}
	blobs := []gitread.Blob{
		{BlobInfo: gitread.BlobInfo{Path: "src/functions.rs", Size: int64(len(rustSource))}, Content: rustSource},
		{BlobInfo: gitread.BlobInfo{Path: "tests/functions.py", Size: int64(len(pythonSource))}, Content: pythonSource},
		{BlobInfo: gitread.BlobInfo{Path: "src/too_large.go", Size: model.MaxFileBytes + 1}},
		{BlobInfo: gitread.BlobInfo{Path: "src/generated.go", Size: 43}, Content: []byte("// Code generated fixture. DO NOT EDIT.\n")},
	}
	first, err := Blobs("abc", blobs)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Blobs("abc", []gitread.Blob{blobs[3], blobs[2], blobs[1], blobs[0]})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("snapshot depends on blob order\nfirst: %#v\nsecond: %#v", first, second)
	}
	if first.Buckets[model.Source].Functions != 2 || first.Buckets[model.Tests].Functions != 4 {
		t.Fatalf("buckets = %#v", first.Buckets)
	}
	if first.Functions[2].Name != "test_only" || first.Functions[2].Bucket != model.Tests {
		t.Fatalf("mixed-file test function = %#v", first.Functions[2])
	}
	if want := []string{"src/too_large.go"}; !reflect.DeepEqual(first.Skipped, want) {
		t.Fatalf("skipped = %#v, want %#v", first.Skipped, want)
	}
}

func TestDirectoryFiltersBeforeReadingContent(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name string, data []byte) string {
		file := filepath.Join(dir, name)
		if err := os.WriteFile(file, data, 0o644); err != nil {
			t.Fatal(err)
		}
		return file
	}
	excluded := write(filepath.Join("node_modules", "unreadable.ts"), []byte("function ignored() {}"))
	if err := os.Chmod(excluded, 0); err != nil {
		t.Fatal(err)
	}
	large := write("large.go", nil)
	if err := os.Truncate(large, int64(model.MaxFileBytes)+1); err != nil {
		t.Fatal(err)
	}
	snapshot, err := Directory(dir)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"large.go"}; !reflect.DeepEqual(snapshot.Skipped, want) {
		t.Fatalf("skipped = %#v, want %#v", snapshot.Skipped, want)
	}
}

func TestValidateFormatNamesInvalidValue(t *testing.T) {
	if err := ValidateFormat("yaml"); err == nil || err.Error() != `format must be json or text, got "yaml"` {
		t.Fatalf("error = %v", err)
	}
}

func TestBlobsRejectsTypeScriptParseErrors(t *testing.T) {
	source := []byte(`function valid() {}
function broken(`)
	snapshot, err := Blobs("abc", []gitread.Blob{{BlobInfo: gitread.BlobInfo{Path: "source.ts", Size: int64(len(source))}, Content: source}})
	if err == nil || !strings.Contains(err.Error(), "parse source.ts: invalid TypeScript or JavaScript syntax") {
		t.Fatalf("snapshot = %#v, error = %v", snapshot, err)
	}
}

func TestDirectoryFindsCloneFixtures(t *testing.T) {
	snapshot, err := Directory(filepath.Join("..", "..", "testdata", "clones"))
	if err != nil {
		t.Fatal(err)
	}
	want := []model.ClonePair{
		{
			ID: "02b2ebbff4aafe5192df9f1c0819880b1af3d514d83d81d12b4b4fd095b0e9d4",
			A:  model.Range{File: "comments/a.go", Start: 3, End: 14},
			B:  model.Range{File: "comments/b.go", Start: 3, End: 15}, Tokens: 51, Lines: 11,
		},
		{
			ID: "65820221732a816b01e43fe89a8e33a4a52832b5f909da51f0495757d5de51b4",
			A:  model.Range{File: "exact/a.go", Start: 3, End: 13},
			B:  model.Range{File: "exact/b.go", Start: 3, End: 13}, Tokens: 51, Lines: 11,
		},
		{
			ID: "f1e16af3db28919ad77a0a1c6cd7bc7683c2c777e05aa429f62b2f032d587da5",
			A:  model.Range{File: "same/same.go", Start: 3, End: 15},
			B:  model.Range{File: "same/same.go", Start: 17, End: 29}, Tokens: 59, Lines: 13,
		},
	}
	if !reflect.DeepEqual(snapshot.Clones, want) {
		t.Fatalf("clones = %#v, want %#v", snapshot.Clones, want)
	}
	if snapshot.Buckets[model.Source].CloneLines != 70 || snapshot.Buckets[model.Source].CloneShare != 70.0/85.0 {
		t.Fatalf("source totals = %#v", snapshot.Buckets[model.Source])
	}
	for _, pair := range snapshot.Clones {
		if strings.HasPrefix(pair.A.File, "four/") || strings.HasPrefix(pair.B.File, "four/") {
			t.Fatalf("four-line fixture counted: %#v", pair)
		}
	}
}

func TestBlobsPreservesMixedRustCloneCoverage(t *testing.T) {
	source := []byte(`fn production(value: i32) -> i32 {
    let alpha = value + 501;
    let beta = alpha * 502;
    let gamma = beta - 503;
    let delta = gamma / 504;
    let epsilon = delta + 505;
    let zeta = epsilon * 506;
    zeta
}

#[cfg(test)]
mod tests {
    fn fixture(value: i32) -> i32 {
        let one = value + 601;
        let two = one * 602;
        let three = two - 603;
        let four = three / 604;
        let five = four + 605;
        let six = five * 606;
        six
    }
}
`)
	snapshot, err := Blobs("mixed", []gitread.Blob{
		{BlobInfo: gitread.BlobInfo{Path: "src/b.rs", Size: int64(len(source))}, Content: source},
		{BlobInfo: gitread.BlobInfo{Path: "src/a.rs", Size: int64(len(source))}, Content: source},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Clones) != 1 || snapshot.Clones[0].A.Start != 1 || snapshot.Clones[0].A.End != 22 {
		t.Fatalf("clones = %#v", snapshot.Clones)
	}
	if snapshot.Buckets[model.Source].CloneLines == 0 || snapshot.Buckets[model.Tests].CloneLines == 0 {
		t.Fatalf("buckets = %#v", snapshot.Buckets)
	}
	if len(snapshot.CloneCoverage) != 4 || snapshot.CloneCoverage[0].Bucket != model.Source || snapshot.CloneCoverage[1].Bucket != model.Tests {
		t.Fatalf("clone coverage = %#v", snapshot.CloneCoverage)
	}
}
