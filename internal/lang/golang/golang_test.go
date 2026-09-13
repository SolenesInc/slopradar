package golang

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/SolenesInc/slopradar/internal/clones"
	"github.com/SolenesInc/slopradar/internal/lang"
	"github.com/SolenesInc/slopradar/internal/model"
)

func TestAnalyzeGolden(t *testing.T) {
	root := filepath.Join("..", "..", "..", "testdata", "go")
	path := filepath.Join(root, "functions.go")
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	result, err := Analyze("testdata/go/functions.go", source)
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
	for _, token := range result.Tokens {
		if token.Text == "// comment-only line" {
			t.Fatal("comment leaked into token stream")
		}
	}
}

func TestBodylessDeclarationsDoNotContributeFunctionMass(t *testing.T) {
	source := []byte("package fixture\nfunc external()\nfunc implemented() {}\nvar literal = func() { if ready() {} }\n")
	result, err := Analyze("fixture.go", source)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, function := range result.Functions {
		names = append(names, function.Name)
	}
	if !reflect.DeepEqual(names, []string{"implemented", "literal"}) {
		t.Fatalf("functions = %#v", result.Functions)
	}
	if result.Functions[0].CC != 1 || result.Functions[1].CC != 2 {
		t.Fatalf("implemented metrics = %#v", result.Functions)
	}
	foundDeclarationToken := false
	for _, token := range result.Tokens {
		foundDeclarationToken = foundDeclarationToken || token.Text == "external"
	}
	if !foundDeclarationToken {
		t.Fatal("bodyless declaration disappeared from lexical clone input")
	}
}

func TestClassificationCoversGoDefaultsAndConfiguration(t *testing.T) {
	generated, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "go", "generated.go"))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		path    string
		content []byte
		config  model.Config
		want    model.Classification
	}{
		{path: "x.go", want: model.Classification{Bucket: model.Source}},
		{path: "x_test.go", want: model.Classification{Bucket: model.Tests}},
		{path: "vendor/x.go", want: model.Classification{Excluded: true}},
		{path: "x.go", content: generated, want: model.Classification{Bucket: model.Source, Generated: true}},
		{path: "bench/x.go", config: model.Config{TestGlobs: []string{"bench/"}}, want: model.Classification{Bucket: model.Tests}},
		{path: "private/x.go", config: model.Config{Excludes: []string{"private/"}}, want: model.Classification{Excluded: true}},
	}
	for _, test := range cases {
		if got := model.Classify(test.path, test.content, test.config); got != test.want {
			t.Errorf("Classify(%q) = %#v, want %#v", test.path, got, test.want)
		}
	}
}

func TestConfigRejectsUnknownAndTrailingContent(t *testing.T) {
	for _, data := range []string{`{"unknown": []}`, `{} {}`} {
		if _, err := model.ParseConfig([]byte(data)); err == nil {
			t.Fatalf("ParseConfig(%q) succeeded", data)
		}
	}
}

func TestTypeSwitchCountsClausesAndNestedDecisions(t *testing.T) {
	source := []byte(`package fixture
func classify(value any) int {
    switch item := value.(type) {
    case int, string:
        switch any(item).(type) {
        case int:
            return 1
        default:
            return 2
        }
    case bool:
        return 3
	default:
		return 4
	}
}
`)
	result, err := Analyze("fixture.go", source)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Functions) != 1 || result.Functions[0].CC != 4 {
		t.Fatalf("base plus three non-default clauses: %#v", result.Functions)
	}
}

func TestFunctionLiteralNamesFollowStructuralOwners(t *testing.T) {
	source := []byte(`package fixture
var first, second = func() {}, func() {}

func outer() {
    left, right := func() {}, func() {}
    assigned, assignedField := func() {}, func() {}
    _ = left
    _ = right
    _ = assigned
    _ = assignedField
    deeplyWrapped := (((((func() {})))))
    _ = deeplyWrapped
    register((((((func() {}))))))
    immediate := (func() {})()
    _ = immediate
	nested := func() func() { return func() {} }
	_ = nested
}
`)
	result, err := Analyze("fixture.go", source)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	var appendNested func([]model.Function)
	appendNested = func(functions []model.Function) {
		for _, function := range functions {
			names = append(names, function.Name)
			appendNested(function.Nested)
		}
	}
	for _, function := range result.Functions {
		if function.Name == "outer" {
			appendNested(function.Nested)
			continue
		}
		names = append(names, function.Name)
	}
	want := []string{"first", "second", "left", "right", "assigned", "assignedField", "deeplyWrapped", "cb:register", "immediate", "nested", "(anonymous)"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("names = %#v, want %#v", names, want)
	}
}

