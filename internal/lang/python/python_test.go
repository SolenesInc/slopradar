package python

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/SolenesInc/slopradar/internal/clones"
	"github.com/SolenesInc/slopradar/internal/lang"
	"github.com/SolenesInc/slopradar/internal/model"
)

func TestAnalyzeGolden(t *testing.T) {
	root := filepath.Join("..", "..", "..", "testdata", "python")
	source, err := os.ReadFile(filepath.Join(root, "functions.py"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := Analyze("testdata/python/functions.py", source)
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
	if len(result.Comments) != 2 {
		t.Fatalf("comments = %#v", result.Comments)
	}
}

func TestClassificationCoversPythonDefaults(t *testing.T) {
	for _, file := range []string{"test_worker.py", "worker_test.py", "tests/worker.py"} {
		if got := model.Classify(file, nil, model.Config{}); got.Bucket != model.Tests {
			t.Errorf("Classify(%q) bucket = %q", file, got.Bucket)
		}
	}
	generated, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "python", "generated.py"))
	if err != nil {
		t.Fatal(err)
	}
	if got := model.Classify("generated.py", generated, model.Config{}); !got.Generated {
		t.Fatal("generated Python was not classified")
	}
}

func TestDocstringsRequireCapableSuiteAndPlainString(t *testing.T) {
	source := []byte(`"""module doc"""
if True:
    "ordinary block string"

def run():
    """function doc"""
    if True:
        f"executed {side_effect()}"

class Worker:
    r"""class doc"""
    def method(self):
        b"bytes are not docstrings"
        f"formatted {side_effect()}"
`)
	result, err := Analyze("fixture.py", source)
	if err != nil {
		t.Fatal(err)
	}
	lines := make([]int, len(result.Comments))
	for i, span := range result.Comments {
		lines[i] = span.StartLine
	}
	if want := []int{1, 6, 11}; !reflect.DeepEqual(lines, want) {
		t.Fatalf("comment lines = %#v, want docstrings %#v", lines, want)
	}
	foundSideEffect := false
	foundBytes := false
	for _, token := range result.Tokens {
		foundSideEffect = foundSideEffect || token.Text == "side_effect"
		foundBytes = foundBytes || strings.HasPrefix(strings.ToLower(token.Text), "b\"")
	}
	if !foundSideEffect || !foundBytes {
		t.Fatalf("ordinary string tokens were removed: %#v", result.Tokens)
	}
}

func TestLambdaNamesFollowStructuralOwners(t *testing.T) {
	source := []byte(`first, second = lambda: 1, lambda: 2
deeply_wrapped = (((((((lambda: 3)))))))
register(((((((lambda: 4)))))))
outer = lambda: (lambda: 5)
`)
	result, err := Analyze("fixture.py", source)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(result.Functions)+1)
	for _, function := range result.Functions {
		names = append(names, function.Name)
		for _, nested := range function.Nested {
			names = append(names, nested.Name)
		}
	}
	want := []string{"first", "second", "deeply_wrapped", "cb:register", "outer", "(anonymous)"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("names = %#v, want %#v", names, want)
	}
}

func TestCompleteClassPathsStopAtFunctionBoundaries(t *testing.T) {
	result, err := Analyze("owners.py", []byte("class OuterA:\n class Inner:\n  def run(self): pass\nclass OuterB:\n class Inner:\n  def run(self): pass\ndef factory():\n class Local:\n  def run(self): pass\n def helper(): pass\n"))
	if err != nil || len(result.Warnings) != 0 {
		t.Fatalf("analyze: %v; warnings %v", err, result.Warnings)
	}
	var names []string
	for _, f := range result.Functions {
		names = append(names, f.Name)
		for _, nested := range f.Nested {
			names = append(names, nested.Name)
		}
	}
	if !reflect.DeepEqual(names, []string{"OuterA.Inner.run", "OuterB.Inner.run", "factory", "Local.run", "helper"}) {
		t.Fatalf("names = %#v", names)
	}
}

func TestSuiteStructurePreventsFalseExactClone(t *testing.T) {
	first := pythonCloneFixture("        ", "    ", 3)
	second := pythonCloneFixture("        ", "    ", 2)
	files := []clones.File{
		pythonCloneFile(t, "first.py", first),
		pythonCloneFile(t, "second.py", second),
	}
	if result := clones.Detect(files); len(result.Pairs) != 0 {
		t.Fatalf("structurally different suites produced exact clones: %#v", result.Pairs)
	}
}

