package golang

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
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
