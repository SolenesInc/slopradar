package typescript

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/SolenesInc/slopradar/internal/diff"
	"github.com/SolenesInc/slopradar/internal/lang"
	"github.com/SolenesInc/slopradar/internal/model"
)

func TestAnalyzeGolden(t *testing.T) {
	root := filepath.Join("..", "..", "..", "testdata", "typescript")
	file := filepath.Join(root, "functions.ts")
	source, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	result, err := Analyze("testdata/typescript/functions.ts", source)
	if err != nil {
		t.Fatal(err)
	}
	wantData, err := os.ReadFile(filepath.Join(root, "functions.golden.json"))
	if err != nil {
		t.Fatal(err)
	}
	var want []model.Function
	if err := json.Unmarshal(wantData, &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.Functions, want) {
		got, _ := json.MarshalIndent(result.Functions, "", "  ")
		t.Fatalf("functions differ\ngot: %s\nwant: %s", got, wantData)
	}
	if len(result.Comments) != 1 {
		t.Fatalf("comments = %#v", result.Comments)
	}
}

func TestAnalyzeUsingDeclaration(t *testing.T) {
	result, err := Analyze("using.ts", []byte("export async function run() { await using value = acquire(); if (value) return value; }"))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Functions) != 1 || result.Functions[0].Name != "run" || result.Functions[0].CC != 2 {
		t.Fatalf("functions = %#v", result.Functions)
	}
}

func TestAnalyzeDeclarationFiles(t *testing.T) {
	root := filepath.Join("..", "..", "..", "testdata", "typescript")
	source, err := os.ReadFile(filepath.Join(root, "declarations.d.ts"))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"declarations.d.ts", "declarations.d.mts", "declarations.d.cts"} {
		result, err := Analyze(file, source)
		if err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		if len(result.Functions) != 0 {
			t.Fatalf("%s functions = %#v", file, result.Functions)
		}
		if len(result.Tokens) == 0 {
			t.Fatalf("%s returned no tokens", file)
		}
	}
}

func TestAnalyzeAcceptsUppercaseExtensions(t *testing.T) {
	result, err := Analyze("SOURCE.TS", []byte("function run() {}"))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Functions) != 1 || result.Functions[0].File != "SOURCE.TS" {
		t.Fatalf("functions = %#v", result.Functions)
	}
}

func TestAnalyzeAcceptsNonUTF8UnixFilename(t *testing.T) {
	file := string([]byte{'s', 'o', 'u', 'r', 'c', 'e', 0xff, '.', 't', 's'})
	result, err := Analyze(file, []byte("function run() {}"))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Functions) != 1 || result.Functions[0].File != file {
		t.Fatalf("functions = %#v", result.Functions)
	}
}

func TestAnalyzeEmptyAndInvalidUTF8(t *testing.T) {
	empty, err := Analyze("empty.ts", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(empty.Functions) != 0 || len(empty.Comments) != 0 || len(empty.Tokens) != 0 {
		t.Fatalf("empty analysis = %#v", empty)
	}
	invalid, err := Analyze("invalid.ts", []byte{0xff})
	if err == nil || !strings.Contains(err.Error(), "native Oxc bridge status=1: source is not UTF-8") {
		t.Fatalf("invalid analysis = %#v, error = %v", invalid, err)
	}
}

func TestAnalyzeRejectsDiagnosticsWithoutMetrics(t *testing.T) {
	result, err := Analyze("invalid.ts", []byte("export function broken( {"))
	if err == nil || !strings.Contains(err.Error(), "invalid TypeScript or JavaScript syntax") {
		t.Fatalf("result = %#v, error = %v", result, err)
	}
	if len(result.Functions) != 0 || len(result.Tokens) != 0 {
		t.Fatalf("parse failure returned metrics: %#v", result)
	}
}

func TestAnalyzePreservesOriginalTokensAndAssignmentNames(t *testing.T) {
	result, err := Analyze("names.ts", []byte("target.handler = () => { /* marker */ return 'é'; };"))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Functions) != 1 || result.Functions[0].Name != "target.handler" {
		t.Fatalf("functions = %#v", result.Functions)
	}
	if len(result.Comments) != 1 {
		t.Fatalf("comments = %#v", result.Comments)
	}
	foundUnicode := false
	for _, token := range result.Tokens {
		if token.Text == "'é'" {
			foundUnicode = true
		}
		if strings.Contains(token.Text, "marker") {
			t.Fatalf("comment appeared in token stream: %#v", token)
		}
	}
	if !foundUnicode {
		t.Fatalf("tokens = %#v", result.Tokens)
	}
}

