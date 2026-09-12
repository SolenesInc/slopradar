package typescript

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
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
