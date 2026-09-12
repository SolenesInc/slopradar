package lang

import (
	"bytes"
	"fmt"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/SolenesInc/slopradar/internal/model"
)

type Span struct {
	StartByte int `json:"start_byte"`
	EndByte   int `json:"end_byte"`
	StartLine int `json:"start_line"`
	EndLine   int `json:"end_line"`
}

type Token struct {
	Text   string       `json:"text"`
	Line   int          `json:"line"`
	Bucket model.Bucket `json:"bucket"`
}

type Result struct {
	Functions       []model.Function
	FunctionBuckets []model.Bucket
	Comments        []Span
	Tokens          []Token
	TestSpans       []Span
}

type Rules struct {
	Language   *sitter.Language
	Function   func(*sitter.Node) bool
	Name       func(*sitter.Node, []byte) string
	Decision   func(*sitter.Node, []byte) int
	Comment    func(*sitter.Node, []byte) bool
	TestScope  func(*sitter.Node, []byte) bool
	ParseError string
}

type candidate struct {
	node     *sitter.Node
	name     string
	bucket   model.Bucket
	children []*candidate
}

func Analyze(file string, source []byte, rules Rules) (Result, error) {
	parser := sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(rules.Language); err != nil {
		return Result{}, fmt.Errorf("set parser language: %w", err)
	}
	tree := parser.Parse(source, nil)
	if tree == nil {
		return Result{}, fmt.Errorf("parse %s: parser returned no tree", file)
	}
	defer tree.Close()
	root := tree.RootNode()
	if root.HasError() {
		return Result{}, fmt.Errorf("parse %s: %s", file, rules.ParseError)
	}
	result := Result{Functions: []model.Function{}, FunctionBuckets: []model.Bucket{}, Comments: []Span{}, Tokens: []Token{}, TestSpans: []Span{}}
	collectLexical(root, source, rules, model.Source, &result)
	var roots []*candidate
	collectFunctions(root, source, rules, model.Source, nil, &roots)
	for _, root := range roots {
		result.Functions = append(result.Functions, buildFunction(file, source, rules, result.Comments, root))
		result.FunctionBuckets = append(result.FunctionBuckets, root.bucket)
	}
	return result, nil
}

func collectLexical(node *sitter.Node, source []byte, rules Rules, bucket model.Bucket, result *Result) {
	if rules.TestScope != nil && rules.TestScope(node, source) {
		bucket = model.Tests
		result.TestSpans = append(result.TestSpans, spanOf(node))
	}
	if rules.Comment(node, source) {
		result.Comments = append(result.Comments, spanOf(node))
		return
	}
	if node.ChildCount() == 0 {
		text := node.Utf8Text(source)
		if len(bytes.TrimSpace([]byte(text))) != 0 {
			result.Tokens = append(result.Tokens, Token{Text: text, Line: int(node.StartPosition().Row) + 1, Bucket: bucket})
		}
		return
	}
	for i := uint(0); i < node.ChildCount(); i++ {
		collectLexical(node.Child(i), source, rules, bucket, result)
	}
}

func collectFunctions(node *sitter.Node, source []byte, rules Rules, bucket model.Bucket, parent *candidate, roots *[]*candidate) {
	if rules.TestScope != nil && rules.TestScope(node, source) {
		bucket = model.Tests
	}
	current := parent
	if rules.Function(node) {
		current = &candidate{node: node, name: rules.Name(node, source), bucket: bucket}
		if parent == nil {
			*roots = append(*roots, current)
		} else {
			parent.children = append(parent.children, current)
		}
	}
	for i := uint(0); i < node.NamedChildCount(); i++ {
		collectFunctions(node.NamedChild(i), source, rules, bucket, current, roots)
	}
}

func buildFunction(file string, source []byte, rules Rules, comments []Span, item *candidate) model.Function {
	cc := 1 + decisions(item.node, source, rules)
	var nested []model.Function
	for _, child := range item.children {
		nested = append(nested, buildFunction(file, source, rules, comments, child))
	}
	sloc := countSLOC(source, int(item.node.StartByte()), int(item.node.EndByte()), comments)
	return model.NewFunction(file, item.name, int(item.node.StartPosition().Row)+1, cc, sloc, nested)
}

func decisions(node *sitter.Node, source []byte, rules Rules) int {
	total := rules.Decision(node, source)
	for i := uint(0); i < node.NamedChildCount(); i++ {
		total += decisions(node.NamedChild(i), source, rules)
	}
	return total
}

func spanOf(node *sitter.Node) Span {
	return Span{
		StartByte: int(node.StartByte()), EndByte: int(node.EndByte()),
		StartLine: int(node.StartPosition().Row) + 1, EndLine: int(node.EndPosition().Row) + 1,
	}
}

func countSLOC(source []byte, start, end int, comments []Span) int {
	fragment := append([]byte(nil), source[start:end]...)
	for _, comment := range comments {
		left := max(start, comment.StartByte)
		right := min(end, comment.EndByte)
		for i := left; i < right; i++ {
			if source[i] != '\n' && source[i] != '\r' {
				fragment[i-start] = ' '
			}
		}
	}
	count := 0
	for _, line := range bytes.Split(fragment, []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) != 0 {
			count++
		}
	}
	return count
}