func TestGo127GeneralizedNewAndGenericMethods(t *testing.T) {
	source := []byte(`package fixture
type Pair[A, B any] struct{}
var answer = new(42)

func (*Pair[A, B]) Map[C any](value C) C {
    nested := func(flag bool) C {
        if flag && true {
            return value
        }
        return value
    }
    return nested(new(true) != nil)
}
`)
	result, err := Analyze("fixture.go", source)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("warnings = %#v", result.Warnings)
	}
	if len(result.Functions) != 1 || result.Functions[0].Name != "Pair.Map" || result.Functions[0].CC != 3 {
		t.Fatalf("generic method = %#v", result.Functions)
	}
	nested := result.Functions[0].Nested
	if len(nested) != 1 || nested[0].Name != "nested" || nested[0].CC != 3 {
		t.Fatalf("nested function = %#v", nested)
	}
}

func TestPhysicalCoordinatesAndExactLexicalTokens(t *testing.T) {
	source := []byte("package fixture\r\n//line generated.go:700\r\nfunc physical() {\r\nraw := `a\r\nb`\r\nmessage := \"left\\nright\"\r\n_ = raw; // trailing\r\n_ = message\r\n}\r\n")
	result, err := Analyze("physical.go", source)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Functions) != 1 || result.Functions[0].Line != 3 {
		t.Fatalf("function coordinates = %#v", result.Functions)
	}
	if len(result.Comments) != 2 || result.Comments[0].StartLine != 2 || result.Comments[1].StartLine != 7 {
		t.Fatalf("comment coordinates = %#v", result.Comments)
	}
	texts := []string{}
	lines := map[string]int{}
	for _, item := range result.Tokens {
		texts = append(texts, item.Text)
		lines[item.Text] = item.Line
	}
	if lines["func"] != 3 || lines["a\r\nb"] != 4 {
		t.Fatalf("token coordinates = %#v", result.Tokens)
	}
	if strings.Count(strings.Join(texts, "\x00"), ";") != 1 {
		t.Fatalf("implicit semicolons leaked or explicit semicolon disappeared: %#v", texts)
	}
	if !reflect.DeepEqual(stringTokens(texts), []string{"`", "a\r\nb", "`", "\"", "left", "\\n", "right", "\""}) {
		t.Fatalf("string tokens changed source bytes: %#v", stringTokens(texts))
	}
}

func TestWhitespaceOnlyStringContentsRemainCloneTokens(t *testing.T) {
	source := []byte("package fixture\nvar one = \" \"\nvar two = \"  \"\nvar raw = `\t`\n")
	result, err := Analyze("fixture.go", source)
	if err != nil {
		t.Fatal(err)
	}
	contents := []string{}
	for _, item := range result.Tokens {
		if item.Text == " " || item.Text == "  " || item.Text == "\t" {
			contents = append(contents, item.Text)
		}
	}
	if !reflect.DeepEqual(contents, []string{" ", "  ", "\t"}) {
		t.Fatalf("whitespace string tokens = %#v", contents)
	}
}

