package rust

import (
	"strconv"
	"strings"
	"unicode/utf8"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/SolenesInc/slopradar/internal/lang"
)

func modules(root *sitter.Node, source []byte) []lang.Module {
	var result []lang.Module
	collectModules(root, source, false, &result)
	return result
}

func collectModules(node *sitter.Node, source []byte, testOnly bool, result *[]lang.Module) {
	testOnly = testOnly || isTestScope(node, source)
	if node.Kind() == "mod_item" {
		module := lang.Module{Name: strings.TrimPrefix(text(node.ChildByFieldName("name"), source), "r#"), TestOnly: testOnly}
		module.Path, module.AlternatePaths, module.Uncertain = modulePaths(node, source)
		if body := node.ChildByFieldName("body"); body != nil {
			module.Inline = true
			collectModules(body, source, testOnly, &module.Children)
		}
		*result = append(*result, module)
		return
	}
	for i := uint(0); i < node.NamedChildCount(); i++ {
		collectModules(node.NamedChild(i), source, testOnly, result)
	}
}

func modulePaths(node *sitter.Node, source []byte) (direct *string, alternatives []string, uncertain bool) {
	for sibling := node.PrevNamedSibling(); sibling != nil; sibling = sibling.PrevNamedSibling() {
		switch sibling.Kind() {
		case "line_comment", "block_comment":
			continue
		case "attribute_item":
			attribute := sibling.NamedChild(0)
			if attribute == nil || attribute.NamedChildCount() == 0 {
				continue
			}
			switch text(attribute.NamedChild(0), source) {
			case "path":
				value, ok := modulePathLiteral(attribute.ChildByFieldName("value"), source)
				if !ok {
					uncertain = true
					continue
				}
				if direct != nil {
					alternatives = append(alternatives, *direct)
					uncertain = true
				}
				direct = &value
			case "cfg_attr":
				paths, found := conditionalPaths(attribute, source)
				alternatives = append(alternatives, paths...)
				uncertain = uncertain || found
			}
		default:
			return
		}
	}
	return
}

func conditionalPaths(node *sitter.Node, source []byte) ([]string, bool) {
	var paths []string
	found := false
	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		if child.Kind() == "identifier" && child.Utf8Text(source) == "path" && i+2 < node.ChildCount() && node.Child(i+1).Kind() == "=" {
			found = true
			if value, ok := modulePathLiteral(node.Child(i+2), source); ok {
				paths = append(paths, value)
			}
		}
		children, nested := conditionalPaths(child, source)
		paths = append(paths, children...)
		found = found || nested
	}
	return paths, found
}

func modulePathLiteral(node *sitter.Node, source []byte) (string, bool) {
	if node == nil {
		return "", false
	}
	value := node.Utf8Text(source)
	if node.Kind() == "raw_string_literal" {
		start, end := strings.IndexByte(value, '"'), strings.LastIndexByte(value, '"')
		if start >= 0 && end > start {
			return value[start+1 : end], true
		}
	}
	if node.Kind() != "string_literal" {
		return "", false
	}
	return decodeModulePath(value[1 : len(value)-1])
}

func decodeModulePath(value string) (string, bool) {
	var decoded strings.Builder
	for len(value) > 0 {
		if value[0] != '\\' {
			decoded.WriteByte(value[0])
			value = value[1:]
			continue
		}
		if strings.HasPrefix(value, "\\u{") {
			end := strings.IndexByte(value, '}')
			if end < 0 {
				return "", false
			}
			code, err := strconv.ParseUint(strings.ReplaceAll(value[3:end], "_", ""), 16, 32)
			if err != nil || !utf8.ValidRune(rune(code)) {
				return "", false
			}
			decoded.WriteRune(rune(code))
			value = value[end+1:]
			continue
		}
		if strings.HasPrefix(value, "\\0") {
			decoded.WriteByte(0)
			value = value[2:]
			continue
		}
		if strings.HasPrefix(value, "\\\n") || strings.HasPrefix(value, "\\\r\n") {
			value = strings.TrimLeft(value[1:], " \t\r\n")
			continue
		}
		char, multibyte, rest, err := strconv.UnquoteChar(value, '"')
		if err != nil {
			return "", false
		}
		if multibyte {
			decoded.WriteRune(char)
		} else {
			decoded.WriteByte(byte(char))
		}
		value = rest
	}
	result := decoded.String()
	return result, utf8.ValidString(result)
}
