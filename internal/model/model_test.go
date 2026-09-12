package model

import (
	"math"
	"os"
	"testing"
)

func TestSummarizeUsesOnlyTopLevelFunctions(t *testing.T) {
	functions := []Function{
		NewFunction("a.go", "a", 1, 11, 16, []Function{NewFunction("a.go", "nested", 2, 20, 9, nil)}),
		NewFunction("b.go", "b", 1, 1, 9, nil),
	}
	got := Summarize(functions)
	if got.Functions != 2 || got.Mass != 47 || got.MassOverCC10 != 44 {
		t.Fatalf("totals = %#v", got)
	}
	if math.Abs(got.Erosion-44.0/47.0) > 1e-12 {
		t.Fatalf("erosion = %v", got.Erosion)
	}
}

func TestGeneratedMarkersMustAppearInComments(t *testing.T) {
	content, err := os.ReadFile("classify.go")
	if err != nil {
		t.Fatal(err)
	}
	if Classify("internal/model/classify.go", content, Config{}).Generated {
		t.Fatal("generated marker implementation classified itself as generated")
	}
	for _, source := range []string{`const marker = "@generated"`, `marker := "linguist-generated"`, `[]byte("Code generated x DO NOT EDIT.")`} {
		if Classify("source.go", []byte(source), Config{}).Generated {
			t.Fatalf("string literal classified as generated: %s", source)
		}
	}
	for _, source := range []string{"// @generated", "# linguist-generated", "// Code generated fixture. DO NOT EDIT."} {
		if !Classify("source.go", []byte(source), Config{}).Generated {
			t.Fatalf("comment marker not classified as generated: %s", source)
		}
	}
}
