package python

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
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
