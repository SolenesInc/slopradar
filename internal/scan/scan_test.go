package scan

import (
	"os"
	"path/filepath"
	"reflect"
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
	if want := []string{"src/too_large.go"}; !reflect.DeepEqual(first.Skipped, want) {
		t.Fatalf("skipped = %#v, want %#v", first.Skipped, want)
	}
}

func TestValidateFormatNamesInvalidValue(t *testing.T) {
	if err := ValidateFormat("yaml"); err == nil || err.Error() != `format must be json or text, got "yaml"` {
		t.Fatalf("error = %v", err)
	}
}

func TestBlobsReportsRecoverableParseErrors(t *testing.T) {
	source := []byte(`function valid() {}
const using = "x";
switch (using) {
case "x": break;
}
function recovered() {}`)
	snapshot, err := Blobs("abc", []gitread.Blob{{BlobInfo: gitread.BlobInfo{Path: "source.ts", Size: int64(len(source))}, Content: source}})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Warnings) != 1 || snapshot.Warnings[0] != "parse source.ts: invalid TypeScript or JavaScript syntax; analyzed recoverable syntax" {
		t.Fatalf("warnings = %#v", snapshot.Warnings)
	}
}
