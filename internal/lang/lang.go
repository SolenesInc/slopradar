package lang

import (
	"bytes"
	"fmt"
	"unicode"

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

type Module struct {
	Name           string
	Path           *string
	AlternatePaths []string
	TestOnly       bool
	Inline         bool
	Uncertain      bool
	Children       []Module
}

type Result struct {
	Modules         []Module
	TestOnly        bool
	Functions       []model.Function
	FunctionBuckets []model.Bucket
	Comments        []Span
	Tokens          []Token
	TestSpans       []Span
	Warnings        []string
}

type Rules struct {
	Modules        func(*sitter.Node, []byte) []Module
	Language       *sitter.Language
	Function       func(*sitter.Node) bool
	Name           func(*sitter.Node, []byte) string
	Decision       func(*sitter.Node, []byte) int
	Comment        func(*sitter.Node, []byte) bool
	TestScope      func(*sitter.Node, []byte) bool
	TokenBoundary  func(*sitter.Node) bool
	KeepWhitespace func(*sitter.Node) bool
	ParseError     string
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
	result := Result{Functions: []model.Function{}, FunctionBuckets: []model.Bucket{}, Comments: []Span{}, Tokens: []Token{}, TestSpans: []Span{}, Warnings: []string{}}
	if root.HasError() {
		result.Warnings = append(result.Warnings, fmt.Sprintf("parse %s: %s; analyzed recoverable syntax", file, rules.ParseError))
	}
	if rules.Modules != nil {
		result.Modules = rules.Modules(root, source)
	}
	result.TestOnly = rules.TestScope != nil && rules.TestScope(root, source)
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
		if len(bytes.TrimSpace([]byte(text))) != 0 || rules.KeepWhitespace != nil && rules.KeepWhitespace(node) {
			result.Tokens = append(result.Tokens, Token{Text: text, Line: int(node.StartPosition().Row) + 1, Bucket: bucket})
		}
		return
	}
	boundary := rules.TokenBoundary != nil && rules.TokenBoundary(node)
	boundaryIndex := len(result.Tokens)
	if boundary {
		result.Tokens = append(result.Tokens, Token{Text: "\x00" + node.Kind() + ":open", Bucket: bucket})
	}
	for i := uint(0); i < node.ChildCount(); i++ {
		collectLexical(node.Child(i), source, rules, bucket, result)
	}
	if boundary {
		if len(result.Tokens) == boundaryIndex+1 {
			result.Tokens = result.Tokens[:boundaryIndex]
			return
		}
		result.Tokens[boundaryIndex].Line = result.Tokens[boundaryIndex+1].Line
		result.Tokens = append(result.Tokens, Token{Text: "\x00" + node.Kind() + ":close", Line: result.Tokens[len(result.Tokens)-1].Line, Bucket: bucket})
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
	function := model.NewFunction(file, item.name, int(item.node.StartPosition().Row)+1, cc, sloc, nested)
	if item.bucket == model.Tests {
		function.Bucket = model.Tests
	}
	return function
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

func CountSLOC(source []byte, start, end int, comments []Span) int {
	return countSLOC(source, start, end, comments)
}

func CountLines(source []byte, comments, testSpans []Span) map[model.Bucket]int {
	lines := SourceLines(source, comments, testSpans)
	return map[model.Bucket]int{model.Source: len(lines[model.Source]), model.Tests: len(lines[model.Tests])}
}

func SourceLines(source []byte, comments, testSpans []Span) map[model.Bucket][]int {
	return sourceLines(source, comments, testSpans, false)
}

func JavaScriptSourceLines(source []byte, comments, testSpans []Span) map[model.Bucket][]int {
	return sourceLines(source, comments, testSpans, true)
}

func sourceLines(source []byte, comments, testSpans []Span, javascript bool) map[model.Bucket][]int {
	content := append([]byte(nil), source...)
	lineBreaks := make([]bool, len(content))
	for offset := 0; offset < len(content); {
		width := lineTerminatorWidth(content, offset, javascript)
		if width == 0 {
			offset++
			continue
		}
		for i := range width {
			lineBreaks[offset+i] = true
		}
		offset += width
	}
	for _, comment := range comments {
		for i := comment.StartByte; i < comment.EndByte; i++ {
			if !lineBreaks[i] {
				content[i] = ' '
			}
		}
	}
	lines := map[model.Bucket][]int{model.Source: {}, model.Tests: {}}
	whitespace := unicode.IsSpace
	if javascript {
		whitespace = javaScriptWhitespace
	}
	lineStart := 0
	lineNumber := 1
	for lineStart <= len(content) {
		lineEnd, terminatorWidth := nextLineTerminator(content, lineStart, javascript)
		if len(bytes.TrimFunc(content[lineStart:lineEnd], whitespace)) != 0 {
			bucket := model.Source
			for _, span := range testSpans {
				if lineStart < span.EndByte && lineEnd >= span.StartByte {
					bucket = model.Tests
					break
				}
			}
			lines[bucket] = append(lines[bucket], lineNumber)
		}
		if terminatorWidth == 0 {
			break
		}
		lineStart = lineEnd + terminatorWidth
		lineNumber++
	}
	return lines
}

func javaScriptWhitespace(character rune) bool {
	return character == '\t' || character == '\v' || character == '\f' || character == '\ufeff' || unicode.Is(unicode.Zs, character)
}

func nextLineTerminator(content []byte, start int, javascript bool) (int, int) {
	for offset := start; offset < len(content); offset++ {
		if width := lineTerminatorWidth(content, offset, javascript); width != 0 {
			return offset, width
		}
	}
	return len(content), 0
}

func CountLineTerminators(content []byte, javascript bool) int {
	count := 0
	for offset := 0; offset < len(content); {
		width := lineTerminatorWidth(content, offset, javascript)
		if width == 0 {
			offset++
			continue
		}
		count++
		offset += width
	}
	return count
}

func lineTerminatorWidth(content []byte, offset int, javascript bool) int {
	switch content[offset] {
	case '\n':
		return 1
	case '\r':
		if !javascript {
			return 0
		}
		if offset+1 < len(content) && content[offset+1] == '\n' {
			return 2
		}
		return 1
	case 0xe2:
		if javascript && offset+2 < len(content) && content[offset+1] == 0x80 && (content[offset+2] == 0xa8 || content[offset+2] == 0xa9) {
			return 3
		}
	}
	return 0
}
