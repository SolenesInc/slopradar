package rust

import (
	"fmt"
	"slices"
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_rust "github.com/tree-sitter/tree-sitter-rust/bindings/go"

	"github.com/SolenesInc/slopradar/internal/lang"
)

func Analyze(file string, source []byte) (lang.Result, error) {
	rules := lang.Rules{
		Modules:  modules,
		Language: sitter.NewLanguage(tree_sitter_rust.Language()),
		Function: isFunction,
		Name:     name,
		Decision: decision,
		Comment: func(node *sitter.Node, _ []byte) bool {
			return node.Kind() == "line_comment" || node.Kind() == "block_comment"
		},
		TestScope:  isTestScope,
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
	var modules []string
	for parent := node.Parent(); parent != nil && !isFunction(parent); parent = parent.Parent() {
		if parent.Kind() == "mod_item" {
			modules = append(modules, text(parent.ChildByFieldName("name"), source))
		}
	}
	slices.Reverse(modules)
	return strings.Join(append(modules, localName(node, source)), "::")
}

func localName(node *sitter.Node, source []byte) string {
	if node.Kind() == "function_item" {
		functionName := text(node.ChildByFieldName("name"), source)
		for parent := node.Parent(); parent != nil; parent = parent.Parent() {
			if parent.Kind() == "function_item" {
				break
			}
			if parent.Kind() == "impl_item" {
				owner := compact(text(parent.ChildByFieldName("type"), source))
				if trait := parent.ChildByFieldName("trait"); trait != nil {
					owner = "<" + owner + " as " + compact(trait.Utf8Text(source)) + ">"
				}
				return owner + "::" + functionName
			}
			if parent.Kind() == "trait_item" {
				return text(parent.ChildByFieldName("name"), source) + "::" + functionName
			}
		}
		return functionName
	}
	suffix := ""
	for parent := node.Parent(); parent != nil; parent = parent.Parent() {
		if isFunction(parent) {
			break
		}
		switch parent.Kind() {
		case "const_item", "static_item":
			if binding := parent.ChildByFieldName("name"); binding != nil {
				return compact(binding.Utf8Text(source)) + suffix
			}
		case "array_expression", "tuple_expression":
			if index, _ := sequencePosition(parent, node); index >= 0 {
				suffix = fmt.Sprintf("[%d]", index) + suffix
			}
		case "let_declaration":
			if pattern := parent.ChildByFieldName("pattern"); pattern != nil {
				return compact(pattern.Utf8Text(source)) + suffix
			}
		case "assignment_expression":
			if left := parent.ChildByFieldName("left"); left != nil {
				return compact(left.Utf8Text(source)) + suffix
			}
		case "field_initializer":
			if field := parent.ChildByFieldName("field"); field != nil {
				suffix = "." + compact(field.Utf8Text(source)) + suffix
			}
		case "call_expression":
			if function, arguments := parent.ChildByFieldName("function"), parent.ChildByFieldName("arguments"); function != nil && arguments != nil && arguments.StartByte() <= node.StartByte() && arguments.EndByte() >= node.EndByte() {
				callback := ".cb:" + compact(function.Utf8Text(source))
				if index, count := sequencePosition(arguments, node); count > 1 {
					callback += fmt.Sprintf("[%d]", index)
				}
				suffix = callback + suffix
			}
		}
	}
	if suffix != "" {
		return strings.TrimPrefix(suffix, ".")
	}
	return "(anonymous)"
}

func sequencePosition(parent, node *sitter.Node) (int, int) {
	index, count := -1, 0
	for i := uint(0); i < parent.NamedChildCount(); i++ {
		child := parent.NamedChild(i)
		switch child.Kind() {
		case "line_comment", "block_comment", "attribute_item":
			continue
		}
		if child.StartByte() <= node.StartByte() && child.EndByte() >= node.EndByte() {
			index = count
		}
		count++
	}
	return index, count
}

func isTestScope(node *sitter.Node, source []byte) bool {
	if node.Kind() == "source_file" || node.Kind() == "declaration_list" {
		for i := uint(0); i < node.NamedChildCount(); i++ {
			child := node.NamedChild(i)
			if child.Kind() == "inner_attribute_item" && testAttribute(child, source) {
				return true
			}
		}
	}
	if node.Kind() == "attribute_item" {
		for sibling := node.NextNamedSibling(); sibling != nil; sibling = sibling.NextNamedSibling() {
			switch sibling.Kind() {
			case "line_comment", "block_comment", "attribute_item":
				continue
			default:
				return isTestScope(sibling, source)
			}
		}
		return testAttribute(node, source)
	}
	for sibling := node.PrevNamedSibling(); sibling != nil; sibling = sibling.PrevNamedSibling() {
		switch sibling.Kind() {
		case "line_comment", "block_comment":
			continue
		case "attribute_item":
			if testAttribute(sibling, source) {
				return true
			}
		default:
			return false
		}
	}
	return false
}

func testAttribute(item *sitter.Node, source []byte) bool {
	if item == nil || (item.Kind() != "attribute_item" && item.Kind() != "inner_attribute_item") || item.NamedChildCount() != 1 {
		return false
	}
	attribute := item.NamedChild(0)
	if attribute == nil || attribute.Kind() != "attribute" || attribute.NamedChildCount() == 0 {
		return false
	}
	name := attribute.NamedChild(0).Utf8Text(source)
	arguments := attribute.ChildByFieldName("arguments")
	value := attribute.ChildByFieldName("value")
	switch name {
	case "test":
		return arguments == nil && value == nil
	case "cfg":
		return value == nil && arguments != nil && cfgRequiresTest(arguments, source)
	default:
		return false
	}
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
