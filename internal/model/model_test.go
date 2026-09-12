package model

import (
	"math"
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
