package scan

import (
	"context"
	"fmt"
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

		{"excluded target", map[string]string{".slopradar.json": `{"excludes":["src/bindings.rs"]}`, "src/lib.rs": "mod bindings; fn public() {}", "src/bindings.rs": "fn binding() {}"}, nil, false},
		{"generated target", map[string]string{"src/lib.rs": "mod bindings; fn public() {}", "src/bindings.rs": "// @generated\nfn binding() {}"}, nil, false},
		{"excluded directory", map[string]string{".slopradar.json": `{"excludes":["src/generated/"]}`, "src/lib.rs": "#[path=\"generated/bindings.rs\"] mod bindings; fn public() {}", "src/generated/bindings.rs": "fn binding() {}"}, nil, false},
		{"missing", map[string]string{"src/lib.rs": "#[cfg(test)] mod missing;", "src/other.rs": "fn other() {}"}, nil, true},
		{"ambiguous layouts", map[string]string{"src/lib.rs": "#[cfg(test)] mod helpers;", "src/helpers.rs": "fn helper() {}", "src/helpers/mod.rs": "fn helper() {}"}, nil, true},
		{"conditional path", map[string]string{"src/lib.rs": "#[cfg(test)] #[cfg_attr(unix, path=\"other.rs\")] mod helpers;", "src/helpers.rs": "fn helper() {}", "src/other.rs": "fn other() {}"}, nil, true},

		{"integration crate root", map[string]string{"tests/integration.rs": "mod common; fn entry() {}", "tests/common/mod.rs": "#[path=\"../../src/helpers.rs\"] mod helpers;", "src/helpers.rs": "fn helper() {}"}, []string{"tests/integration.rs", "src/helpers.rs"}, false},
		{"example crate root", map[string]string{"examples/demo.rs": "#[cfg(test)] mod helpers; fn example() {}", "examples/helpers/mod.rs": "fn helper() {}"}, []string{"examples/helpers/mod.rs"}, false},
		{"test path inheritance", map[string]string{"tests/integration.rs": "#[path=\"../src/helpers.rs\"] mod helpers;", "src/helpers.rs": "fn helper() {}"}, []string{"src/helpers.rs"}, false},
		{"nested main is a module", map[string]string{"src/lib.rs": "#[cfg(test)] mod foo;", "src/foo.rs": "mod main;", "src/foo/main.rs": "mod child; fn entry() {}", "src/foo/main/child.rs": "fn child() {}"}, []string{"src/foo/main.rs", "src/foo/main/child.rs"}, false},
		{"nested lib is a module", map[string]string{"src/lib.rs": "#[cfg(test)] mod foo;", "src/foo.rs": "mod lib;", "src/foo/lib.rs": "mod child; fn entry() {}", "src/foo/lib/child.rs": "fn child() {}"}, []string{"src/foo/lib.rs", "src/foo/lib/child.rs"}, false},
		{"multifile src/bin root", map[string]string{"src/bin/demo/main.rs": "#[cfg(test)] mod child;", "src/bin/demo/child.rs": "fn child() {}"}, []string{"src/bin/demo/child.rs"}, false},
		{"multifile tests root", map[string]string{"tests/demo/main.rs": "#[cfg(test)] mod child;", "tests/demo/child.rs": "fn child() {}"}, []string{"tests/demo/child.rs"}, false},
		{"multifile examples root", map[string]string{"examples/demo/main.rs": "#[cfg(test)] mod child;", "examples/demo/child.rs": "fn child() {}"}, []string{"examples/demo/child.rs"}, false},
		{"multifile benches root", map[string]string{"benches/demo/main.rs": "#[cfg(test)] mod child;", "benches/demo/child.rs": "fn child() {}"}, []string{"benches/demo/child.rs"}, false},
		{"build script root", map[string]string{"build.rs": "mod helper; fn main() {}", "helper.rs": "fn helper() {}"}, nil, false},
		{"workspace build script root", map[string]string{"crates/demo/Cargo.toml": "[package]\nname=\"demo\"\nversion=\"0.1.0\"", "crates/demo/build.rs": "mod helper; fn main() {}", "crates/demo/helper.rs": "fn helper() {}", "crates/demo/src/lib.rs": "fn public() {}"}, nil, false},
		{"nested build is a module", map[string]string{"src/lib.rs": "#[cfg(test)] mod build;", "src/build.rs": "mod child; fn entry() {}", "src/build/child.rs": "fn child() {}"}, []string{"src/build.rs", "src/build/child.rs"}, false},
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

func TestIgnoredRustModulesAllowDirectoryAndGitReports(t *testing.T) {
	for _, kind := range []string{"generated", "excluded", "excluded-directory"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			gitForScan(t, dir, "init", "-b", "main")
			gitForScan(t, dir, "config", "user.name", "Slopradar Test")
			gitForScan(t, dir, "config", "user.email", "test@slopradar.invalid")
			target := "bindings.rs"
			content := "fn bindings() {}\n"
			if kind == "generated" {
				content = "// @generated\n" + content
			}
			if kind == "excluded" {
				writeScanFile(t, dir, model.ConfigFile, `{"excludes":["bindings.rs"]}`)
			}
			if kind == "excluded-directory" {
				target = "generated/bindings.rs"
				if err := os.Mkdir(filepath.Join(dir, "generated"), 0o700); err != nil {
					t.Fatal(err)
				}
				writeScanFile(t, dir, model.ConfigFile, `{"excludes":["generated/"]}`)
			}
			writeScanFile(t, dir, target, content)
			writeScanFile(t, dir, "lib.rs", fmt.Sprintf("#[path=%q] mod bindings; fn public() {}\n", target))
			gitForScan(t, dir, "add", ".")
			gitForScan(t, dir, "commit", "-m", "ignored bindings")
			directory, err := Directory(dir)
			if err != nil {
				t.Fatal(err)
			}
			revision, err := Revision(context.Background(), dir, "HEAD")
			if err != nil {
				t.Fatal(err)
			}
			for _, s := range []model.Snapshot{directory, revision} {
				if len(s.Functions) != 1 || len(s.Warnings) != 0 {
					t.Fatalf("snapshot=%#v", s)
				}
				if _, err := diff.Build(s, s, []string{"lib.rs"}); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestBuildRootSurvivesOmittedSource(t *testing.T) {
	for _, kind := range []string{"generated", "excluded-file", "excluded-directory"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.MkdirAll(filepath.Join(dir, "crates/demo/src"), 0o700); err != nil {
				t.Fatal(err)
			}
			gitForScan(t, dir, "init", "-b", "main")
			gitForScan(t, dir, "config", "user.name", "Slopradar Test")
			gitForScan(t, dir, "config", "user.email", "test@slopradar.invalid")
			writeScanFile(t, dir, "crates/demo/Cargo.toml", "[package]\nname=\"demo\"\nversion=\"0.1.0\"\n")
			writeScanFile(t, dir, "crates/demo/build.rs", "mod helper; fn main() {}\n")
			writeScanFile(t, dir, "crates/demo/helper.rs", "pub fn helper() {}\n")
			source := "pub fn library() {}\n"
			switch kind {
			case "generated":
				source = "// @generated\n" + source
			case "excluded-file":
				writeScanFile(t, dir, ".slopradar.json", `{"excludes":["crates/demo/src/lib.rs", "**/Cargo.toml"]}`)
			case "excluded-directory":
				writeScanFile(t, dir, ".slopradar.json", `{"excludes":["crates/demo/src/"]}`)
			}
			writeScanFile(t, dir, "crates/demo/src/lib.rs", source)
			gitForScan(t, dir, "add", ".")
			gitForScan(t, dir, "commit", "-m", "fixture")
			gitSnapshot, err := Revision(context.Background(), dir, "HEAD")
			if err != nil {
				t.Fatal(err)
			}
			directory, err := Directory(dir)
			if err != nil {
				t.Fatal(err)
			}
			if len(gitSnapshot.Warnings) != 0 || len(directory.Warnings) != 0 || len(gitSnapshot.Functions) != 2 || !reflect.DeepEqual(gitSnapshot.Functions, directory.Functions) {
				t.Fatalf("Git=%#v directory=%#v", gitSnapshot, directory)
			}
			if _, err := diff.Build(gitSnapshot, directory, nil); err != nil {
				t.Fatal(err)
			}
		})
	}
}
