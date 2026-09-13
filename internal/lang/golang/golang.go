package golang

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/scanner"
	"go/token"
	"strings"

	"github.com/SolenesInc/slopradar/internal/lang"
	"github.com/SolenesInc/slopradar/internal/model"
)

type candidate struct {
	node     ast.Node
	name     string
	children []*candidate
}

func Analyze(file string, source []byte) (lang.Result, error) {
	set := token.NewFileSet()
	parsed, err := parser.ParseFile(set, file, source, parser.AllErrors|parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return lang.Result{}, fmt.Errorf("parse %s: invalid Go syntax: %w", file, err)
	}
	tokenFile := set.File(parsed.Pos())
	comments, tokens, err := lexical(file, source)
	if err != nil {
		return lang.Result{}, err
	}
	parents := parentNodes(parsed)
	roots := functionCandidates(parsed, parents, source)
	result := lang.Result{
		Functions:       []model.Function{},
		FunctionBuckets: []model.Bucket{},
		Comments:        comments,
		Tokens:          tokens,
		TestSpans:       []lang.Span{},
		Warnings:        []string{},
	}
	for _, root := range roots {
		result.Functions = append(result.Functions, buildFunction(file, source, tokenFile, comments, root))
		result.FunctionBuckets = append(result.FunctionBuckets, model.Source)
	}
	return result, nil
}

func lexical(file string, source []byte) ([]lang.Span, []lang.Token, error) {
	set := token.NewFileSet()
	tokenFile := set.AddFile(file, set.Base(), len(source))
	errors := scanner.ErrorList{}
	var lexer scanner.Scanner
	lexer.Init(tokenFile, source, func(position token.Position, message string) {
		errors.Add(position, message)
	}, scanner.ScanComments)
	comments := []lang.Span{}
	tokens := []lang.Token{}
	for {
		position, kind, literal := lexer.Scan()
		end := lexer.End()
		if kind == token.EOF {
			break
		}
		if kind == token.SEMICOLON && literal == "\n" {
			continue
		}
		startOffset := tokenFile.Offset(position)
		endOffset := tokenFile.Offset(end)
		if kind == token.COMMENT {
			comments = append(comments, lang.Span{
				StartByte: startOffset,
				EndByte:   endOffset,
				StartLine: tokenFile.PositionFor(position, false).Line,
				EndLine:   tokenFile.PositionFor(end, false).Line,
			})
			continue
		}
		if kind == token.STRING {
			tokens = appendStringTokens(tokens, tokenFile, source, startOffset, endOffset)
			continue
		}
		tokens = appendToken(tokens, tokenFile, source, startOffset, endOffset)
	}
	if len(errors) != 0 {
		errors.Sort()
		return nil, nil, fmt.Errorf("scan %s: invalid Go tokens: %w", file, errors)
	}
	return comments, tokens, nil
}

func appendStringTokens(tokens []lang.Token, tokenFile *token.File, source []byte, start, end int) []lang.Token {
	tokens = appendToken(tokens, tokenFile, source, start, start+1)
	if source[start] == '`' {
		if start+1 < end-1 {
			tokens = appendContentToken(tokens, tokenFile, source, start+1, end-1)
		}
		return appendToken(tokens, tokenFile, source, end-1, end)
	}
	contentStart := start + 1
	for offset := contentStart; offset < end-1; {
		if source[offset] != '\\' {
			offset++
			continue
		}
		if contentStart < offset {
			tokens = appendContentToken(tokens, tokenFile, source, contentStart, offset)
		}
		escapeEnd := offset + escapeWidth(source[offset:end-1])
		tokens = appendToken(tokens, tokenFile, source, offset, escapeEnd)
		offset = escapeEnd
		contentStart = offset
	}
	if contentStart < end-1 {
		tokens = appendContentToken(tokens, tokenFile, source, contentStart, end-1)
	}
	return appendToken(tokens, tokenFile, source, end-1, end)
}

func appendContentToken(tokens []lang.Token, tokenFile *token.File, source []byte, start, end int) []lang.Token {
	return appendToken(tokens, tokenFile, source, start, end)
}

func escapeWidth(source []byte) int {
	if len(source) < 2 {
		return len(source)
	}
	switch source[1] {
	case 'x':
		return min(4, len(source))
	case 'u':
		return min(6, len(source))
	case 'U':
		return min(10, len(source))
	default:
		if source[1] >= '0' && source[1] <= '7' {
			return min(4, len(source))
		}
		return 2
	}
}

func appendToken(tokens []lang.Token, tokenFile *token.File, source []byte, start, end int) []lang.Token {
	position := tokenFile.Pos(start)
	return append(tokens, lang.Token{
		Text:   string(source[start:end]),
		Line:   tokenFile.PositionFor(position, false).Line,
		Bucket: model.Source,
	})
}

func parentNodes(root ast.Node) map[ast.Node]ast.Node {
	parents := map[ast.Node]ast.Node{}
	stack := []ast.Node{}
	ast.Inspect(root, func(node ast.Node) bool {
		if node == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		if len(stack) != 0 {
			parents[node] = stack[len(stack)-1]
		}
		stack = append(stack, node)
		return true
	})
	return parents
}

