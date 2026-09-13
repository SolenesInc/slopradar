package model

import (
	"math"
	"os"
	"strings"
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

func TestParseConfigRejectsMalformedPatterns(t *testing.T) {
	for _, test := range []struct {
		data  string
		field string
	}{
		{data: `{"excludes":["["]}`, field: "excludes"},
		{data: `{"test_globs":["valid/**","["]}`, field: "test_globs"},
	} {
		_, err := ParseConfig([]byte(test.data))
		if err == nil || !strings.Contains(err.Error(), test.field) || !strings.Contains(err.Error(), `pattern "["`) {
			t.Fatalf("ParseConfig(%s) error = %v", test.data, err)
		}
	}
}

func TestClassifyPreservesUnixBackslashesAndGlobEscapes(t *testing.T) {
	file := `vendor\main.go`
	if got := Classify(file, nil, Config{}); got.Excluded {
		t.Fatalf("literal backslash path classified as vendor directory: %#v", got)
	}
	if got := Classify(file, nil, Config{Excludes: []string{"vendor/*.go"}}); got.Excluded {
		t.Fatalf("slash glob matched literal backslash path: %#v", got)
	}
	config, err := ParseConfig([]byte(`{"excludes":["vendor\\\\main.go"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := Classify(file, nil, config); !got.Excluded {
		t.Fatalf("escaped literal backslash glob did not match: %#v", got)
	}
}

func TestClassifyLimitsBuiltInTestdataExclusionToGo(t *testing.T) {
	for _, file := range []string{"testdata/helper.go", "src/testdata/helper.GO"} {
		if got := Classify(file, nil, Config{}); !got.Excluded {
			t.Errorf("Go file %q was not excluded: %#v", file, got)
		}
	}
	for _, file := range []string{"testdata/helper.ts", "testdata/helper.py", "testdata/helper.rs"} {
		if got := Classify(file, nil, Config{}); got.Excluded {
			t.Errorf("non-Go file %q was excluded: %#v", file, got)
		}
	}
	if got := Classify("testdata/helper.ts", nil, Config{Excludes: []string{"testdata/"}}); !got.Excluded {
		t.Fatalf("explicit testdata exclusion did not apply: %#v", got)
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
	for _, source := range []string{
		`const marker = "@generated"`,
		`marker := "linguist-generated"`,
		`[]byte("Code generated x DO NOT EDIT.")`,
		"const fixture = `\n// @generated\n`",
		"/* ordinary header */ const fixture = `\n/* @generated */\n`",
		"package fixture\n// @generated inside handwritten source",
	} {
		if Classify("source.go", []byte(source), Config{}).Generated {
			t.Fatalf("string literal classified as generated: %s", source)
		}
	}
	for _, source := range []string{
		"// @generated",
		"// Code generated fixture. DO NOT EDIT.",
		"\ufeff\n// ordinary header\n\n/*\n@generated\n*/\npackage fixture",
		"/* ordinary header */ /* @generated */ package fixture",
	} {
		if !Classify("source.go", []byte(source), Config{}).Generated {
			t.Fatalf("comment marker not classified as generated: %s", source)
		}
	}
	if !Classify("generated.py", []byte("#!/usr/bin/python3\n# linguist-generated"), Config{}).Generated {
		t.Fatal("Python header comment marker was missed")
	}
	if !Classify("generated.rs", []byte("/* header /* nested */\n * @generated\n */ fn generated() {}"), Config{}).Generated {
		t.Fatal("nested Rust header comment marker was missed")
	}
	for _, source := range []string{"#[doc = \"@generated\"]\nfn handwritten() {}", "#![doc = \"@generated\"]\nfn handwritten() {}"} {
		if Classify("handwritten.rs", []byte(source), Config{}).Generated {
			t.Fatalf("Rust attribute literal classified as generated: %s", source)
		}
	}
}