func TestEquivalentTabsAndSpacesRemainExactClones(t *testing.T) {
	spaces := pythonCloneFixture("        ", "    ", 3)
	tabs := pythonCloneFixture("\t\t", "\t", 3)
	files := []clones.File{
		pythonCloneFile(t, "spaces.py", spaces),
		pythonCloneFile(t, "tabs.py", tabs),
	}
	result := clones.Detect(files)
	if len(result.Pairs) != 1 {
		t.Fatalf("equivalent suite structure clone pairs = %#v", result.Pairs)
	}
	if result.Pairs[0].Tokens < clones.JscpdDefaultMinimumTokens || result.Pairs[0].Lines < clones.JscpdDefaultMinimumLines {
		t.Fatalf("clone = %#v, want production thresholds", result.Pairs[0])
	}
	wantA := cloneRange(files[0])
	wantB := cloneRange(files[1])
	if result.Pairs[0].A != wantA || result.Pairs[0].B != wantB {
		t.Fatalf("clone ranges = %#v, %#v; want %#v, %#v", result.Pairs[0].A, result.Pairs[0].B, wantA, wantB)
	}
}

func TestSuiteTokensPreserveInlineContinuationsStringsAndCoordinates(t *testing.T) {
	inline := []byte("if ready: work()\n")
	multiline := []byte("if ready:\n    work()\n")
	inlineResult, err := Analyze("inline.py", inline)
	if err != nil {
		t.Fatal(err)
	}
	multilineResult, err := Analyze("multiline.py", multiline)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(tokenTexts(inlineResult.Tokens), tokenTexts(multilineResult.Tokens)) {
		t.Fatalf("inline tokens = %#v, multiline tokens = %#v", inlineResult.Tokens, multilineResult.Tokens)
	}
	if got := boundaryLines(inlineResult.Tokens); !reflect.DeepEqual(got, []int{1, 1}) {
		t.Fatalf("inline boundary lines = %#v", got)
	}
	if got := boundaryLines(multilineResult.Tokens); !reflect.DeepEqual(got, []int{2, 2}) {
		t.Fatalf("multiline boundary lines = %#v", got)
	}

	source := []byte(`"""module doc"""
def run():
    """function doc"""
    # omitted
    value = (
        " "
        "  "
    )
    return value
`)
	result, err := Analyze("details.py", source)
	if err != nil {
		t.Fatal(err)
	}
	texts := tokenTexts(result.Tokens)
	if strings.Contains(strings.Join(texts, "\x00"), "doc") {
		t.Fatalf("docstring appeared in tokens: %#v", result.Tokens)
	}
	if strings.Contains(strings.Join(texts, "\x00"), "omitted") {
		t.Fatalf("comment appeared in tokens: %#v", result.Tokens)
	}
	if !containsToken(texts, " ") || !containsToken(texts, "  ") {
		t.Fatalf("meaningful string whitespace changed: %#v", result.Tokens)
	}
	if got := boundaryLines(result.Tokens); !reflect.DeepEqual(got, []int{5, 9}) {
		t.Fatalf("retained suite boundary lines = %#v", got)
	}
	for _, token := range result.Tokens {
		if token.Text == "  " && token.Line != 7 {
			t.Fatalf("continuation token = %#v, want physical line 7", token)
		}
	}
	docstringOnly, err := Analyze("docstring-only.py", []byte("def documented(): \"\"\"only\"\"\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range docstringOnly.Tokens {
		if strings.HasPrefix(token.Text, "\x00") {
			t.Fatalf("stripped suite left a structural token: %#v", docstringOnly.Tokens)
		}
	}
}

func pythonCloneFixture(nestedIndent, outerIndent string, nestedCalls int) []byte {
	var source strings.Builder
	source.WriteString("def process():\n")
	source.WriteString(outerIndent + "if ready:\n")
	for index, name := range []string{"alpha", "bravo", "charlie", "delta", "echo", "foxtrot", "golf", "hotel", "india", "juliet", "kilo", "lima", "mike", "november"} {
		indent := outerIndent
		if index < nestedCalls {
			indent = nestedIndent
		}
		source.WriteString(indent + name + "()\n")
	}
	return []byte(source.String())
}

func cloneRange(file clones.File) model.Range {
	lines := file.SourceLines[model.Source]
	return model.Range{File: file.Path, Start: lines[0], End: lines[len(lines)-1]}
}

func pythonCloneFile(t *testing.T, path string, source []byte) clones.File {
	t.Helper()
	result, err := Analyze(path, source)
	if err != nil {
		t.Fatal(err)
	}
	lines := lang.SourceLines(source, result.Comments, result.TestSpans)
	if len(result.Tokens) < clones.JscpdDefaultMinimumTokens || len(lines[model.Source]) < clones.JscpdDefaultMinimumLines {
		t.Fatalf("fixture has %d tokens across %d lines, want at least %d tokens across %d lines", len(result.Tokens), len(lines[model.Source]), clones.JscpdDefaultMinimumTokens, clones.JscpdDefaultMinimumLines)
	}
	return clones.File{Path: path, Language: "python", Tokens: result.Tokens, SourceLines: lines}
}

func tokenTexts(tokens []lang.Token) []string {
	texts := make([]string, len(tokens))
	for index, token := range tokens {
		texts[index] = token.Text
	}
	return texts
}

func containsToken(tokens []string, want string) bool {
	for _, token := range tokens {
		if token == want {
			return true
		}
	}
	return false
}

func boundaryLines(tokens []lang.Token) []int {
	lines := []int{}
	for _, token := range tokens {
		if strings.HasPrefix(token.Text, "\x00") {
			lines = append(lines, token.Line)
		}
	}
	return lines
}

func TestCallbackNamesRetainAssignments(t *testing.T) {
	result, err := Analyze("owners.py", []byte("first = wrap(lambda: 1)\nsecond = wrap(lambda: 2)\nnested = outer(inner(lambda: 3))\nparent = lambda: wrap(lambda: 4)\n"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range result.Functions {
		names = append(names, f.Name)
	}
	if !reflect.DeepEqual(names, []string{"first.cb:wrap", "second.cb:wrap", "nested.cb:outer.cb:inner", "parent"}) {
		t.Fatalf("names = %q", names)
	}
	if result.Functions[3].Nested[0].Name != "cb:wrap" {
		t.Fatalf("nested = %#v", result.Functions[3])
	}
}

func TestClassLambdaOwners(t *testing.T) {
	result, err := Analyze("classes.py", []byte("class A:\n callback = wrap(lambda: 1)\nclass B:\n callback = wrap(lambda: 2)\n def method(self):\n  local = wrap(lambda: 3)\n"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range result.Functions {
		names = append(names, f.Name)
	}
	if !reflect.DeepEqual(names, []string{"A.callback.cb:wrap", "B.callback.cb:wrap", "B.method"}) {
		t.Fatalf("names = %q", names)
	}
	if result.Functions[2].Nested[0].Name != "local.cb:wrap" {
		t.Fatalf("nested = %#v", result.Functions[2])
	}
}

func TestCollectionLambdaOwners(t *testing.T) {
	source := []byte("handlers = {\"a\": lambda: 1, \"b\": lambda: 2}\nitems = [lambda: 1, (lambda: 2, lambda: 3)]\nfirst, second = (lambda: 1, lambda: 2)\n")
	result, err := Analyze("collections.py", source)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range result.Functions {
		names = append(names, f.Name)
	}
	want := []string{`handlers["a"]`, `handlers["b"]`, "items[0]", "items[1][0]", "items[1][1]", "first", "second"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("names = %q, want %q", names, want)
	}
}

func TestCollectionCommentsAndCallbackArguments(t *testing.T) {
	result, err := Analyze("collections.py", []byte("items = [lambda: 1, # ignored\n lambda: 2]\nowned = combine(lambda: 1, # ignored\n lambda: 2)\n"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range result.Functions {
		names = append(names, f.Name)
	}
	want := []string{"items[0]", "items[1]", "owned.cb:combine[0]", "owned.cb:combine[1]"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("names = %q, want %q", names, want)
	}
}

func TestClassOwnerStopsAtLambdaBoundary(t *testing.T) {
	result, err := Analyze("classes.py", []byte("class A:\n outer = lambda: (lambda: 1, lambda: 2)\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Functions) != 1 || result.Functions[0].Name != "A.outer" || len(result.Functions[0].Nested) != 2 {
		t.Fatalf("functions = %#v", result.Functions)
	}
	if result.Functions[0].Nested[0].Name != "[0]" || result.Functions[0].Nested[1].Name != "[1]" {
		t.Fatalf("nested = %#v", result.Functions[0].Nested)
	}
}

func TestDocstringsIgnorePrecedingComments(t *testing.T) {
	source := []byte("#!/usr/bin/python3\n# module comment\n\"module doc\"\nclass C:\n    # class comment\n    \"class doc\"\n    def f(self):\n        # function comment\n        \"function doc\"\n        return 1\n")
	result, err := Analyze("comments.py", source)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Warnings) != 0 || len(result.Functions) != 1 || result.Functions[0].SLOC != 2 {
		t.Fatalf("functions=%+v warnings=%v", result.Functions, result.Warnings)
	}
	for _, token := range result.Tokens {
		if strings.Contains(token.Text, "doc") || strings.Contains(token.Text, "comment") {
			t.Fatalf("documentation token retained: %+v", token)
		}
	}
	if got := lang.CountLines(source, result.Comments, result.TestSpans)[model.Source]; got != 3 {
		t.Fatalf("source lines=%d, want class, function, return", got)
	}
}
