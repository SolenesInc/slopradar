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
	case "if_statement", "for_statement", "expression_case", "communication_case":
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
	for parent, depth := node.Parent(), 0; parent != nil && depth < 4; parent, depth = parent.Parent(), depth+1 {
		switch parent.Kind() {
		case "short_var_declaration", "var_spec", "assignment_statement":
			if target := firstIdentifier(parent.ChildByFieldName("left"), source); target != "" {
				return target
			}
			if target := text(parent.ChildByFieldName("name"), source); target != "" {
				return target
			}
		case "keyed_element":
			if key := parent.NamedChild(0); key != nil && key.Id() != node.Id() {
				return strings.Trim(key.Utf8Text(source), "\"'`")
			}
		case "call_expression":
			if callee := parent.ChildByFieldName("function"); callee != nil {
				return "cb:" + compact(callee.Utf8Text(source))
			}
		}
	}
	return "(anonymous)"
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

func firstIdentifier(node *sitter.Node, source []byte) string {
	if node == nil {
		return ""
	}
	if node.Kind() == "identifier" || node.Kind() == "field_identifier" || node.Kind() == "selector_expression" {
		return node.Utf8Text(source)
	}
	for i := uint(0); i < node.NamedChildCount(); i++ {
		if found := firstIdentifier(node.NamedChild(i), source); found != "" {
			return found
		}
	}
	return ""
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
