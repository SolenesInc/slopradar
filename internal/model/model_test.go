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

func TestExcludedDirectoryUsesOnlyStructuralRules(t *testing.T) {
	if ExcludedDirectory("src", []string{"src/f*"}) {
		t.Fatal("file glob excluded a directory with possible included descendants")
	}
	for _, test := range []struct {
		directory string
		config    Config
	}{
		{directory: "src/node_modules"},
		{directory: "src/.generated"},
		{directory: "src/nested", config: Config{Excludes: []string{"src/"}}},
	} {
		if !ExcludedDirectory(test.directory, test.config.Excludes) {
			t.Errorf("directory %q was not excluded", test.directory)
		}
	}
	if ExcludedDirectory("testdata", nil) {
		t.Fatal("testdata directory excluded before descendant languages are known")
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

func TestClassifyScopesTestDirectoriesByLanguage(t *testing.T) {
	tests := []struct {
		file string
		want Bucket
	}{
		{file: "tests/helper.go", want: Source},
		{file: "__tests__/helper.go", want: Source},
		{file: "tests/helper.ts", want: Source},
		{file: "__tests__/helper.ts", want: Tests},
		{file: "__tests__/helper.tsx", want: Tests},
		{file: "__tests__/helper.mts", want: Tests},
		{file: "__tests__/helper.cts", want: Tests},
		{file: "tests/helper.js", want: Source},
		{file: "__tests__/helper.js", want: Tests},
		{file: "__tests__/helper.jsx", want: Tests},
		{file: "__tests__/helper.mjs", want: Tests},
		{file: "__tests__/helper.cjs", want: Tests},
		{file: "tests/helper.py", want: Tests},
		{file: "__tests__/helper.py", want: Source},
		{file: "tests/helper.rs", want: Tests},
		{file: "__tests__/helper.rs", want: Source},
	}
	for _, test := range tests {
		if got := Classify(test.file, nil, Config{}).Bucket; got != test.want {
			t.Errorf("Classify(%q) bucket = %q, want %q", test.file, got, test.want)
		}
	}
}

func TestConfiguredTestGlobsApplyToEveryLanguage(t *testing.T) {
	config := Config{TestGlobs: []string{"verification/"}}
	for _, file := range []string{"verification/helper.go", "verification/helper.ts", "verification/helper.js", "verification/helper.py", "verification/helper.rs"} {
		if got := Classify(file, nil, config).Bucket; got != Tests {
			t.Errorf("Classify(%q) bucket = %q, want %q", file, got, Tests)
		}
	}
}

func TestJavaScriptGeneratedHeadersRespectAllTerminators(t *testing.T) {
	for _, extension := range []string{".ts", ".tsx", ".mts", ".cts", ".js", ".jsx", ".mjs", ".cjs"} {
		for _, terminator := range []string{"\n", "\r\n", "\r", "\u2028", "\u2029"} {
			for _, header := range []string{"// ordinary header", "#!/usr/bin/env node"} {
				file := "handwritten" + extension
				source := header + terminator + `const marker = "@generated";`
				if Classify(file, []byte(source), Config{}).Generated {
					t.Fatalf("%s marker outside %q comment after %q was treated as generated", file, header, terminator)
				}
				generated := header + terminator + "// @generated\nconst value = 1;"
				if !Classify(file, []byte(generated), Config{}).Generated {
					t.Fatalf("%s genuine marker after %q was missed", file, terminator)
				}
			}
		}
	}
	for _, extension := range []string{".go", ".rs"} {
		for _, terminator := range []string{"\r", "\u2028", "\u2029"} {
			if !Classify("source"+extension, []byte("// header"+terminator+"@generated"), Config{}).Generated {
				t.Fatalf("%s line comment incorrectly ended at %q", extension, terminator)
			}
		}
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

func TestDeclarationTestFileSuffixes(t *testing.T) {
	for _, suffix := range []string{".d.ts", ".d.mts", ".d.cts"} {
		for _, marker := range []string{".test", ".spec"} {
			file := "src/widget" + marker + suffix
			if got := Classify(file, nil, Config{}); got.Bucket != Tests {
				t.Errorf("%s bucket=%s", file, got.Bucket)
			}
		}
		for _, name := range []string{"widget", "widget.tested"} {
			if got := Classify("src/"+name+suffix, nil, Config{}); got.Bucket != Source {
				t.Errorf("%s%s bucket=%s", name, suffix, got.Bucket)
			}
		}
	}
}

func TestGoGeneratedDirectiveRequiresCanonicalLine(t *testing.T) {
	for _, test := range []struct {
		header    string
		generated bool
	}{
		{"// Code generated fixture. DO NOT EDIT.", true},
		{"// Code generated fixture. DO NOT EDIT.\r\n", true},
		{"/*\n// Code generated fixture. DO NOT EDIT.\n*/", true},
		{"// Not Code generated by this package DO NOT EDIT.", false},
		{"// Code generated DO NOT EDIT.", false},
		{"// Code generated fixture. DO NOT EDIT. This is only an example.", false},
		{"/* Code generated fixture. DO NOT EDIT. */", false},
		{"/* Code generated fixture.\n DO NOT EDIT. */", false},
		{"// Code generated fixture.\n// DO NOT EDIT.", false},
	} {
		if got := Classify("source.go", []byte(test.header+"\npackage fixture\nfunc handwritten() {}\n"), Config{}); got.Generated != test.generated {
			t.Errorf("header %q generated = %v, want %v", test.header, got.Generated, test.generated)
		}
	}
}

func TestPythonGeneratedMarkersRespectCarriageReturnLines(t *testing.T) {
	for _, ending := range []string{"\n", "\r\n", "\r"} {
		source := "# header" + ending + "value = '@generated'" + ending
		if Classify("source.py", []byte(source), Config{}).Generated {
			t.Fatalf("literal marker classified generated for %q", ending)
		}
		source = "# header" + ending + "# @generated" + ending
		if !Classify("generated.py", []byte(source), Config{}).Generated {
			t.Fatalf("leading marker ignored for %q", ending)
		}
	}
}
