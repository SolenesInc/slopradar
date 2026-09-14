package scan

import (
	"testing"

	"github.com/SolenesInc/slopradar/internal/gitread"
)

func TestGeneratedMarkerInTemplateLiteralKeepsHandwrittenFunctions(t *testing.T) {
	content := []byte("export const fixture = `\n// @generated\n`;\nexport function handwritten(value: boolean) {\nif (value) return 1;\nreturn 0;\n}\n")
	snapshot, err := Blobs("fixture", []gitread.Blob{{
		BlobInfo: gitread.BlobInfo{Path: "example.ts", Size: int64(len(content))},
		Content:  content,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Functions) != 1 || snapshot.Functions[0].Name != "handwritten" || snapshot.Functions[0].CC != 2 {
		t.Fatalf("handwritten function was omitted: %#v", snapshot.Functions)
	}
}
