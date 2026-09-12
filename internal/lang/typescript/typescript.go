package typescript

import (
	"fmt"
	"path"
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_javascript "github.com/tree-sitter/tree-sitter-javascript/bindings/go"
	tree_sitter_typescript "github.com/tree-sitter/tree-sitter-typescript/bindings/go"

	"github.com/SolenesInc/slopradar/internal/lang"
)

func Analyze(file string, source []byte) (lang.Result, error) {
	grammar, err := grammarFor(file)
	if err != nil {
		return lang.Result{}, err
	}
	rules := lang.Rules{
		Language:   grammar,
		Function:   isFunction,
		Name:       name,
		Decision:   decision,
		Comment:    func(node *sitter.Node, _ []byte) bool { return node.Kind() == "comment" },
		ParseError: "invalid TypeScript or JavaScript syntax",
	}
	return lang.Analyze(file, source, rules)
}

func grammarFor(file string) (*sitter.Language, error) {
	switch strings.ToLower(path.Ext(file)) {
	case ".ts", ".mts", ".cts":
		return sitter.NewLanguage(tree_sitter_typescript.LanguageTypescript()), nil
	case ".tsx":
		return sitter.NewLanguage(tree_sitter_typescript.LanguageTSX()), nil
	case ".js", ".jsx", ".mjs", ".cjs":
		return sitter.NewLanguage(tree_sitter_javascript.Language()), nil
	default:
		return nil, fmt.Errorf("unsupported TypeScript or JavaScript file %q", file)
	}
}

func isFunction(node *sitter.Node) bool {
	switch node.Kind() {
	case "function_declaration", "function_expression", "generator_function_declaration", "generator_function", "arrow_function", "method_definition":
		return true
	default:
		return false
	}
}

func decision(node *sitter.Node, source []byte) int {
	switch node.Kind() {
	case "if_statement", "for_statement", "for_in_statement", "while_statement", "do_statement", "catch_clause", "switch_case", "ternary_expression":
		return 1
	case "binary_expression":
		operator := node.ChildByFieldName("operator")
		if operator != nil {
			switch operator.Utf8Text(source) {
			case "&&", "||", "??":
				return 1
			}
		}
	}
	return 0
}

func name(node *sitter.Node, source []byte) string {
	if own := node.ChildByFieldName("name"); own != nil {
		return trimName(own.Utf8Text(source))
	}
	for parent, depth := node.Parent(), 0; parent != nil && depth < 5; parent, depth = parent.Parent(), depth+1 {
		switch parent.Kind() {
		case "variable_declarator":
			if target := parent.ChildByFieldName("name"); target != nil {
				return trimName(target.Utf8Text(source))
			}
		case "assignment_expression", "augmented_assignment_expression":
			if target := parent.ChildByFieldName("left"); target != nil {
				return trimName(target.Utf8Text(source))
			}
		case "pair", "public_field_definition":
			if target := parent.ChildByFieldName("key"); target != nil {
				return trimName(target.Utf8Text(source))
			}
			if target := parent.ChildByFieldName("name"); target != nil {
				return trimName(target.Utf8Text(source))
			}
		case "call_expression", "new_expression":
			if callee := parent.ChildByFieldName("function"); callee != nil {
				return "cb:" + compact(callee.Utf8Text(source))
			}
		}
	}
	return "(anonymous)"
}

func trimName(value string) string {
	return strings.TrimSpace(strings.Trim(value, "\"'`"))
}

func compact(value string) string {
	return strings.Join(strings.Fields(value), "")
}
