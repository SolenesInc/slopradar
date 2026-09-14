package rust

import sitter "github.com/tree-sitter/go-tree-sitter"

type cfgTruth int

const (
	cfgUnknown cfgTruth = iota
	cfgFalse
	cfgTrue
)

func cfgRequiresTest(arguments *sitter.Node, source []byte) bool {
	withoutTest := cfgPredicates(arguments, source, false)
	withTest := cfgPredicates(arguments, source, true)
	return len(withoutTest) == 1 && withoutTest[0] == cfgFalse && len(withTest) == 1 && withTest[0] != cfgFalse
}

func cfgPredicates(tree *sitter.Node, source []byte, testEnabled bool) []cfgTruth {
	var predicates []cfgTruth
	var parts []*sitter.Node
	for i := uint(0); i < tree.ChildCount(); i++ {
		child := tree.Child(i)
		switch child.Kind() {
		case "(", ")", "line_comment", "block_comment":
			continue
		case ",":
			predicates = append(predicates, cfgPredicate(parts, source, testEnabled))
			parts = nil
		default:
			parts = append(parts, child)
		}
	}
	if len(parts) != 0 {
		predicates = append(predicates, cfgPredicate(parts, source, testEnabled))
	}
	return predicates
}

func cfgPredicate(parts []*sitter.Node, source []byte, testEnabled bool) cfgTruth {
	if len(parts) == 0 {
		return cfgUnknown
	}
	name := parts[0].Utf8Text(source)
	if len(parts) == 1 {
		switch name {
		case "test":
			if testEnabled {
				return cfgTrue
			}
			return cfgFalse
		case "true":
			return cfgTrue
		case "false":
			return cfgFalse
		default:
			return cfgUnknown
		}
	}
	if len(parts) != 2 || parts[1].Kind() != "token_tree" {
		return cfgUnknown
	}
	children := cfgPredicates(parts[1], source, testEnabled)
	switch name {
	case "all":
		result := cfgTrue
		for _, child := range children {
			if child == cfgFalse {
				return cfgFalse
			}
			if child == cfgUnknown {
				result = cfgUnknown
			}
		}
		return result
	case "any":
		result := cfgFalse
		for _, child := range children {
			if child == cfgTrue {
				return cfgTrue
			}
			if child == cfgUnknown {
				result = cfgUnknown
			}
		}
		return result
	case "not":
		if len(children) == 1 {
			switch children[0] {
			case cfgFalse:
				return cfgTrue
			case cfgTrue:
				return cfgFalse
			}
		}
	}
	return cfgUnknown
}
