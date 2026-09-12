package python

import (
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_python "github.com/tree-sitter/tree-sitter-python/bindings/go"

	"github.com/SolenesInc/slopradar/internal/lang"
)

func Analyze(file string, source []byte) (lang.Result, error) {
	rules := lang.Rules{
		Language:   sitter.NewLanguage(tree_sitter_python.Language()),
		Function:   isFunction,
		Name:       name,
		Decision:   decision,
		Comment:    isComment,
		ParseError: "invalid Python syntax",
	}
	return lang.Analyze(file, source, rules)
}

func isFunction(node *sitter.Node) bool {
	return node.Kind() == "function_definition" || node.Kind() == "lambda"
}

func decision(node *sitter.Node, _ []byte) int {
	switch node.Kind() {
	case "if_statement", "elif_clause", "for_statement", "while_statement", "except_clause",
		"boolean_operator", "conditional_expression", "for_in_clause", "if_clause", "case_clause":
		return 1
	default:
		return 0
	}
}

func name(node *sitter.Node, source []byte) string {
	if node.Kind() == "function_definition" {
		functionName := text(node.ChildByFieldName("name"), source)
		if className := enclosingClass(node, source); className != "" {
			return className + "." + functionName
		}
		return functionName
	}
	for parent, depth := node.Parent(), 0; parent != nil && depth < 5; parent, depth = parent.Parent(), depth+1 {
		switch parent.Kind() {
		case "assignment", "named_expression":
			if target := parent.ChildByFieldName("left"); target != nil {
				return strings.TrimSpace(target.Utf8Text(source))
			}
		case "call":
			if callee := parent.ChildByFieldName("function"); callee != nil {
				return "cb:" + strings.Join(strings.Fields(callee.Utf8Text(source)), "")
			}
		}
	}
	return "(anonymous)"
}

func enclosingClass(node *sitter.Node, source []byte) string {
	for parent := node.Parent(); parent != nil; parent = parent.Parent() {
		if parent.Kind() == "function_definition" {
			return ""
		}
		if parent.Kind() == "class_definition" {
			return text(parent.ChildByFieldName("name"), source)
		}
	}
	return ""
}

func isComment(node *sitter.Node, _ []byte) bool {
	if node.Kind() == "comment" {
		return true
	}
	if node.Kind() != "string" && node.Kind() != "concatenated_string" {
		return false
	}
	expression := node.Parent()
	if expression == nil || expression.Kind() != "expression_statement" {
		return false
	}
	body := expression.Parent()
	if body == nil || (body.Kind() != "module" && body.Kind() != "block") {
		return false
	}
	return body.NamedChildCount() > 0 && body.NamedChild(0).Id() == expression.Id()
}

func text(node *sitter.Node, source []byte) string {
	if node == nil {
		return ""
	}
	return node.Utf8Text(source)
}
