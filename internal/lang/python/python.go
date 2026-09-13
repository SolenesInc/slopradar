package python

import (
	"slices"
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_python "github.com/tree-sitter/tree-sitter-python/bindings/go"

	"github.com/SolenesInc/slopradar/internal/lang"
)

func Analyze(file string, source []byte) (lang.Result, error) {
	rules := lang.Rules{
		Language:       sitter.NewLanguage(tree_sitter_python.Language()),
		Function:       isFunction,
		Name:           name,
		Decision:       decision,
		Comment:        isComment,
		TokenBoundary:  isSuite,
		KeepWhitespace: isStringContent,
		ParseError:     "invalid Python syntax",
	}
	return lang.Analyze(file, source, rules)
}

func isSuite(node *sitter.Node) bool {
	return node.Kind() == "block"
}

func isStringContent(node *sitter.Node) bool {
	return node.Kind() == "string_content"
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
	for parent := node.Parent(); parent != nil; parent = parent.Parent() {
		switch parent.Kind() {
		case "assignment":
			if target := assignmentTarget(parent, node, source); target != "" {
				return target
			}
		case "named_expression":
			if target, value := parent.ChildByFieldName("name"), parent.ChildByFieldName("value"); target != nil && contains(value, node) {
				return strings.TrimSpace(target.Utf8Text(source))
			}
		case "call":
			if callee, arguments := parent.ChildByFieldName("function"), parent.ChildByFieldName("arguments"); callee != nil && contains(arguments, node) {
				return "cb:" + strings.Join(strings.Fields(callee.Utf8Text(source)), "")
			}
		case "lambda", "function_definition":
			return "(anonymous)"
		}
	}
	return "(anonymous)"
}

func assignmentTarget(owner, lambda *sitter.Node, source []byte) string {
	targets := owner.ChildByFieldName("left")
	values := owner.ChildByFieldName("right")
	if targets == nil || values == nil || !contains(values, lambda) {
		return ""
	}
	index := 0
	if values.Kind() == "expression_list" {
		index = -1
		for i := uint(0); i < values.NamedChildCount(); i++ {
			if contains(values.NamedChild(i), lambda) {
				index = int(i)
				break
			}
		}
		if index < 0 {
			return ""
		}
	}
	if targets.Kind() != "pattern_list" && targets.Kind() != "tuple_pattern" {
		if index == 0 {
			return strings.TrimSpace(targets.Utf8Text(source))
		}
		return ""
	}
	if uint(index) >= targets.NamedChildCount() {
		return ""
	}
	return strings.TrimSpace(targets.NamedChild(uint(index)).Utf8Text(source))
}

func enclosingClass(node *sitter.Node, source []byte) string {
	var names []string
	for parent := node.Parent(); parent != nil; parent = parent.Parent() {
		if parent.Kind() == "function_definition" {
			break
		}
		if parent.Kind() == "class_definition" {
			names = append(names, text(parent.ChildByFieldName("name"), source))
		}
	}
	slices.Reverse(names)
	return strings.Join(names, ".")
}

func isComment(node *sitter.Node, source []byte) bool {
	if node.Kind() == "comment" {
		return true
	}
	if !plainString(node, source) {
		return false
	}
	expression := node.Parent()
	if expression == nil || expression.Kind() != "expression_statement" {
		return false
	}
	body := expression.Parent()
	if body == nil || !docstringBody(body) {
		return false
	}
	return body.NamedChildCount() > 0 && body.NamedChild(0).Id() == expression.Id()
}

func plainString(node *sitter.Node, source []byte) bool {
	if node.Kind() == "concatenated_string" {
		if node.NamedChildCount() == 0 {
			return false
		}
		for i := uint(0); i < node.NamedChildCount(); i++ {
			if !plainString(node.NamedChild(i), source) {
				return false
			}
		}
		return true
	}
	if node.Kind() != "string" {
		return false
	}
	literal := node.Utf8Text(source)
	quote := strings.IndexAny(literal, "'\"")
	if quote < 0 {
		return false
	}
	prefix := strings.ToLower(literal[:quote])
	return prefix == "" || prefix == "r" || prefix == "u"
}

func docstringBody(body *sitter.Node) bool {
	if body.Kind() == "module" {
		return true
	}
	if body.Kind() != "block" || body.Parent() == nil {
		return false
	}
	return body.Parent().Kind() == "class_definition" || body.Parent().Kind() == "function_definition"
}

func contains(ancestor, descendant *sitter.Node) bool {
	return ancestor != nil && descendant != nil && ancestor.StartByte() <= descendant.StartByte() && ancestor.EndByte() >= descendant.EndByte()
}

func text(node *sitter.Node, source []byte) string {
	if node == nil {
		return ""
	}
	return node.Utf8Text(source)
}
