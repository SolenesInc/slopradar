package rust

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

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

func TestDirectTestAttributesClassifyFunctionsAndTokens(t *testing.T) {
	source := []byte(`#[test]
fn direct_test() {}

#[cfg(test)]
fn configured_test() {}

fn production() {}
`)
	result, err := Analyze("fixture.rs", source)
	if err != nil {
		t.Fatal(err)
	}
	wantBuckets := []model.Bucket{model.Tests, model.Tests, model.Source}
	if !reflect.DeepEqual(result.FunctionBuckets, wantBuckets) {
		t.Fatalf("function buckets = %#v, want %#v", result.FunctionBuckets, wantBuckets)
	}
	for _, token := range result.Tokens {
		if (token.Text == "direct_test" || token.Text == "configured_test") && token.Bucket != model.Tests {
			t.Fatalf("test token = %#v", token)
		}
	}
}
