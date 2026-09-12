package golang

import (
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_go "github.com/tree-sitter/tree-sitter-go/bindings/go"

	"github.com/SolenesInc/slopradar/internal/lang"
)

func Analyze(file string, source []byte) (lang.Result, error) {
	rules := lang.Rules{
		Language:   sitter.NewLanguage(tree_sitter_go.Language()),
		Function:   isFunction,
		Name:       name,
		Decision:   decision,
		Comment:    func(node *sitter.Node, _ []byte) bool { return node.Kind() == "comment" },
		ParseError: "invalid Go syntax",
	}
	return lang.Analyze(file, source, rules)
}

func isFunction(node *sitter.Node) bool {
	return node.Kind() == "function_declaration" || node.Kind() == "method_declaration" || node.Kind() == "func_literal"
}

func decision(node *sitter.Node, source []byte) int {
	switch node.Kind() {
	case "if_statement", "for_statement", "expression_case", "type_case", "communication_case":
		return 1
	case "binary_expression":
		operator := node.ChildByFieldName("operator")
		if operator != nil && (operator.Utf8Text(source) == "&&" || operator.Utf8Text(source) == "||") {
			return 1
		}
	}
	return 0
}

func name(node *sitter.Node, source []byte) string {
	switch node.Kind() {
	case "function_declaration":
		return text(node.ChildByFieldName("name"), source)
	case "method_declaration":
		receiver := node.ChildByFieldName("receiver")
		return receiverType(receiver, source) + "." + text(node.ChildByFieldName("name"), source)
	}
	for parent := node.Parent(); parent != nil; parent = parent.Parent() {
		switch parent.Kind() {
		case "short_var_declaration", "var_spec", "assignment_statement":
			if target := assignmentTarget(parent, node, source); target != "" {
				return target
			}
		case "keyed_element":
			if key, value := parent.ChildByFieldName("key"), parent.ChildByFieldName("value"); key != nil && contains(value, node) {
				return strings.Trim(key.Utf8Text(source), "\"'`")
			}
		case "call_expression":
			if callee, arguments := parent.ChildByFieldName("function"), parent.ChildByFieldName("arguments"); callee != nil && contains(arguments, node) {
				return "cb:" + compact(callee.Utf8Text(source))
			}
		case "func_literal", "function_declaration", "method_declaration":
			return "(anonymous)"
		}
	}
	return "(anonymous)"
}

func assignmentTarget(owner, function *sitter.Node, source []byte) string {
	var targets, values *sitter.Node
	switch owner.Kind() {
	case "var_spec":
		values = owner.ChildByFieldName("value")
		index := expressionIndex(values, function)
		if index < 0 {
			return ""
		}
		for i, seen := uint(0), 0; i < owner.NamedChildCount(); i++ {
			if owner.FieldNameForNamedChild(uint32(i)) != "name" {
				continue
			}
			if seen == index {
				return strings.TrimSpace(owner.NamedChild(i).Utf8Text(source))
			}
			seen++
		}
		return ""
	default:
		targets = owner.ChildByFieldName("left")
		values = owner.ChildByFieldName("right")
	}
	index := expressionIndex(values, function)
	if index < 0 || targets == nil {
		return ""
	}
	if targets.Kind() != "expression_list" {
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

func expressionIndex(expressions, descendant *sitter.Node) int {
	if expressions == nil || !contains(expressions, descendant) {
		return -1
	}
	if expressions.Kind() != "expression_list" {
		return 0
	}
	for i := uint(0); i < expressions.NamedChildCount(); i++ {
		if contains(expressions.NamedChild(i), descendant) {
			return int(i)
		}
	}
	return -1
}

func contains(ancestor, descendant *sitter.Node) bool {
	return ancestor != nil && descendant != nil && ancestor.StartByte() <= descendant.StartByte() && ancestor.EndByte() >= descendant.EndByte()
}

func receiverType(node *sitter.Node, source []byte) string {
	if node == nil {
		return "(anonymous)"
	}
	var found string
	var visit func(*sitter.Node)
	visit = func(current *sitter.Node) {
		if current.Kind() == "type_identifier" {
			found = current.Utf8Text(source)
			return
		}
		for i := uint(0); found == "" && i < current.NamedChildCount(); i++ {
			visit(current.NamedChild(i))
		}
	}
	visit(node)
	if found == "" {
		return "(anonymous)"
	}
	return found
}

func text(node *sitter.Node, source []byte) string {
	if node == nil {
		return ""
	}
	return node.Utf8Text(source)
}

func compact(value string) string {
	return strings.Join(strings.Fields(value), "")
}
