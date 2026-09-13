package golang

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
	root := filepath.Join("..", "..", "..", "testdata", "go")
	path := filepath.Join(root, "functions.go")
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	result, err := Analyze("testdata/go/functions.go", source)
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
	for _, token := range result.Tokens {
		if token.Text == "// comment-only line" {
			t.Fatal("comment leaked into token stream")
		}
	}
}

func TestClassificationCoversGoDefaultsAndConfiguration(t *testing.T) {
	generated, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "go", "generated.go"))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		path    string
		content []byte
		config  model.Config
		want    model.Classification
	}{
		{path: "x.go", want: model.Classification{Bucket: model.Source}},
		{path: "x_test.go", want: model.Classification{Bucket: model.Tests}},
		{path: "vendor/x.go", want: model.Classification{Excluded: true}},
		{path: "x.go", content: generated, want: model.Classification{Bucket: model.Source, Generated: true}},
		{path: "bench/x.go", config: model.Config{TestGlobs: []string{"bench/"}}, want: model.Classification{Bucket: model.Tests}},
		{path: "private/x.go", config: model.Config{Excludes: []string{"private/"}}, want: model.Classification{Excluded: true}},
	}
	for _, test := range cases {
		if got := model.Classify(test.path, test.content, test.config); got != test.want {
			t.Errorf("Classify(%q) = %#v, want %#v", test.path, got, test.want)
		}
	}
}

func TestConfigRejectsUnknownAndTrailingContent(t *testing.T) {
	for _, data := range []string{`{"unknown": []}`, `{} {}`} {
		if _, err := model.ParseConfig([]byte(data)); err == nil {
			t.Fatalf("ParseConfig(%q) succeeded", data)
		}
	}
}

func TestTypeSwitchCountsClausesAndNestedDecisions(t *testing.T) {
	source := []byte(`package fixture
func classify(value any) int {
    switch item := value.(type) {
    case int, string:
        switch any(item).(type) {
        case int:
            return 1
        default:
            return 2
        }
    case bool:
        return 3
	default:
		return 4
	}
}
`)
	result, err := Analyze("fixture.go", source)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Functions) != 1 || result.Functions[0].CC != 4 {
		t.Fatalf("base plus three non-default clauses: %#v", result.Functions)
	}
}

func TestFunctionLiteralNamesFollowStructuralOwners(t *testing.T) {
	source := []byte(`package fixture
var first, second = func() {}, func() {}

func outer() {
    left, right := func() {}, func() {}
    assigned, assignedField := func() {}, func() {}
    _ = left
    _ = right
    _ = assigned
    _ = assignedField
    deeplyWrapped := (((((func() {})))))
    _ = deeplyWrapped
    register((((((func() {}))))))
    immediate := (func() {})()
    _ = immediate
	nested := func() func() { return func() {} }
	_ = nested
}
`)
	result, err := Analyze("fixture.go", source)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	var appendNested func([]model.Function)
	appendNested = func(functions []model.Function) {
		for _, function := range functions {
			names = append(names, function.Name)
			appendNested(function.Nested)
		}
	}
	for _, function := range result.Functions {
		if function.Name == "outer" {
			appendNested(function.Nested)
			continue
		}
		names = append(names, function.Name)
	}
	want := []string{"first", "second", "left", "right", "assigned", "assignedField", "deeplyWrapped", "cb:register", "immediate", "nested", "(anonymous)"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("names = %#v, want %#v", names, want)
	}
}

func TestGo127GeneralizedNewAndGenericMethods(t *testing.T) {
	source := []byte(`package fixture
type Pair[A, B any] struct{}
var answer = new(42)

func (*Pair[A, B]) Map[C any](value C) C {
    nested := func(flag bool) C {
        if flag && true {
            return value
        }
        return value
    }
    return nested(new(true) != nil)
}
`)
	result, err := Analyze("fixture.go", source)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("warnings = %#v", result.Warnings)
	}
	if len(result.Functions) != 1 || result.Functions[0].Name != "Pair.Map" || result.Functions[0].CC != 3 {
		t.Fatalf("generic method = %#v", result.Functions)
	}
	nested := result.Functions[0].Nested
	if len(nested) != 1 || nested[0].Name != "nested" || nested[0].CC != 3 {
		t.Fatalf("nested function = %#v", nested)
	}
}

func TestPhysicalCoordinatesAndExactLexicalTokens(t *testing.T) {
	source := []byte("package fixture\r\n//line generated.go:700\r\nfunc physical() {\r\nraw := `a\r\nb`\r\nmessage := \"left\\nright\"\r\n_ = raw; // trailing\r\n_ = message\r\n}\r\n")
	result, err := Analyze("physical.go", source)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Functions) != 1 || result.Functions[0].Line != 3 {
		t.Fatalf("function coordinates = %#v", result.Functions)
	}
	if len(result.Comments) != 2 || result.Comments[0].StartLine != 2 || result.Comments[1].StartLine != 7 {
		t.Fatalf("comment coordinates = %#v", result.Comments)
	}
	texts := []string{}
	lines := map[string]int{}
	for _, item := range result.Tokens {
		texts = append(texts, item.Text)
		lines[item.Text] = item.Line
	}
	if lines["func"] != 3 || lines["a\r\nb"] != 4 {
		t.Fatalf("token coordinates = %#v", result.Tokens)
	}
	if strings.Count(strings.Join(texts, "\x00"), ";") != 1 {
		t.Fatalf("implicit semicolons leaked or explicit semicolon disappeared: %#v", texts)
	}
	if !reflect.DeepEqual(stringTokens(texts), []string{"`", "a\r\nb", "`", "\"", "left", "\\n", "right", "\""}) {
		t.Fatalf("string tokens changed source bytes: %#v", stringTokens(texts))
	}
}

func TestAnalyzeRejectsRecoveredSyntax(t *testing.T) {
	result, err := Analyze("broken.go", []byte("package fixture\nfunc broken("))
	if err == nil || !strings.Contains(err.Error(), "parse broken.go: invalid Go syntax") {
		t.Fatalf("result = %#v, error = %v", result, err)
	}
}

func TestAnalyzePreservesArbitraryFilenameBytes(t *testing.T) {
	file := string([]byte{'f', 'i', 'x', 't', 'u', 'r', 'e', 0xff, '.', 'g', 'o'})
	result, err := Analyze(file, []byte("package fixture\nfunc kept() {}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Functions) != 1 || result.Functions[0].File != file {
		t.Fatalf("functions = %#v", result.Functions)
	}
}

func stringTokens(values []string) []string {
	result := []string{}
	for _, value := range values {
		if value == "`" || value == "a\r\nb" || value == "\"" || value == "left" || value == "\\n" || value == "right" {
			result = append(result, value)
		}
	}
	return result
}
