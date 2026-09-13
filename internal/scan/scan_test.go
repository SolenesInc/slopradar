package scan

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	analysiscache "github.com/SolenesInc/slopradar/internal/cache"
	"github.com/SolenesInc/slopradar/internal/clones"
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
	if first.Functions[2].Name != "tests::test_only" || first.Functions[2].Bucket != model.Tests {
		t.Fatalf("mixed-file test function = %#v", first.Functions[2])
	}
	if want := []string{"src/too_large.go"}; !reflect.DeepEqual(first.Skipped, want) {
		t.Fatalf("skipped = %#v, want %#v", first.Skipped, want)
	}
}

func TestRustMixedScopeLinesRemainInBothBuckets(t *testing.T) {
	source := []byte("fn prod() {} #[cfg(test)] fn helper() {}\n")
	for _, file := range []string{"src/mixed.rs", "tests/mixed.rs"} {
		t.Run(file, func(t *testing.T) {
			result, err := Blobs("mixed", []gitread.Blob{{BlobInfo: gitread.BlobInfo{Path: file, Size: int64(len(source))}, Content: source}})
			if err != nil {
				t.Fatal(err)
			}
			wantSourceFunctions, wantTestFunctions, wantSourceLines := 1, 1, 1
			if strings.HasPrefix(file, "tests/") {
				wantSourceFunctions, wantTestFunctions, wantSourceLines = 0, 2, 0
			}
			if result.Buckets[model.Source].Functions != wantSourceFunctions || result.Buckets[model.Tests].Functions != wantTestFunctions || result.Buckets[model.Source].SourceLines != wantSourceLines || result.Buckets[model.Tests].SourceLines != 1 {
				t.Fatalf("mixed scope buckets = %+v", result.Buckets)
			}
		})
	}
}

func TestRustMixedScopeCloneLengthsCountPhysicalLines(t *testing.T) {
	for _, directory := range []string{"src", "tests"} {
		for _, lineCount := range []int{clones.JscpdDefaultMinimumLines - 1, clones.JscpdDefaultMinimumLines} {
			t.Run(fmt.Sprintf("%s_%d", directory, lineCount), func(t *testing.T) {
				var source strings.Builder
				for i := range lineCount {
					fmt.Fprintf(&source, "fn prod%d() {} #[cfg(test)] fn helper%d() {}\n", i, i)
				}
				var blobs []gitread.Blob
				for _, name := range []string{"a.rs", "b.rs"} {
					blobs = append(blobs, gitread.Blob{BlobInfo: gitread.BlobInfo{Path: directory + "/" + name, Size: int64(source.Len())}, Content: []byte(source.String())})
				}
				result, err := Blobs("mixed", blobs)
				if err != nil {
					t.Fatal(err)
				}
				if lineCount < clones.JscpdDefaultMinimumLines {
					if len(result.Clones) != 0 {
						t.Fatalf("shared lines inflated clone length: %+v", result.Clones)
					}
					return
				}
				if len(result.Clones) != 1 || result.Clones[0].Lines != lineCount {
					t.Fatalf("clone lengths = %+v", result.Clones)
				}
				for _, bucket := range []model.Bucket{model.Source, model.Tests} {
					want := lineCount * len(blobs)
					if directory == "tests" && bucket == model.Source {
						want = 0
					}
					totals := result.Buckets[bucket]
					if totals.SourceLines != want || totals.CloneLines != want {
						t.Fatalf("%s totals = %+v, want %d lines", bucket, totals, want)
					}
				}
			})
		}
	}
}