func functionCandidates(root ast.Node, parents map[ast.Node]ast.Node, source []byte) []*candidate {
	roots := []*candidate{}
	var current *candidate
	previous := []*candidate{}
	ast.Inspect(root, func(node ast.Node) bool {
		if node == nil {
			current = previous[len(previous)-1]
			previous = previous[:len(previous)-1]
			return true
		}
		previous = append(previous, current)
		if !isFunction(node) {
			return true
		}
		item := &candidate{node: node, name: functionName(node, parents, source)}
		if current == nil {
			roots = append(roots, item)
		} else {
			current.children = append(current.children, item)
		}
		current = item
		return true
	})
	return roots
}

func isFunction(node ast.Node) bool {
	switch node := node.(type) {
	case *ast.FuncDecl:
		return node.Body != nil
	case *ast.FuncLit:
		return true
	default:
		return false
	}
}

func buildFunction(file string, source []byte, tokenFile *token.File, comments []lang.Span, item *candidate) model.Function {
	var nested []model.Function
	for _, child := range item.children {
		nested = append(nested, buildFunction(file, source, tokenFile, comments, child))
	}
	start := tokenFile.Offset(item.node.Pos())
	end := tokenFile.Offset(item.node.End())
	return model.NewFunction(
		file,
		item.name,
		tokenFile.PositionFor(item.node.Pos(), false).Line,
		1+decisions(item.node),
		lang.CountSLOC(source, start, end, comments),
		nested,
	)
}

func decisions(root ast.Node) int {
	total := 0
	ast.Inspect(root, func(node ast.Node) bool {
		switch item := node.(type) {
		case *ast.IfStmt, *ast.ForStmt, *ast.RangeStmt:
			total++
		case *ast.CaseClause:
			if len(item.List) != 0 {
				total++
			}
		case *ast.CommClause:
			if item.Comm != nil {
				total++
			}
		case *ast.BinaryExpr:
			if item.Op == token.LAND || item.Op == token.LOR {
				total++
			}
		}
		return true
	})
	return total
}

func functionName(node ast.Node, parents map[ast.Node]ast.Node, source []byte) string {
	if declaration, ok := node.(*ast.FuncDecl); ok {
		if declaration.Recv == nil {
			return declaration.Name.Name
		}
		return receiverType(declaration.Recv) + "." + declaration.Name.Name
	}
	for parent := parents[node]; parent != nil; parent = parents[parent] {
		switch owner := parent.(type) {
		case *ast.AssignStmt:
			if target := assignmentTarget(owner.Lhs, owner.Rhs, node, source); target != "" {
				return target
			}
		case *ast.ValueSpec:
			if target := valueSpecTarget(owner, node); target != nil {
				return target.Name
			}
		case *ast.KeyValueExpr:
			if contains(owner.Value, node) {
				return strings.Trim(sourceText(owner.Key, source), "\"'`")
			}
		case *ast.CallExpr:
			if expressionIndex(owner.Args, node) >= 0 {
				return "cb:" + compact(sourceText(owner.Fun, source))
			}
		case *ast.FuncLit, *ast.FuncDecl:
			return "(anonymous)"
		}
	}
	return "(anonymous)"
}

func assignmentTarget(targets, values []ast.Expr, function ast.Node, source []byte) string {
	index := expressionIndex(values, function)
	if index < 0 || index >= len(targets) {
		return ""
	}
	return strings.TrimSpace(sourceText(targets[index], source))
}

func valueSpecTarget(spec *ast.ValueSpec, function ast.Node) *ast.Ident {
	index := expressionIndex(spec.Values, function)
	if index < 0 || index >= len(spec.Names) {
		return nil
	}
	return spec.Names[index]
}

func expressionIndex(expressions []ast.Expr, descendant ast.Node) int {
	for index, expression := range expressions {
		if contains(expression, descendant) {
			return index
		}
	}
	return -1
}

func contains(ancestor, descendant ast.Node) bool {
	return ancestor != nil && descendant != nil && ancestor.Pos() <= descendant.Pos() && ancestor.End() >= descendant.End()
}

func receiverType(receiver *ast.FieldList) string {
	if receiver == nil || len(receiver.List) == 0 {
		return "(anonymous)"
	}
	expression := receiver.List[0].Type
	for {
		switch item := expression.(type) {
		case *ast.Ident:
			return item.Name
		case *ast.ParenExpr:
			expression = item.X
		case *ast.StarExpr:
			expression = item.X
		case *ast.IndexExpr:
			expression = item.X
		case *ast.IndexListExpr:
			expression = item.X
		default:
			return "(anonymous)"
		}
	}
}

func sourceText(node ast.Node, source []byte) string {
	if node == nil {
		return ""
	}
	start := int(node.Pos()) - 1
	end := int(node.End()) - 1
	if start < 0 || end < start || end > len(source) {
		return ""
	}
	return string(source[start:end])
}

func compact(value string) string {
	return strings.Join(strings.Fields(value), "")
}
