package rust

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_rust "github.com/tree-sitter/tree-sitter-rust/bindings/go"

	"github.com/SolenesInc/slopradar/internal/lang"
	"github.com/SolenesInc/slopradar/internal/model"
)

func TestAnalyzeGolden(t *testing.T) {
	root := filepath.Join("..", "..", "..", "testdata", "rust")
	source, err := os.ReadFile(filepath.Join(root, "functions.rs"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := Analyze("testdata/rust/functions.rs", source)
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
	wantBuckets := []model.Bucket{model.Source, model.Source, model.Tests}
	if !reflect.DeepEqual(result.FunctionBuckets, wantBuckets) {
		t.Fatalf("function buckets = %#v, want %#v", result.FunctionBuckets, wantBuckets)
	}
	foundTestToken := false
	for _, token := range result.Tokens {
		if token.Text == "test_only" && token.Bucket == model.Tests {
			foundTestToken = true
		}
	}
	if !foundTestToken {
		t.Fatal("test module tokens were not assigned to the tests bucket")
	}
}

func TestClassificationCoversRustDefaults(t *testing.T) {
	if got := model.Classify("tests/integration.rs", nil, model.Config{}); got.Bucket != model.Tests {
		t.Fatalf("integration test bucket = %q", got.Bucket)
	}
	generated, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "rust", "generated.rs"))
	if err != nil {
		t.Fatal(err)
	}
	if got := model.Classify("generated.rs", generated, model.Config{}); !got.Generated {
		t.Fatal("generated Rust was not classified")
	}
}

func TestDirectTestAttributesClassifyFunctionsTokensAndLines(t *testing.T) {
	source := []byte(`#[doc = "cfg(test)"]
fn production() {}

#[test]
// attached test comment
fn direct_test() {}

#[cfg(test)]
/* attached configured test comment */
fn configured_test() {}
`)
	result, err := Analyze("fixture.rs", source)
	if err != nil {
		t.Fatal(err)
	}
	wantBuckets := []model.Bucket{model.Source, model.Tests, model.Tests}
	if !reflect.DeepEqual(result.FunctionBuckets, wantBuckets) {
		t.Fatalf("function buckets = %#v, want %#v", result.FunctionBuckets, wantBuckets)
	}
	wantTokenBuckets := map[string]model.Bucket{"production": model.Source, "direct_test": model.Tests, "configured_test": model.Tests}
	for _, token := range result.Tokens {
		if want, ok := wantTokenBuckets[token.Text]; ok && token.Bucket != want {
			t.Fatalf("token = %#v, want bucket %q", token, want)
		}
	}
	lines := lang.SourceLines(source, result.Comments, result.TestSpans)
	if want := []int{1, 2}; !reflect.DeepEqual(lines[model.Source], want) {
		t.Fatalf("source lines = %#v, want %#v", lines[model.Source], want)
	}
	if want := []int{4, 6, 8, 10}; !reflect.DeepEqual(lines[model.Tests], want) {
		t.Fatalf("test lines = %#v, want %#v", lines[model.Tests], want)
	}
}

func TestConfiguredItemsClassifyTheirFunctionsAndHelpersAsTests(t *testing.T) {
	source := []byte(`fn production() {}

#[cfg(test)]
impl Widget {
    fn helper() {
        let nested = || {
            if true {}
        };
    }
}
#[cfg(test)]
const FACTORY: fn() = || {};
`)
	result, err := Analyze("fixture.rs", source)
	if err != nil {
		t.Fatal(err)
	}
	wantBuckets := []model.Bucket{model.Source, model.Tests, model.Tests}
	if !reflect.DeepEqual(result.FunctionBuckets, wantBuckets) {
		t.Fatalf("function buckets = %#v, want %#v", result.FunctionBuckets, wantBuckets)
	}
	if len(result.Functions) != 3 || len(result.Functions[1].Nested) != 1 || result.Functions[1].Nested[0].Name != "nested" {
		t.Fatalf("functions = %#v", result.Functions)
	}
	if result.Functions[1].CC != 2 || result.Functions[1].Nested[0].CC != 2 {
		t.Fatalf("folded helper metrics = %#v", result.Functions[1])
	}
	for _, token := range result.Tokens {
		if (token.Text == "helper" || token.Text == "nested" || token.Text == "FACTORY") && token.Bucket != model.Tests {
			t.Fatalf("token = %#v, want tests bucket", token)
		}
	}
}

func TestTraitAndExternDeclarationsAreNotFunctions(t *testing.T) {
	source := []byte(`trait Service {
    fn required(&self);
    fn defaulted(&self) {
        if true {}
    }
}

struct Worker;

impl Service for Worker {
    fn required(&self) {}
}

unsafe extern "C" {
    fn ffi_required();
}

extern "C" fn exported() {}
`)
	result, err := Analyze("fixture.rs", source)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("warnings = %#v", result.Warnings)
	}
	want := []string{"defaulted", "Worker::required", "exported"}
	got := make([]string, len(result.Functions))
	for i, function := range result.Functions {
		got[i] = function.Name
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("functions = %#v, want %#v", got, want)
	}

	parser := sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(sitter.NewLanguage(tree_sitter_rust.Language())); err != nil {
		t.Fatal(err)
	}
	tree := parser.Parse(source, nil)
	if tree == nil {
		t.Fatal("parser returned no tree")
	}
	defer tree.Close()
	kinds := map[string]int{}
	collectNamedKinds(tree.RootNode(), kinds)
	if kinds["function_item"] != 3 || kinds["function_signature_item"] != 2 {
		t.Fatalf("function node kinds = %#v", kinds)
	}
}

func collectNamedKinds(node *sitter.Node, kinds map[string]int) {
	kinds[node.Kind()]++
	for i := uint(0); i < node.NamedChildCount(); i++ {
		collectNamedKinds(node.NamedChild(i), kinds)
	}
}
