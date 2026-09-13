package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SolenesInc/slopradar/internal/model"
)

func TestDiffRetainsCompleteLexicalOwner(t *testing.T) {
	for _, test := range []struct {
		file, before, after, name string
		line                      int
	}{
		{"modules.rs", "mod alpha {\n mod inner {\n  fn run() { if first {} }\n }\n}\nmod beta {\n fn run() { if first {} if second {} }\n}\n", "mod alpha {\n mod inner {\n  fn run() { if first {} if second {} }\n }\n}\nmod beta {\n fn run() { if first {} if second {} }\n}\n", "alpha::inner::run", 3},
		{"classes.py", "class OuterA:\n class Inner:\n  def run(self):\n   if first: return 1\nclass OuterB:\n class Inner:\n  def run(self):\n   if first: return 1\n   if second: return 2\n", "class OuterA:\n class Inner:\n  def run(self):\n   if first: return 1\n   if second: return 2\nclass OuterB:\n class Inner:\n  def run(self):\n   if first: return 1\n   if second: return 2\n", "OuterA.Inner.run", 3},
	} {
		t.Run(test.file, func(t *testing.T) {
			dir := t.TempDir()
			gitCommand(t, dir, "init", "-b", "main")
			gitCommand(t, dir, "config", "user.name", "Slopradar Test")
			gitCommand(t, dir, "config", "user.email", "test@slopradar.invalid")
			writeFile(t, dir, test.file, test.before)
			gitCommand(t, dir, "add", ".")
			gitCommand(t, dir, "commit", "-m", "before")
			writeFile(t, dir, test.file, test.after)
			gitCommand(t, dir, "add", ".")
			gitCommand(t, dir, "commit", "-m", "after")
			t.Chdir(dir)
			var output bytes.Buffer
			if err := run(context.Background(), []string{"diff", "--base", "HEAD^", "--head", "HEAD", "--format=json", "--no-cache"}, &output); err != nil {
				t.Fatal(err)
			}
			var got model.Diff
			if err := json.Unmarshal(output.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if len(got.Functions) != 1 {
				t.Fatalf("function deltas = %#v", got.Functions)
			}
			f := got.Functions[0]
			if f.Name != test.name || f.Before == nil || f.After == nil || f.Before.Line != test.line || f.After.Line != test.line || f.Before.CC != 2 || f.After.CC != 3 {
				t.Fatalf("function delta = %#v", f)
			}
		})
	}
}

func TestScanGeneratedMarkersRespectJavaScriptTerminators(t *testing.T) {
	dir := t.TempDir()
	terminators := []string{"\n", "\r\n", "\r", "\u2028", "\u2029"}
	for index, terminator := range terminators {
		writeFile(t, dir, fmt.Sprintf("handwritten%d.ts", index), "// header"+terminator+"const marker = \"@generated\"; function visible() {}\n")
		writeFile(t, dir, fmt.Sprintf("generated%d.ts", index), "// @generated"+terminator+"function hidden() {}\n")
	}
	gitCommand(t, dir, "init", "-b", "main")
	gitCommand(t, dir, "config", "user.name", "Slopradar Test")
	gitCommand(t, dir, "config", "user.email", "test@slopradar.invalid")
	gitCommand(t, dir, "add", ".")
	gitCommand(t, dir, "commit", "-m", "terminator fixtures")
	t.Chdir(dir)
	for _, target := range []string{dir, "HEAD"} {
		var output bytes.Buffer
		if err := run(context.Background(), []string{"scan", target, "--format=json", "--no-cache"}, &output); err != nil {
			t.Fatal(err)
		}
		var got model.Snapshot
		if err := json.Unmarshal(output.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if len(got.Functions) != len(terminators) {
			t.Fatalf("scan %s functions = %#v", target, got.Functions)
		}
		for _, f := range got.Functions {
			if !strings.HasPrefix(f.File, "handwritten") || f.Name != "visible" || f.Line != 2 {
				t.Fatalf("function = %#v", f)
			}
		}
	}
}

func TestDiffReportsSwappedNamespaceMetrics(t *testing.T) {
	dir := t.TempDir()
	gitCommand(t, dir, "init", "-b", "main")
	gitCommand(t, dir, "config", "user.name", "Slopradar Test")
	gitCommand(t, dir, "config", "user.email", "test@slopradar.invalid")
	writeFile(t, dir, "owners.ts", "namespace A { export function run(x: boolean) { if (x) {} } }\nnamespace B { export function run(x: boolean) {} }\n")
	gitCommand(t, dir, "add", ".")
	gitCommand(t, dir, "commit", "-m", "before")
	writeFile(t, dir, "owners.ts", "namespace A { export function run(x: boolean) {} }\nnamespace B { export function run(x: boolean) { if (x) {} } }\n")
	gitCommand(t, dir, "add", ".")
	gitCommand(t, dir, "commit", "-m", "after")
	t.Chdir(dir)
	var output bytes.Buffer
	if err := run(context.Background(), []string{"diff", "--base", "HEAD^", "--head", "HEAD", "--format=json", "--no-cache"}, &output); err != nil {
		t.Fatal(err)
	}
	var got model.Diff
	if err := json.Unmarshal(output.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Functions) != 2 {
		t.Fatalf("deltas = %#v", got.Functions)
	}
	for i, f := range got.Functions {
		name, before, after := "A.run", 2, 1
		if i == 1 {
			name, before, after = "B.run", 1, 2
		}
		if f.Name != name || f.Before == nil || f.After == nil || f.Before.CC != before || f.After.CC != after || f.Before.Line != i+1 || f.After.Line != i+1 {
			t.Fatalf("delta = %#v", f)
		}
	}
}

func TestScanExcludesHashbangFromLineTotals(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "executable.mjs", "#!/usr/bin/env node\nfunction f() {}\n")
	gitCommand(t, dir, "init", "-b", "main")
	gitCommand(t, dir, "config", "user.name", "Slopradar Test")
	gitCommand(t, dir, "config", "user.email", "test@slopradar.invalid")
	gitCommand(t, dir, "add", ".")
	gitCommand(t, dir, "commit", "-m", "executable")
	t.Chdir(dir)
	for _, target := range []string{dir, "HEAD"} {
		var output bytes.Buffer
		if err := run(context.Background(), []string{"scan", target, "--format=json", "--no-cache"}, &output); err != nil {
			t.Fatal(err)
		}
		var got model.Snapshot
		if err := json.Unmarshal(output.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got.Buckets[model.Source].SourceLines != 1 || len(got.Functions) != 1 || got.Functions[0].Line != 2 {
			t.Fatalf("scan %s = %#v", target, got)
		}
	}
}

func TestDiffReportsSwappedGoCompositeMetrics(t *testing.T) {
	dir := t.TempDir()
	gitCommand(t, dir, "init", "-b", "main")
	gitCommand(t, dir, "config", "user.name", "Slopradar Test")
	gitCommand(t, dir, "config", "user.email", "test@slopradar.invalid")
	writeFile(t, dir, "owners.go", "package fixture; var a = Hooks{Run: func(x bool) { if x {} }}\nvar b = Hooks{Run: func(x bool) {}}\n")
	gitCommand(t, dir, "add", ".")
	gitCommand(t, dir, "commit", "-m", "before")
	writeFile(t, dir, "owners.go", "package fixture; var a = Hooks{Run: func(x bool) {}}\nvar b = Hooks{Run: func(x bool) { if x {} }}\n")
	gitCommand(t, dir, "add", ".")
	gitCommand(t, dir, "commit", "-m", "after")
	t.Chdir(dir)
	var output bytes.Buffer
	if err := run(context.Background(), []string{"diff", "--base", "HEAD^", "--head", "HEAD", "--format=json", "--no-cache"}, &output); err != nil {
		t.Fatal(err)
	}
	var got model.Diff
	if err := json.Unmarshal(output.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Functions) != 2 {
		t.Fatalf("deltas = %#v", got.Functions)
	}
	for i, f := range got.Functions {
		name, before, after := "a.Run", 2, 1
		if i == 1 {
			name, before, after = "b.Run", 1, 2
		}
		if f.Name != name || f.Before == nil || f.After == nil || f.Before.CC != before || f.After.CC != after || f.Before.Line != i+1 || f.After.Line != i+1 {
			t.Fatalf("delta = %#v", f)
		}
	}
}

func TestNestedRustEntrypointNamesAllowReports(t *testing.T) {
	for _, name := range []string{"main", "lib"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			gitCommand(t, dir, "init", "-b", "main")
			gitCommand(t, dir, "config", "user.name", "Slopradar Test")
			gitCommand(t, dir, "config", "user.email", "test@slopradar.invalid")
			if err := os.MkdirAll(filepath.Join(dir, "src/foo", name), 0o700); err != nil {
				t.Fatal(err)
			}
			writeFile(t, dir, "src/lib.rs", "#[cfg(test)] mod foo;\n")
			writeFile(t, dir, "src/foo.rs", "mod "+name+";\n")
			writeFile(t, dir, "src/foo/"+name+".rs", "mod child; fn entry() {}\n")
			child := "src/foo/" + name + "/child.rs"
			writeFile(t, dir, child, "fn child(x: bool) {}\n")
			gitCommand(t, dir, "add", ".")
			gitCommand(t, dir, "commit", "-m", "before")
			writeFile(t, dir, child, "fn child(x: bool) { if x {} }\n")
			gitCommand(t, dir, "add", ".")
			gitCommand(t, dir, "commit", "-m", "after")
			t.Chdir(dir)
			for _, target := range []string{dir, "HEAD"} {
				var output bytes.Buffer
				if err := run(context.Background(), []string{"scan", target, "--format=json", "--no-cache"}, &output); err != nil {
					t.Fatal(err)
				}
				var got model.Snapshot
				if err := json.Unmarshal(output.Bytes(), &got); err != nil {
					t.Fatal(err)
				}
				if len(got.Warnings) != 0 || got.Buckets[model.Source].Functions != 0 || got.Buckets[model.Tests].Functions != 2 {
					t.Fatalf("snapshot = %#v", got)
				}
			}
			var output bytes.Buffer
			if err := run(context.Background(), []string{"diff", "--base", "HEAD^", "--head", "HEAD", "--trend", "12", "--format=json", "--no-cache"}, &output); err != nil {
				t.Fatal(err)
			}
			var got model.Diff
			if err := json.Unmarshal(output.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if len(got.Functions) != 1 || got.Functions[0].File != child || got.Functions[0].Before.CC != 1 || got.Functions[0].After.CC != 2 || got.Functions[0].After.Bucket != model.Tests || len(got.Trend) == 0 {
				t.Fatalf("diff = %#v", got)
			}
		})
	}
}

func TestDiffReportsSwappedOwnedCallbacks(t *testing.T) {
	dir := t.TempDir()
	gitCommand(t, dir, "init", "-b", "main")
	gitCommand(t, dir, "config", "user.name", "Slopradar Test")
	gitCommand(t, dir, "config", "user.email", "test@slopradar.invalid")
	writeFile(t, dir, "owners.ts", "const a = map((x) => { if (x) {} });\nconst b = map((x) => {});\n")
	gitCommand(t, dir, "add", ".")
	gitCommand(t, dir, "commit", "-m", "before")
	writeFile(t, dir, "owners.ts", "const a = map((x) => {});\nconst b = map((x) => { if (x) {} });\n")
	gitCommand(t, dir, "add", ".")
	gitCommand(t, dir, "commit", "-m", "after")
	t.Chdir(dir)
	var output bytes.Buffer
	if err := run(context.Background(), []string{"diff", "--base", "HEAD^", "--head", "HEAD", "--format=json", "--no-cache"}, &output); err != nil {
		t.Fatal(err)
	}
	var got model.Diff
	if err := json.Unmarshal(output.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Functions) != 2 {
		t.Fatalf("deltas = %#v", got.Functions)
	}
	for i, f := range got.Functions {
		name, before, after := "a.cb:map", 2, 1
		if i == 1 {
			name, before, after = "b.cb:map", 1, 2
		}
		if f.Name != name || f.Before == nil || f.After == nil || f.Before.CC != before || f.After.CC != after || f.Before.Line != i+1 || f.After.Line != i+1 {
			t.Fatalf("delta = %#v", f)
		}
	}
}

func TestDiffReportsSwappedNestedClassMetrics(t *testing.T) {
	dir := t.TempDir()
	gitCommand(t, dir, "init", "-b", "main")
	gitCommand(t, dir, "config", "user.name", "Slopradar Test")
	gitCommand(t, dir, "config", "user.email", "test@slopradar.invalid")
	writeFile(t, dir, "owners.ts", "class A { static child = class Inner { run(x: boolean) { if (x) {} } } }\nclass B { static child = class Inner { run(x: boolean) {} } }\n")
	gitCommand(t, dir, "add", ".")
	gitCommand(t, dir, "commit", "-m", "before")
	writeFile(t, dir, "owners.ts", "class A { static child = class Inner { run(x: boolean) {} } }\nclass B { static child = class Inner { run(x: boolean) { if (x) {} } } }\n")
	gitCommand(t, dir, "add", ".")
	gitCommand(t, dir, "commit", "-m", "after")
	t.Chdir(dir)
	var output bytes.Buffer
	if err := run(context.Background(), []string{"diff", "--base", "HEAD^", "--head", "HEAD", "--format=json", "--no-cache"}, &output); err != nil {
		t.Fatal(err)
	}
	var got model.Diff
	if err := json.Unmarshal(output.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Functions) != 2 {
		t.Fatalf("deltas = %#v", got.Functions)
	}
	for i, f := range got.Functions {
		name, before, after := "A.static child.Inner.run", 2, 1
		if i == 1 {
			name, before, after = "B.static child.Inner.run", 1, 2
		}
		if f.Name != name || f.Before == nil || f.After == nil || f.Before.CC != before || f.After.CC != after || f.Before.Line != i+1 || f.After.Line != i+1 {
			t.Fatalf("delta = %#v", f)
		}
	}
}

func TestDiffReportsSwappedPythonCollectionMetrics(t *testing.T) {
	dir := t.TempDir()
	gitCommand(t, dir, "init", "-b", "main")
	gitCommand(t, dir, "config", "user.name", "Slopradar Test")
	gitCommand(t, dir, "config", "user.email", "test@slopradar.invalid")
	writeFile(t, dir, "owners.py", "handlers = {\"a\": lambda x: 1 if x else 0,\n\"b\": lambda x: 0}\n")
	gitCommand(t, dir, "add", ".")
	gitCommand(t, dir, "commit", "-m", "before")
	writeFile(t, dir, "owners.py", "handlers = {\"a\": lambda x: 0,\n\"b\": lambda x: 1 if x else 0}\n")
	gitCommand(t, dir, "add", ".")
	gitCommand(t, dir, "commit", "-m", "after")
	t.Chdir(dir)
	var output bytes.Buffer
	if err := run(context.Background(), []string{"diff", "--base", "HEAD^", "--head", "HEAD", "--format=json", "--no-cache"}, &output); err != nil {
		t.Fatal(err)
	}
	var got model.Diff
	if err := json.Unmarshal(output.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Functions) != 2 {
		t.Fatalf("deltas = %#v", got.Functions)
	}
	for i, f := range got.Functions {
		name, before, after := `handlers["a"]`, 2, 1
		if i == 1 {
			name, before, after = `handlers["b"]`, 1, 2
		}
		if f.Name != name || f.Before == nil || f.After == nil || f.Before.CC != before || f.After.CC != after || f.Before.Line != i+1 || f.After.Line != i+1 {
			t.Fatalf("delta = %#v", f)
		}
	}
}