func TestNestedRustTestBucketsSurviveScanAndFileOverrides(t *testing.T) {
	source := []byte(`fn outer() {
    #[cfg(all(test, unix))]
    fn test_helper() { let nested = || {}; }
    fn production_helper() {}
}
`)
	for _, file := range []string{"src/fixture.rs", "tests/fixture.rs"} {
		t.Run(file, func(t *testing.T) {
			result, err := Blobs("fixture", []gitread.Blob{{BlobInfo: gitread.BlobInfo{Path: file, Size: int64(len(source))}, Content: source}})
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Functions) != 1 || len(result.Functions[0].Nested) != 2 || len(result.Functions[0].Nested[0].Nested) != 1 {
				t.Fatalf("functions = %#v", result.Functions)
			}
			outer := result.Functions[0]
			bucket := model.Source
			if strings.HasPrefix(file, "tests/") {
				bucket = model.Tests
			}
			if outer.Bucket != bucket || outer.Nested[1].Bucket != bucket || outer.Nested[0].Bucket != model.Tests || outer.Nested[0].Nested[0].Bucket != model.Tests {
				t.Fatalf("buckets = %#v", outer)
			}
			if result.Buckets[bucket].Functions != 1 {
				t.Fatalf("nested rows entered totals: %#v", result.Buckets)
			}
		})
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

func TestDirectoryAndRevisionApplyFileGlobsOnlyToFiles(t *testing.T) {
	dir := t.TempDir()
	gitForScan(t, dir, "init", "-b", "main")
	gitForScan(t, dir, "config", "user.name", "Slopradar Test")
	gitForScan(t, dir, "config", "user.email", "test@slopradar.invalid")
	if err := os.MkdirAll(filepath.Join(dir, "src", "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeScanFile(t, dir, filepath.Join("src", "nested", "a.go"), "package a\n\nfunc A() {}\n")
	writeScanFile(t, dir, model.ConfigFile, `{"excludes":["src/f*"]}`)

	directory, err := Directory(dir)
	if err != nil {
		t.Fatal(err)
	}
	gitForScan(t, dir, "add", ".")
	gitForScan(t, dir, "commit", "-m", "fixture")
	revision, err := Revision(context.Background(), dir, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	for label, snapshot := range map[string]model.Snapshot{"directory": directory, "revision": revision} {
		if len(snapshot.Functions) != 1 || snapshot.Functions[0].File != "src/nested/a.go" || snapshot.Functions[0].Name != "A" {
			t.Fatalf("%s functions = %#v", label, snapshot.Functions)
		}
	}
}

func TestDirectoryLimitsBuiltInTestdataExclusionToGo(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "testdata"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeScanFile(t, dir, filepath.Join("testdata", "fixture.go"), "package fixture\n\nfunc goFixture() {}\n")
	writeScanFile(t, dir, filepath.Join("testdata", "fixture.ts"), "function tsFixture() {}\n")
	writeScanFile(t, dir, filepath.Join("testdata", "fixture.py"), "def python_fixture():\n    pass\n")
	writeScanFile(t, dir, filepath.Join("testdata", "fixture.rs"), "fn rust_fixture() {}\n")
	snapshot, err := Directory(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, function := range snapshot.Functions {
		got[function.Name] = true
	}
	if len(snapshot.Functions) != 3 || !got["tsFixture"] || !got["python_fixture"] || !got["rust_fixture"] || got["goFixture"] {
		t.Fatalf("functions = %#v", snapshot.Functions)
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

func TestBlobsRejectsTypeScriptParseErrors(t *testing.T) {
	source := []byte(`function valid() {}
function broken(`)
	snapshot, err := Blobs("abc", []gitread.Blob{{BlobInfo: gitread.BlobInfo{Path: "source.ts", Size: int64(len(source))}, Content: source}})
	if err == nil || !strings.Contains(err.Error(), "parse source.ts: invalid TypeScript or JavaScript syntax") {
		t.Fatalf("snapshot = %#v, error = %v", snapshot, err)
	}
}

func TestBlobsRejectsGoParseErrors(t *testing.T) {
	source := []byte("package fixture\nfunc broken(")
	snapshot, err := Blobs("abc", []gitread.Blob{{BlobInfo: gitread.BlobInfo{Path: "source.go", Size: int64(len(source))}, Content: source}})
	if err == nil || !strings.Contains(err.Error(), "parse source.go: invalid Go syntax") {
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

func TestDirectoryAndRevisionPreserveUnixPathBytes(t *testing.T) {
	if filepath.Separator != '/' {
		t.Skip("literal backslashes and arbitrary filename bytes are Unix-specific")
	}
	dir := t.TempDir()
	gitForScan(t, dir, "init", "-b", "main")
	gitForScan(t, dir, "config", "user.name", "Slopradar Test")
	gitForScan(t, dir, "config", "user.email", "test@slopradar.invalid")
	backslash := `vendor\main.go`
	invalidUTF8 := string([]byte{'s', 'o', 'u', 'r', 'c', 'e', 0xff, '.', 'g', 'o'})
	writeScanFile(t, dir, backslash, "package fixture\n\nfunc backslash() {}\n")
	assertPaths := func(label string, snapshot model.Snapshot, wantInvalid bool) {
		t.Helper()
		got := map[string]bool{}
		for _, function := range snapshot.Functions {
			got[function.File] = true
		}
		wantCount := 1
		if wantInvalid {
			wantCount++
		}
		if len(snapshot.Functions) != wantCount || !got[backslash] || got[invalidUTF8] != wantInvalid {
			t.Fatalf("%s functions = %#v", label, snapshot.Functions)
		}
	}
	directory, err := Directory(dir)
	if err != nil {
		t.Fatal(err)
	}
	assertPaths("directory", directory, false)
	gitForScan(t, dir, "add", ".")
	invalidSource := "package fixture\n\nfunc invalidUTF8() {}\n"
	oid := strings.TrimSpace(gitInputForScan(t, dir, invalidSource, "hash-object", "-w", "--stdin"))
	gitForScan(t, dir, "update-index", "--add", "--cacheinfo", "100644", oid, invalidUTF8)
	gitForScan(t, dir, "commit", "-m", "unusual path bytes")
	revision, err := Revision(context.Background(), dir, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	assertPaths("revision", revision, true)
}

func TestBlobsDoesNotTreatBackslashesAsConfigSeparators(t *testing.T) {
	source := []byte("package fixture\n\nfunc kept() {}\n")
	snapshot, err := Blobs("abc", []gitread.Blob{
		{BlobInfo: gitread.BlobInfo{Path: `nested\..\.slopradar.json`, Size: 1}, Content: []byte("{")},
		{BlobInfo: gitread.BlobInfo{Path: "source.go", Size: int64(len(source))}, Content: source},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Functions) != 1 || snapshot.Functions[0].File != "source.go" {
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

func TestRevisionCachePreservesLatin1PythonCloneTokens(t *testing.T) {
	dir := t.TempDir()
	gitForScan(t, dir, "init", "-b", "main")
	gitForScan(t, dir, "config", "user.name", "Slopradar Test")
	gitForScan(t, dir, "config", "user.email", "test@slopradar.invalid")
	source := "# coding: latin-1\ndef run():\n"
	for i := range clones.JscpdDefaultMinimumTokens {
		source += fmt.Sprintf("    value%d = '\xe9'\n", i)
	}
	for _, file := range []string{"a.py", "b.py"} {
		writeScanFile(t, dir, file, source)
	}
	gitForScan(t, dir, "add", ".")
	gitForScan(t, dir, "commit", "-m", "Latin-1 source")
	store := analysiscache.New(t.TempDir(), analysiscache.AnalyzerVersion)
	cold, err := RevisionWithCache(context.Background(), dir, "HEAD", store)
	if err != nil {
		t.Fatal(err)
	}
	if len(cold.Warnings) != 0 || len(cold.Clones) != 1 {
		t.Fatalf("warnings = %v, clones = %+v", cold.Warnings, cold.Clones)
	}
	warm, err := RevisionWithCache(context.Background(), dir, "HEAD", store)
	if err != nil {
		t.Fatal(err)
	}
	uncached, err := Revision(context.Background(), dir, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cold, warm) || !reflect.DeepEqual(cold, uncached) {
		t.Fatalf("clone tokens changed: cold %+v, warm %+v, uncached %+v", cold.Clones, warm.Clones, uncached.Clones)
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

func TestRevisionCacheSeparatesTypeScriptDeclarations(t *testing.T) {
	dir := t.TempDir()
	gitForScan(t, dir, "init", "-b", "main")
	gitForScan(t, dir, "config", "user.name", "Slopradar Test")
	gitForScan(t, dir, "config", "user.email", "test@slopradar.invalid")
	writeScanFile(t, dir, "reporter.d.ts", "export const reporter: Reporter\n")
	gitForScan(t, dir, "add", ".")
	gitForScan(t, dir, "commit", "-m", "declaration")
	store := analysiscache.New(t.TempDir(), "test")
	if _, err := RevisionWithCache(context.Background(), dir, "HEAD", store); err != nil {
		t.Fatal(err)
	}
	gitForScan(t, dir, "mv", "reporter.d.ts", "reporter.ts")
	gitForScan(t, dir, "commit", "-m", "ordinary TypeScript")
	if _, err := RevisionWithCache(context.Background(), dir, "HEAD", store); err == nil || !strings.Contains(err.Error(), "parse reporter.ts") {
		t.Fatalf("TypeScript scan error = %v", err)
	}
}

func gitForScan(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-c", "maintenance.autoDetach=false", "-c", "gc.autoDetach=false", "-C", dir}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, output)
	}
	return string(output)
}

func gitInputForScan(t *testing.T, dir, input string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-c", "maintenance.autoDetach=false", "-c", "gc.autoDetach=false", "-C", dir}, args...)...)
	command.Stdin = strings.NewReader(input)
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

func TestJavaScriptLineTerminatorsPreserveCloneCoverage(t *testing.T) {
	terminators := []struct {
		name               string
		text               string
		gitLineCoordinates bool
	}{
		{name: "lf", text: "\n", gitLineCoordinates: true},
		{name: "crlf", text: "\r\n", gitLineCoordinates: true},
		{name: "cr", text: "\r"},
		{name: "line separator", text: "\u2028"},
		{name: "paragraph separator", text: "\u2029"},
	}
	for _, terminator := range terminators {
		t.Run(terminator.name, func(t *testing.T) {
			source := []byte(strings.Join([]string{
				"function repeated(input) {",
				"  const alpha = input + 1 + 2 + 3 + 4 + 5;",
				"  const beta = alpha + 6 + 7 + 8 + 9 + 10;",
				"  // removed comment",
				"  const gamma = beta + 11 + 12 + 13 + 14 + 15;",
				"  if (gamma > input) return gamma;",
				"  return input;",
				"}",
			}, terminator.text))
			snapshot, err := Blobs("lines", []gitread.Blob{
				{BlobInfo: gitread.BlobInfo{Path: "a.js", Size: int64(len(source))}, Content: source},
				{BlobInfo: gitread.BlobInfo{Path: "b.js", Size: int64(len(source))}, Content: source},
			})
			if err != nil {
				t.Fatal(err)
			}
			totals := snapshot.Buckets[model.Source]
			if totals.SourceLines != 14 || totals.CloneLines != 14 || len(snapshot.Clones) != 1 {
				t.Fatalf("snapshot = %#v", snapshot)
			}
			for _, analysisPath := range snapshot.AnalysisPaths {
				if analysisPath.GitLineCoordinates != terminator.gitLineCoordinates {
					t.Fatalf("analysis path = %#v, want Git compatibility %t", analysisPath, terminator.gitLineCoordinates)
				}
			}
		})
	}
}

func TestJavaScriptFinalTemplateTokenKeepsExactlyFiveLineClone(t *testing.T) {
	terminators := []struct {
		name string
		text string
	}{
		{name: "lf", text: "\n"},
		{name: "crlf", text: "\r\n"},
		{name: "cr", text: "\r"},
		{name: "line separator", text: "\u2028"},
		{name: "paragraph separator", text: "\u2029"},
	}
	for _, terminator := range terminators {
		t.Run(terminator.name, func(t *testing.T) {
			left := javascriptFinalTemplateClone(terminator.text, "+")
			right := javascriptFinalTemplateClone(terminator.text, "-")
			snapshot, err := Blobs("template-lines", []gitread.Blob{
				{BlobInfo: gitread.BlobInfo{Path: "a.js", Size: int64(len(left))}, Content: left},
				{BlobInfo: gitread.BlobInfo{Path: "b.js", Size: int64(len(right))}, Content: right},
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(snapshot.Clones) != 1 || snapshot.Clones[0].A != (model.Range{File: "a.js", Start: 2, End: 6}) || snapshot.Clones[0].B != (model.Range{File: "b.js", Start: 2, End: 6}) || snapshot.Clones[0].Lines != 5 {
				t.Fatalf("clones = %#v", snapshot.Clones)
			}
			if totals := snapshot.Buckets[model.Source]; totals.SourceLines != 10 || totals.CloneLines != 10 {
				t.Fatalf("source totals = %#v", totals)
			}
		})
	}
}

func javascriptFinalTemplateClone(terminator, operator string) []byte {
	operands := make([]string, 30)
	for i := range operands {
		operands[i] = fmt.Sprintf("value%d", i)
	}
	template := "`" + strings.Join([]string{"one", "two", "three", "four", "five"}, terminator) + "`"
	return []byte("// ignored" + terminator + "const result = " + strings.Join(operands, " + ") + " + " + template + " " + operator + " tail")
}

func TestDeclarationTestFilesUseTestLineAndCloneBuckets(t *testing.T) {
	for _, suffix := range []string{".d.ts", ".d.mts", ".d.cts"} {
		t.Run(suffix, func(t *testing.T) {
			dir := t.TempDir()
			var source strings.Builder
			source.WriteString("export interface Example {\n")
			for i := range clones.JscpdDefaultMinimumTokens {
				fmt.Fprintf(&source, "field%d: string;\n", i)
			}
			source.WriteString("}\n")
			writeScanFile(t, dir, "first.test"+suffix, source.String())
			writeScanFile(t, dir, "second.spec"+suffix, source.String())
			gitForScan(t, dir, "init", "-b", "main")
			gitForScan(t, dir, "config", "user.name", "Slopradar Test")
			gitForScan(t, dir, "config", "user.email", "test@slopradar.invalid")
			gitForScan(t, dir, "add", ".")
			gitForScan(t, dir, "commit", "-m", "declaration tests")
			directory, err := Directory(dir)
			if err != nil {
				t.Fatal(err)
			}
			revision, err := Revision(context.Background(), dir, "HEAD")
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(directory.Buckets, revision.Buckets) || !reflect.DeepEqual(directory.Clones, revision.Clones) {
				t.Fatal("directory and Git buckets differ")
			}
			if len(directory.Warnings) != 0 || directory.Buckets[model.Source].SourceLines != 0 || directory.Buckets[model.Source].CloneLines != 0 || directory.Buckets[model.Tests].SourceLines == 0 || directory.Buckets[model.Tests].CloneLines == 0 {
				t.Fatalf("snapshot = %#v", directory)
			}
		})
	}
}