func TestWhitespaceOnlyStringContentsPreventFalseExactClones(t *testing.T) {
	firstSource := []byte(`package fixture
func sample() {
	one := 1
	two := 2
	three := 3
	four := 4
	five := " "
	six := 6
	seven := 7
	_ = one
	_ = two
	_ = three
	_ = four
	_ = five
	_ = six
	return
	return
}
`)
	secondSource := []byte(strings.ReplaceAll(string(firstSource), `" "`, `"  "`))
	sources := [][]byte{firstSource, secondSource}
	files := make([]clones.File, 0, len(sources))
	oldFiles := make([]clones.File, 0, len(sources))
	for index, source := range sources {
		result, err := Analyze(fmt.Sprintf("fixture-%d.go", index), source)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Tokens) != clones.JscpdDefaultMinimumTokens+1 {
			t.Fatalf("tokens = %d, want %d", len(result.Tokens), clones.JscpdDefaultMinimumTokens+1)
		}
		sourceLines := lang.SourceLines(source, result.Comments, result.TestSpans)
		if len(sourceLines[model.Source]) < clones.JscpdDefaultMinimumLines {
			t.Fatalf("source lines = %d, want at least %d", len(sourceLines[model.Source]), clones.JscpdDefaultMinimumLines)
		}
		path := fmt.Sprintf("fixture-%d.go", index)
		files = append(files, clones.File{Path: path, Language: "go", Tokens: result.Tokens, SourceLines: sourceLines})
		oldTokens := make([]lang.Token, 0, len(result.Tokens))
		for _, item := range result.Tokens {
			if strings.TrimSpace(item.Text) == "" {
				continue
			}
			oldTokens = append(oldTokens, item)
		}
		oldFiles = append(oldFiles, clones.File{Path: path, Language: "go", Tokens: oldTokens, SourceLines: sourceLines})
	}
	if result := clones.Detect(oldFiles); len(result.Pairs) != 1 || result.Pairs[0].Tokens != clones.JscpdDefaultMinimumTokens {
		t.Fatalf("old filtered clone result = %#v", result.Pairs)
	}
	if result := clones.Detect(files); len(result.Pairs) != 0 {
		t.Fatalf("whitespace-sensitive clone result = %#v", result.Pairs)
	}
}

func TestAnalyzeRejectsRecoveredSyntax(t *testing.T) {
	result, err := Analyze("broken.go", []byte("package fixture\nfunc broken("))
	if err == nil || !strings.Contains(err.Error(), "parse broken.go: invalid Go syntax") {
		t.Fatalf("result = %#v, error = %v", result, err)
	}
}

func TestBareCarriageReturnsRemainGoWhitespace(t *testing.T) {
	source := []byte("package fixture; func value() int {\r/* first\rsecond */\rtext := `one\rtwo`;\rreturn len(text);\r}")
	result, err := Analyze("fixture.go", source)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Functions) != 1 || result.Functions[0].Line != 1 || result.Functions[0].SLOC != 1 {
		t.Fatalf("functions = %#v", result.Functions)
	}
	if len(result.Comments) != 1 || result.Comments[0].StartLine != 1 || result.Comments[0].EndLine != 1 {
		t.Fatalf("comments = %#v", result.Comments)
	}
	foundRawCarriageReturn := false
	for _, token := range result.Tokens {
		if token.Line != 1 {
			t.Fatalf("token = %#v, want line 1", token)
		}
		if token.Text == "one\rtwo" {
			foundRawCarriageReturn = true
		}
	}
	if !foundRawCarriageReturn {
		t.Fatalf("raw string bytes changed: %#v", result.Tokens)
	}
	lines := lang.SourceLines(source, result.Comments, result.TestSpans)
	if want := []int{1}; !reflect.DeepEqual(lines[model.Source], want) {
		t.Fatalf("source lines = %#v, want %#v", lines[model.Source], want)
	}
}

func TestAnalyzePreservesArbitraryFilenameBytes(t *testing.T) {
	file := string([]byte{'f', 'i', 'x', 't', 'u', 'r', 'e', 0xff, '.', 'g', 'o'})
	result, err := Analyze(file, []byte("package fixture\nfunc kept() {}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Functions) != 1 || result.Functions[0].File != file {
		t.Fatalf("functions = %#v", result.Functions)
	}
}

func stringTokens(values []string) []string {
	result := []string{}
	for _, value := range values {
		if value == "`" || value == "a\r\nb" || value == "\"" || value == "left" || value == "\\n" || value == "right" {
			result = append(result, value)
		}
	}
	return result
}
