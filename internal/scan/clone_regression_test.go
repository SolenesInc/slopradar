package scan

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/SolenesInc/slopradar/internal/clones"
	"github.com/SolenesInc/slopradar/internal/gitread"
	"github.com/SolenesInc/slopradar/internal/model"
)

func TestCloneEligibilityIgnoresCommentAndBlankLines(t *testing.T) {
	function := []string{
		"func %s(value int) int { first := value + 10; second := first + 20;",
		"third := second + 30; fourth := third + 40;",
		"fifth := fourth + 50; sixth := fifth + 60;",
		"return sixth + first + second + third + fourth + fifth }",
	}
	var positiveID string
	var positiveTotals model.Totals
	for _, variant := range []struct {
		separator string
		extraLine bool
	}{
		{"\n", false},
		{"\n// explanation\n\n/* more explanation */\n", false},
		{"\n", true},
		{"\n// explanation\n\n/* more explanation */\n", true},
	} {
		body := strings.Join(function, variant.separator)
		if variant.extraLine {
			body = strings.Replace(body, "; second", ";\nsecond", 1)
		}
		blobs := make([]gitread.Blob, 0, 2)
		for _, name := range []string{"alpha", "beta"} {
			content := []byte("package fixture\n\n" + fmt.Sprintf(body, name) + "\n")
			blobs = append(blobs, gitread.Blob{
				BlobInfo: gitread.BlobInfo{Path: name + ".go", Size: int64(len(content))},
				Content:  content,
			})
		}
		snapshot, err := Blobs("fixture", blobs)
		if err != nil {
			t.Fatal(err)
		}
		if !variant.extraLine {
			if len(snapshot.Clones) != 0 {
				t.Fatalf("four source lines qualified with separator %q: %#v", variant.separator, snapshot.Clones)
			}
			continue
		}
		if len(snapshot.Clones) != 1 {
			t.Fatalf("five source lines did not produce one clone: %#v", snapshot.Clones)
		}
		if positiveID == "" {
			positiveID = snapshot.Clones[0].ID
			positiveTotals = snapshot.Buckets[model.Source]
		} else if snapshot.Clones[0].ID != positiveID || snapshot.Buckets[model.Source] != positiveTotals {
			t.Fatalf("comment-only lines changed clone identity or coverage: %#v", snapshot)
		}
	}
}

func TestBlobsHandlesRepetitiveJavaScript(t *testing.T) {
	tokenCount := clones.JscpdDefaultMinimumTokens * clones.JscpdDefaultMinimumTokens
	source := []byte(strings.Repeat(";\n", tokenCount))
	blobs := []gitread.Blob{{
		BlobInfo: gitread.BlobInfo{Path: "repeated.js", Size: int64(len(source))},
		Content:  source,
	}}
	first, err := Blobs("repetitive", blobs)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Blobs("repetitive", blobs)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("repetitive clone output is not deterministic")
	}
	firstNonOverlappingShift := (tokenCount + 1) / 2
	lastEligibleShift := tokenCount - clones.JscpdDefaultMinimumTokens
	expectedPairs := lastEligibleShift - firstNonOverlappingShift + 1
	if len(first.Clones) != expectedPairs {
		t.Fatalf("clone pairs = %d, want %d", len(first.Clones), expectedPairs)
	}
	if first.Buckets[model.Source].CloneLines != tokenCount {
		t.Fatalf("source clone lines = %d, want %d", first.Buckets[model.Source].CloneLines, tokenCount)
	}
}
