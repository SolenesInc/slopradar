package scan

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	analysiscache "github.com/SolenesInc/slopradar/internal/cache"
	"github.com/SolenesInc/slopradar/internal/diff"
	"github.com/SolenesInc/slopradar/internal/gitread"
	"github.com/SolenesInc/slopradar/internal/model"
)

func TestRustExternalModuleScopes(t *testing.T) {
	for _, test := range []struct {
		name     string
		files    map[string]string
		tests    []string
		warnings bool
	}{
		{"flat", map[string]string{"src/lib.rs": "#[cfg(test)] mod helpers;", "src/helpers.rs": "fn helper() {}"}, []string{"src/helpers.rs"}, false},
		{"directory transitive", map[string]string{"src/lib.rs": "#[cfg(all(test, unix))] mod helpers;", "src/helpers/mod.rs": "mod child; fn helper() {}", "src/helpers/child.rs": "fn child() {}"}, []string{"src/helpers/mod.rs", "src/helpers/child.rs"}, false},
		{"file transitive", map[string]string{"src/lib.rs": "#[cfg(test)] mod helpers;", "src/helpers.rs": "mod child; fn helper() {}", "src/helpers/child.rs": "fn child() {}"}, []string{"src/helpers.rs", "src/helpers/child.rs"}, false},
		{"inline", map[string]string{"src/lib.rs": "#[cfg(test)] mod helpers { mod child; }", "src/helpers/child.rs": "fn child() {}"}, []string{"src/helpers/child.rs"}, false},
		{"raw path", map[string]string{"src/lib.rs": "#[cfg(test)] #[path = r#\"elsewhere.rs\"#] mod helpers;", "src/elsewhere.rs": "fn helper() {}"}, []string{"src/elsewhere.rs"}, false},
		{"path relative to declaring file", map[string]string{"src/lib.rs": "mod ordinary;", "src/ordinary.rs": "#[cfg(test)] #[path = \"elsewhere.rs\"] mod helpers;", "src/elsewhere.rs": "fn helper() {}"}, []string{"src/elsewhere.rs"}, false},
		{"nested path", map[string]string{"src/lib.rs": "mod ordinary;", "src/ordinary.rs": "#[cfg(test)] mod inline { #[path = \"elsewhere.rs\"] mod helpers; }", "src/ordinary/inline/elsewhere.rs": "fn helper() {}"}, []string{"src/ordinary/inline/elsewhere.rs"}, false},
		{"inline path override", map[string]string{"src/lib.rs": "#[cfg(test)] #[path = \"custom\"] mod inline { #[path = \"elsewhere.rs\"] mod helpers; }", "src/custom/elsewhere.rs": "fn helper() {}"}, []string{"src/custom/elsewhere.rs"}, false},
		{"shared production", map[string]string{"src/lib.rs": "#[cfg(test)] #[path=\"shared.rs\"] mod tests; #[path=\"shared.rs\"] mod production;", "src/shared.rs": "fn shared() {}"}, nil, false},
		{"unknown feature", map[string]string{"src/lib.rs": "#[cfg(any(test, feature=\"production\"))] mod shared;", "src/shared.rs": "fn shared() {}"}, nil, false},
		{"file inner cfg", map[string]string{"src/lib.rs": "mod helpers;", "src/helpers.rs": "#![cfg(test)]\nmod child; fn helper() {}", "src/helpers/child.rs": "fn child() {}"}, []string{"src/helpers.rs", "src/helpers/child.rs"}, false},
		{"inline inner cfg", map[string]string{"src/lib.rs": "mod inline { #![cfg(test)] mod child; }", "src/inline/child.rs": "fn child() {}"}, []string{"src/inline/child.rs"}, false},

		{"configured test path", map[string]string{".slopradar.json": `{"test_globs":["src/helpers.rs"]}`, "src/lib.rs": "mod helpers;", "src/helpers.rs": "mod child; fn helper() {}", "src/helpers/child.rs": "fn child() {}"}, []string{"src/helpers.rs", "src/helpers/child.rs"}, false},
		{"absolute path stays outside snapshot", map[string]string{"src/lib.rs": "#[cfg(test)] #[path=\"/helpers.rs\"] mod helpers;", "src/helpers.rs": "fn helper() {}"}, nil, true},
		{"missing", map[string]string{"src/lib.rs": "#[cfg(test)] mod missing;", "src/other.rs": "fn other() {}"}, nil, true},
		{"ambiguous layouts", map[string]string{"src/lib.rs": "#[cfg(test)] mod helpers;", "src/helpers.rs": "fn helper() {}", "src/helpers/mod.rs": "fn helper() {}"}, nil, true},
		{"conditional path", map[string]string{"src/lib.rs": "#[cfg(test)] #[cfg_attr(unix, path=\"other.rs\")] mod helpers;", "src/helpers.rs": "fn helper() {}", "src/other.rs": "fn other() {}"}, nil, true},
		{"test path inheritance", map[string]string{"tests/integration.rs": "#[path=\"../src/helpers.rs\"] mod helpers;", "src/helpers.rs": "fn helper() {}"}, []string{"src/helpers.rs"}, false},
		{"unrooted cycle", map[string]string{"src/a.rs": "#[path=\"b.rs\"] mod b; fn a() {}", "src/b.rs": "#[path=\"a.rs\"] mod a; fn b() {}"}, nil, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var blobs []gitread.Blob
			for file, content := range test.files {
				blobs = append(blobs, gitread.Blob{BlobInfo: gitread.BlobInfo{Path: file, Size: int64(len(content))}, Content: []byte(content)})
			}
			got, err := Blobs("tree", blobs)
			if err != nil {
				t.Fatal(err)
			}
			if (len(got.Warnings) > 0) != test.warnings {
				t.Fatalf("warnings = %v", got.Warnings)
			}
			for _, f := range got.Functions {
				want := model.Source
				for _, file := range test.tests {
					if f.File == file {
						want = model.Tests
					}
				}
				if f.Bucket != want {
					t.Errorf("%s bucket = %s, want %s", f.File, f.Bucket, want)
				}
			}
			for i, j := 0, len(blobs)-1; i < j; i, j = i+1, j-1 {
				blobs[i], blobs[j] = blobs[j], blobs[i]
			}
			reversed, err := Blobs("tree", blobs)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, reversed) {
				t.Fatal("classification depends on blob order")
			}
		})
	}
}

