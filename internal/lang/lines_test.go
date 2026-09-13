package lang

import (
	"reflect"
	"strings"
	"testing"

	"github.com/SolenesInc/slopradar/internal/model"
)

func TestSourceLinesPartitionNonCommentCodeBySpan(t *testing.T) {
	source := "prod test\n  test  \n/*prod*/ test\nprod /*test*/\n\u2003test\n"
	var tests, comments []Span
	for offset := 0; offset < len(source); {
		next := strings.Index(source[offset:], "test")
		if next < 0 {
			break
		}
		start := offset + next
		tests = append(tests, Span{StartByte: start, EndByte: start + len("test")})
		offset = start + len("test")
	}
	for _, comment := range []string{"/*prod*/", "/*test*/"} {
		start := strings.Index(source, comment)
		comments = append(comments, Span{StartByte: start, EndByte: start + len(comment)})
	}
	tests = append(tests, tests[0])
	want := map[model.Bucket][]int{model.Source: {1, 4}, model.Tests: {1, 2, 3, 5}}
	if got := SourceLines([]byte(source), comments, tests); !reflect.DeepEqual(got, want) {
		t.Fatalf("source lines = %v, want %v", got, want)
	}
}
