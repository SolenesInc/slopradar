package rust

import (
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_rust "github.com/tree-sitter/tree-sitter-rust/bindings/go"

	"github.com/SolenesInc/slopradar/internal/lang"
)

func Analyze(file string, source []byte) (lang.Result, error) {
	rules := lang.Rules{
		Language: sitter.NewLanguage(tree_sitter_rust.Language()),
		Function: isFunction,
		Name:     name,
		Decision: decision,
		Comment: func(node *sitter.Node, _ []byte) bool {
			return node.Kind() == "line_comment" || node.Kind() == "block_comment"
		},
		TestScope:  isTestModule,
		ParseError: "invalid Rust syntax",
	}
	return lang.Analyze(file, source, rules)
}

func isFunction(node *sitter.Node) bool {
	return node.Kind() == "function_item" || node.Kind() == "closure_expression"
}

func decision(node *sitter.Node, source []byte) int {
	switch node.Kind() {
	case "if_expression", "while_expression", "for_expression", "loop_expression", "match_arm", "try_expression":
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
	if node.Kind() == "function_item" {
		functionName := text(node.ChildByFieldName("name"), source)
		for parent := node.Parent(); parent != nil; parent = parent.Parent() {
			if parent.Kind() == "function_item" {
				break
			}
			if parent.Kind() == "impl_item" {
				return compact(text(parent.ChildByFieldName("type"), source)) + "::" + functionName
			}
		}
		return functionName
	}
	for parent, depth := node.Parent(), 0; parent != nil && depth < 5; parent, depth = parent.Parent(), depth+1 {
		switch parent.Kind() {
		case "let_declaration":
			if pattern := parent.ChildByFieldName("pattern"); pattern != nil {
				return compact(pattern.Utf8Text(source))
			}
		case "assignment_expression":
			if left := parent.ChildByFieldName("left"); left != nil {
				return compact(left.Utf8Text(source))
			}
		case "field_initializer":
			if field := parent.ChildByFieldName("field"); field != nil {
				return compact(field.Utf8Text(source))
			}
		case "call_expression":
			if function := parent.ChildByFieldName("function"); function != nil {
				return "cb:" + compact(function.Utf8Text(source))
			}
		}
	}
	return "(anonymous)"
}

func isTestModule(node *sitter.Node, source []byte) bool {
	if node.Kind() == "attribute_item" {
		return testAttribute(node, source)
	}
	if node.Kind() != "mod_item" && node.Kind() != "function_item" {
		return false
	}
	for sibling := node.PrevNamedSibling(); sibling != nil && sibling.Kind() == "attribute_item"; sibling = sibling.PrevNamedSibling() {
		if testAttribute(sibling, source) {
			return true
		}
	}
	return false
}

func testAttribute(node *sitter.Node, source []byte) bool {
	attribute := strings.Join(strings.Fields(node.Utf8Text(source)), "")
	return attribute == "#[test]" || strings.Contains(attribute, "cfg(test)")
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
