package python

import (
	"fmt"
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
	functionName := localName(node, source)
	if className := enclosingClass(node, source); className != "" {
		return className + "." + functionName
	}
	return functionName
}

func localName(node *sitter.Node, source []byte) string {
	if node.Kind() == "function_definition" {
		return text(node.ChildByFieldName("name"), source)
	}
	suffix := ""
	for child, parent := node, node.Parent(); parent != nil; child, parent = parent, parent.Parent() {
		switch parent.Kind() {
		case "pair":
			if key := parent.ChildByFieldName("key"); key != nil && contains(parent.ChildByFieldName("value"), node) {
				suffix = "[" + key.Utf8Text(source) + "]" + suffix
			}
		case "dictionary":
			if child.Kind() != "pair" || !contains(child.ChildByFieldName("value"), node) {
				if index, _ := sequencePosition(parent, node); index >= 0 {
					suffix = fmt.Sprintf("[entry:%d]", index) + suffix
				}
			}
		case "list", "tuple", "set", "expression_list":
			assignment := parent.Parent()
			if assignment != nil && assignment.Kind() == "assignment" && destructured(assignment.ChildByFieldName("left")) && contains(parent, assignment.ChildByFieldName("right")) {
				continue
			}
			if index, _ := sequencePosition(parent, node); index >= 0 {
				suffix = fmt.Sprintf("[%d]", index) + suffix
			}
		case "assignment":
			if target := assignmentTarget(parent, node, source); target != "" {
				return target + suffix
			}
		case "named_expression":
			if target, value := parent.ChildByFieldName("name"), parent.ChildByFieldName("value"); target != nil && contains(value, node) {
				return strings.TrimSpace(target.Utf8Text(source)) + suffix
			}
		case "call":
			if callee, arguments := parent.ChildByFieldName("function"), parent.ChildByFieldName("arguments"); callee != nil && contains(arguments, node) {
				callback := ".cb:" + strings.Join(strings.Fields(callee.Utf8Text(source)), "")
				if index, count := sequencePosition(arguments, node); count > 1 {
					callback += fmt.Sprintf("[%d]", index)
				}
				suffix = callback + suffix
			}
		case "lambda", "function_definition":
			return callbackSuffix(suffix)
		}
	}
	return callbackSuffix(suffix)
}

func callbackSuffix(suffix string) string {
	if suffix != "" {
		return strings.TrimPrefix(suffix, ".")
	}
	return "(anonymous)"
}

func assignmentTarget(owner, lambda *sitter.Node, source []byte) string {
	targets, values := owner.ChildByFieldName("left"), owner.ChildByFieldName("right")
	if targets == nil || !contains(values, lambda) {
		return ""
	}
	if !destructured(targets) {
		return strings.TrimSpace(targets.Utf8Text(source))
	}
	switch values.Kind() {
	case "expression_list", "tuple", "list":
		if index, _ := sequencePosition(values, lambda); index >= 0 && uint(index) < targets.NamedChildCount() {
			return strings.TrimSpace(targets.NamedChild(uint(index)).Utf8Text(source))
		}
	}
	return strings.TrimSpace(targets.Utf8Text(source))
}

func destructured(node *sitter.Node) bool {
	return node != nil && (node.Kind() == "pattern_list" || node.Kind() == "tuple_pattern" || node.Kind() == "list_pattern")
}

func sequencePosition(parent, node *sitter.Node) (int, int) {
	index, count := -1, 0
	for i := uint(0); i < parent.NamedChildCount(); i++ {
		child := parent.NamedChild(i)
		if child.Kind() == "comment" {
			continue
		}
		if contains(child, node) {
			index = count
		}
		count++
	}
	return index, count
}

func enclosingClass(node *sitter.Node, source []byte) string {
	var names []string
	for parent := node.Parent(); parent != nil; parent = parent.Parent() {
		if isFunction(parent) {
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