func TestRustParentChangeReclassifiesCachedChildrenAndDiff(t *testing.T) {
	dir := t.TempDir()
	gitForScan(t, dir, "init", "-b", "main")
	gitForScan(t, dir, "config", "user.name", "Slopradar Test")
	gitForScan(t, dir, "config", "user.email", "test@slopradar.invalid")
	for _, sub := range []string{"src", "tests"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	source := "fn helper(x: bool) {\n" + strings.Repeat(" if x { work(); }\n", 12) + "}\n"
	writeScanFile(t, dir, "src/lib.rs", "mod helpers;\n")
	writeScanFile(t, dir, "src/helpers.rs", source)
	writeScanFile(t, dir, "tests/copy.rs", source)
	gitForScan(t, dir, "add", ".")
	gitForScan(t, dir, "commit", "-m", "production helper")
	store := analysiscache.New(filepath.Join(t.TempDir(), "cache"), analysiscache.AnalyzerVersion)
	before, err := RevisionWithCache(context.Background(), dir, "HEAD", store)
	if err != nil {
		t.Fatal(err)
	}
	writeScanFile(t, dir, "src/lib.rs", "#[cfg(test)] mod helpers;\n")
	gitForScan(t, dir, "add", ".")
	gitForScan(t, dir, "commit", "-m", "test helper")
	after, err := RevisionWithCache(context.Background(), dir, "HEAD", store)
	if err != nil {
		t.Fatal(err)
	}
	uncached, err := Revision(context.Background(), dir, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, uncached) {
		t.Fatal("warm cache retained parent scope")
	}
	directory, err := Directory(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after.Functions, directory.Functions) || !reflect.DeepEqual(after.Buckets, directory.Buckets) {
		t.Fatal("Git and directory scope differ")
	}
	if before.Buckets[model.Source].Functions != 1 || after.Buckets[model.Source].Functions != 0 || after.Buckets[model.Tests].Functions != 2 {
		t.Fatalf("buckets before=%v after=%v", before.Buckets, after.Buckets)
	}
	if before.Buckets[model.Source].CloneLines == 0 || after.Buckets[model.Source].CloneLines != 0 || after.Buckets[model.Tests].CloneLines <= before.Buckets[model.Tests].CloneLines {
		t.Fatalf("clone buckets before=%v after=%v", before.Buckets, after.Buckets)
	}

	restored, err := RevisionWithCache(context.Background(), dir, "HEAD^", store)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(restored, before) {
		t.Fatal("revisiting production snapshot retained test scope")
	}
	change, err := diff.Build(before, after, []string{"src/lib.rs"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(change.Touched, []string{"src/helpers.rs", "src/lib.rs"}) {
		t.Fatalf("touched = %v", change.Touched)
	}
	if change.Buckets[model.Source].MassRemovedOverCC10 == 0 || change.Buckets[model.Tests].MassAddedOverCC10 == 0 {
		t.Fatalf("delta buckets = %v", change.Buckets)
	}
}
