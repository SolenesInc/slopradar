package scan

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	analysiscache "github.com/SolenesInc/slopradar/internal/cache"
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

func TestDirectoryIgnoresSymlinkedConfiguration(t *testing.T) {
	dir := t.TempDir()
	writeScanFile(t, dir, "source.go", "package fixture\n\nfunc kept() {}\n")
	external := filepath.Join(t.TempDir(), "external.json")
	if err := os.WriteFile(external, []byte(`{"excludes":["source.go"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(dir, model.ConfigFile)); err != nil {
		t.Fatal(err)
	}
	snapshot, err := Directory(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Functions) != 1 || snapshot.Functions[0].Name != "kept" {
		t.Fatalf("external configuration affected snapshot: %#v", snapshot)
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

func TestBlobsPreservesNonUTF8TypeScriptPath(t *testing.T) {
	file := string([]byte{'s', 'o', 'u', 'r', 'c', 'e', 0xff, '.', 't', 's'})
	source := []byte("function run() {}")
	snapshot, err := Blobs("abc", []gitread.Blob{{BlobInfo: gitread.BlobInfo{Path: file, Size: int64(len(source))}, Content: source}})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Functions) != 1 || snapshot.Functions[0].File != file {
		t.Fatalf("functions = %#v", snapshot.Functions)
	}
}

func TestRevisionCacheRebindsPathsAndClassifiesAfterLoading(t *testing.T) {
	dir := t.TempDir()
	gitForScan(t, dir, "init", "-b", "main")
	gitForScan(t, dir, "config", "user.name", "Slopradar Test")
	gitForScan(t, dir, "config", "user.email", "test@slopradar.invalid")
	writeScanFile(t, dir, "source.go", "package fixture\n\nfunc cached() {}\n")
	gitForScan(t, dir, "add", ".")
	gitForScan(t, dir, "commit", "-m", "source")
	store := analysiscache.New(t.TempDir(), "test")
	first, err := RevisionWithCache(context.Background(), dir, "HEAD", store)
	if err != nil {
		t.Fatal(err)
	}
	warm, err := RevisionWithCache(context.Background(), dir, "HEAD", store)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, warm) {
		t.Fatalf("cold and warm snapshots differ\ncold: %#v\nwarm: %#v", first, warm)
	}
	gitForScan(t, dir, "mv", "source.go", "source_test.go")
	gitForScan(t, dir, "commit", "-m", "move to tests")
	cached, err := RevisionWithCache(context.Background(), dir, "HEAD", store)
	if err != nil {
		t.Fatal(err)
	}
	uncached, err := Revision(context.Background(), dir, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cached, uncached) {
		t.Fatalf("cached and uncached snapshots differ\ncached: %#v\nuncached: %#v", cached, uncached)
	}
	if len(cached.Functions) != 1 || cached.Functions[0].File != "source_test.go" || cached.Functions[0].Bucket != model.Tests || cached.Buckets[model.Source].Functions != 0 || cached.Buckets[model.Tests].Functions != 1 {
		t.Fatalf("renamed cached snapshot = %#v", cached)
	}
}

func TestRevisionCacheSeparatesTypeScriptDialects(t *testing.T) {
	dir := t.TempDir()
	gitForScan(t, dir, "init", "-b", "main")
	gitForScan(t, dir, "config", "user.name", "Slopradar Test")
	gitForScan(t, dir, "config", "user.email", "test@slopradar.invalid")
	writeScanFile(t, dir, "view.tsx", "export const view = () => <div />;\n")
	gitForScan(t, dir, "add", ".")
	gitForScan(t, dir, "commit", "-m", "tsx")
	store := analysiscache.New(t.TempDir(), "test")
	if _, err := RevisionWithCache(context.Background(), dir, "HEAD", store); err != nil {
		t.Fatal(err)
	}
	gitForScan(t, dir, "mv", "view.tsx", "view.ts")
	gitForScan(t, dir, "commit", "-m", "ts")
	if _, err := RevisionWithCache(context.Background(), dir, "HEAD", store); err == nil || !strings.Contains(err.Error(), "parse view.ts") {
		t.Fatalf("TypeScript scan error = %v", err)
	}
}

func gitForScan(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", dir}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, output)
	}
	return string(output)
}

func writeScanFile(t *testing.T, root, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
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
