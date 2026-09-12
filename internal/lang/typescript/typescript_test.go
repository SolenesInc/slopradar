package typescript

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

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
