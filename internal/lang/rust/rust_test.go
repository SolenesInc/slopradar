package rust

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

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
