package rust

import (
	"reflect"
	"testing"
)

func TestModuleMetadataKeepsPathsRelativeAndUnescapesRustStrings(t *testing.T) {
	result, err := Analyze("src/lib.rs", []byte(`
#[cfg(test)]
mod inline {
 #[path = "snow\u{2603}.rs"] mod snow;
 #[path = "escaped\x2drs.rs"] mod escaped;
 #[path = "line\
   continued.rs"] mod continued;
 #[path = r#"literal\backslash.rs"#] mod raw;
 mod r#type;
}
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Modules) != 1 || !result.Modules[0].Inline {
		t.Fatalf("modules=%#v", result.Modules)
	}
	var paths []string
	for _, m := range result.Modules[0].Children {
		if !m.TestOnly {
			t.Fatalf("lost inherited test scope: %#v", m)
		}
		if m.Path != nil {
			paths = append(paths, *m.Path)
		} else {
			paths = append(paths, m.Name)
		}
	}
	want := []string{"snow☃.rs", "escaped-rs.rs", "linecontinued.rs", `literal\backslash.rs`, "type"}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("paths=%q, want %q", paths, want)
	}
}