func TestAnalyzeConsumesOwnershipHintsAtFunctionBoundaries(t *testing.T) {
	result, err := Analyze("ownership.ts", []byte(`
const outer = () => () => 1
const parent = () => {
  const child = () => 2
  return child
}
factory(() => () => 3)
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Functions) != 3 {
		t.Fatalf("functions = %#v", result.Functions)
	}
	want := [][]string{{"outer", "(anonymous)"}, {"parent", "child"}, {"cb:factory", "(anonymous)"}}
	for i, names := range want {
		if result.Functions[i].Name != names[0] || len(result.Functions[i].Nested) != 1 || result.Functions[i].Nested[0].Name != names[1] {
			t.Fatalf("functions[%d] = %#v, want %q with nested %q", i, result.Functions[i], names[0], names[1])
		}
	}
}

func TestAnalyzeQualifiesClassMembers(t *testing.T) {
	result, err := Analyze("classes.ts", []byte(`
class Declared {
  run() {}
  static build() {}
  get value() { return 1 }
  set value(next: number) {}
  task = () => () => {}
  static boot = () => {}
}
const Assigned = class {
  run() {}
}
const Alias = class Internal {
  run() {}
}
factory(class {
  run() {}
})
class Outer {
  method() {
    class Inner {
      run() {}
    }
    return () => {}
  }
}
`))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"Declared.run",
		"Declared.static build",
		"Declared.get value",
		"Declared.set value",
		"Declared.task",
		"Declared.static boot",
		"Assigned.run",
		"Alias.Internal.run",
		"(anonymous class).run",
		"Outer.method",
	}
	got := make([]string, len(result.Functions))
	for i, function := range result.Functions {
		got[i] = function.Name
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("functions = %#v, want %#v", got, want)
	}
	if got := result.Functions[4].Nested; len(got) != 1 || got[0].Name != "(anonymous)" {
		t.Fatalf("class arrow property nested functions = %#v", got)
	}
	if got := result.Functions[9].Nested; len(got) != 2 || got[0].Name != "Inner.run" || got[1].Name != "(anonymous)" {
		t.Fatalf("nested class functions = %#v", got)
	}
}

func TestAnalyzeQualifiesObjectLiteralMembers(t *testing.T) {
	result, err := Analyze("objects.ts", []byte(`
const owned = {
  run() {},
  arrow: () => {},
  functionValue: function() {},
  get value() { return 1 },
  set value(next: number) {},
  nested: { run() {} },
  explicit: function retained() {},
}
assigned.target = { run() {}, nested: { arrow: () => {} } }
factory({ run() {}, arrow: () => {} })
function outer() { return { run() {} } }
`))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"owned.run",
		"owned.arrow",
		"owned.functionValue",
		"owned.get value",
		"owned.set value",
		"owned.nested.run",
		"owned.explicit.retained",
		"assigned.target.run",
		"assigned.target.nested.arrow",
		"run",
		"arrow",
		"outer",
	}
	got := make([]string, len(result.Functions))
	for i, function := range result.Functions {
		got[i] = function.Name
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("functions = %#v, want %#v", got, want)
	}
	if got := result.Functions[11].Nested; len(got) != 1 || got[0].Name != "run" {
		t.Fatalf("returned object functions = %#v", got)
	}
}

func TestAnalyzeRecognizesJavaScriptLineTerminators(t *testing.T) {
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
			source := strings.Join([]string{
				"function f(x) {",
				"  // removed comment",
				"  if (x) return 1;",
				"  return 0;",
				"}",
			}, terminator.text)
			result, err := Analyze("lines.js", []byte(source))
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Functions) != 1 || result.Functions[0].Line != 1 || result.Functions[0].SLOC != 4 {
				t.Fatalf("functions = %#v", result.Functions)
			}
			if len(result.Comments) != 1 || result.Comments[0].StartLine != 2 || result.Comments[0].EndLine != 2 {
				t.Fatalf("comments = %#v", result.Comments)
			}
			wantTokenLines := map[string]int{"if": 3, "0": 4}
			for _, token := range result.Tokens {
				if want, ok := wantTokenLines[token.Text]; ok && token.Line != want {
					t.Fatalf("token = %#v, want line %d", token, want)
				}
			}
		})
	}
}

func TestClassOwnershipKeepsDiffOnChangedMethod(t *testing.T) {
	base := analyzeSnapshot(t, "base", `
class A {
  run() { if (ready) {} }
}
class B {
  run() { if (ready) {} if (waiting) {} }
}
`)
	head := analyzeSnapshot(t, "head", `
class A {
  run() { if (ready) {} if (waiting) {} }
}
class B {
  run() { if (ready) {} if (waiting) {} }
}
`)
	got, err := diff.Build(base, head, []string{"classes.ts"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Functions) != 1 || got.Functions[0].Name != "A.run" || got.Functions[0].Before.Line != 3 || got.Functions[0].After.Line != 3 {
		t.Fatalf("function deltas = %#v", got.Functions)
	}
}

func analyzeSnapshot(t *testing.T, rev, source string) model.Snapshot {
	t.Helper()
	result, err := Analyze("classes.ts", []byte(source))
	if err != nil {
		t.Fatal(err)
	}
	for i := range result.Functions {
		result.Functions[i].Bucket = model.Source
	}
	return model.Snapshot{
		Rev:       rev,
		Functions: result.Functions,
		Clones:    []model.ClonePair{},
		Buckets:   map[model.Bucket]model.Totals{model.Source: model.Summarize(result.Functions), model.Tests: {}},
	}
}

func TestAnalyzeTSXAndJavaScript(t *testing.T) {
	root := filepath.Join("..", "..", "..", "testdata", "typescript")
	for _, name := range []string{"component.tsx", "browser.js"} {
		source, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		result, err := Analyze(name, source)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Functions) != 1 || result.Functions[0].CC != 2 {
			t.Fatalf("%s functions = %#v", name, result.Functions)
		}
	}
}

func TestClassificationCoversTypeScriptDefaults(t *testing.T) {
	generated, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "typescript", "generated.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if got := model.Classify("src/x.spec.tsx", nil, model.Config{}); got.Bucket != model.Tests {
		t.Fatalf("spec bucket = %q", got.Bucket)
	}
	if got := model.Classify("src/__tests__/x.js", nil, model.Config{}); got.Bucket != model.Tests {
		t.Fatalf("__tests__ bucket = %q", got.Bucket)
	}
	if got := model.Classify("generated.ts", generated, model.Config{}); !got.Generated {
		t.Fatal("generated TypeScript was not classified")
	}
}

func TestAnalyzeNamespaceOwners(t *testing.T) {
	result, err := Analyze("namespaces.ts", []byte(`
namespace A {
 export function run() { function local() {} }
 export const arrow = () => 1;
 export const obj = { run() {} };
 export class Worker { run() {} }
}
namespace B { export function run() {} }
namespace A.Inner { export function run() {} }
namespace A { export function again() {} }
module Legacy { export function run() {} }
function outside() {}
`))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range result.Functions {
		names = append(names, f.Name)
	}
	want := []string{"A.run", "A.arrow", "A.obj.run", "A.Worker.run", "B.run", "A.Inner.run", "A.again", "Legacy.run", "outside"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("names = %v, want %v", names, want)
	}
	if got := result.Functions[0].Nested; len(got) != 1 || got[0].Name != "local" {
		t.Fatalf("nested = %#v", got)
	}
}

func TestHashbangExcludedFromSourceLines(t *testing.T) {
	for _, suffix := range []string{"ts", "tsx", "mts", "cts", "js", "jsx", "mjs", "cjs"} {
		for _, terminator := range []string{"\n", "\r\n", "\r", "\u2028", "\u2029"} {
			source := []byte("#!/usr/bin/env node" + terminator + "function f() {}")
			result, err := Analyze("executable."+suffix, source)
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Comments) != 1 || result.Comments[0].StartByte != 0 || result.Comments[0].EndByte != len("#!/usr/bin/env node") {
				t.Fatalf("%s %q comments = %#v", suffix, terminator, result.Comments)
			}
			lines := lang.JavaScriptSourceLines(source, result.Comments, result.TestSpans)
			if !reflect.DeepEqual(lines[model.Source], []int{2}) {
				t.Fatalf("source lines = %v", lines)
			}
		}
	}
}

func TestECMAScriptWhitespaceLines(t *testing.T) {
	for _, whitespace := range []string{"\u00a0", "\u1680", "\u2000", "\u202f", "\u205f", "\u3000", "\ufeff"} {
		for _, suffix := range []string{"ts", "tsx", "js", "jsx", "mts", "cts", "mjs", "cjs"} {
			source := []byte("function f() {\n" + whitespace + "\nreturn 1;\n}")
			result, err := Analyze("space."+suffix, source)
			if err != nil {
				t.Fatal(err)
			}
			lines := lang.JavaScriptSourceLines(source, result.Comments, result.TestSpans)
			if result.Functions[0].SLOC != 3 || !reflect.DeepEqual(lines[model.Source], []int{1, 3, 4}) {
				t.Fatalf("%s/%q function=%#v lines=%v", suffix, whitespace, result.Functions, lines)
			}
		}
	}
	source := []byte("function f() { return `\n\u0085\n`; }")
	result, err := Analyze("literal.js", source)
	if err != nil {
		t.Fatal(err)
	}
	if result.Functions[0].SLOC != 3 || len(lang.JavaScriptSourceLines(source, result.Comments, result.TestSpans)[model.Source]) != 3 {
		t.Fatal("non-ECMAScript whitespace inside literal lost its source line")
	}
}
