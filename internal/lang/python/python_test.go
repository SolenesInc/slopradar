package python

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
