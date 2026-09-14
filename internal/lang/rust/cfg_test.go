package rust

import (
	"reflect"
	"testing"

	"github.com/SolenesInc/slopradar/internal/lang"
	"github.com/SolenesInc/slopradar/internal/model"
)

func TestCompoundCfgRequiresTestForTestBucket(t *testing.T) {
	for _, test := range []struct {
		predicate string
		bucket    model.Bucket
	}{
		{"test", model.Tests},
		{"all(test, unix)", model.Tests},
		{`all(feature = "test", test,)`, model.Tests},
		{`all(test, any(unix, feature = r#"test,not(test)"#))`, model.Tests},
		{"any(all(test, unix), all(test, windows))", model.Tests},
		{"not(not(test))", model.Tests},
		{"not(any(not(test), windows))", model.Tests},
		{"all(/* retained syntax */ test, not(windows))", model.Tests},
		{"any(test, unix)", model.Source},
		{"not(test)", model.Source},
		{`feature = "test"`, model.Source},
		{"all(unix, not(windows))", model.Source},
		{"all()", model.Source},
		{"any()", model.Source},
		{"all(test, not(test))", model.Source},
		{"any(test, not(test))", model.Source},
	} {
		t.Run(test.predicate, func(t *testing.T) {
			source := []byte("#[cfg(" + test.predicate + ")]\nfn helper() { if ready() { work(); } }\n")
			result, err := Analyze("helper.rs", source)
			if err != nil || len(result.Warnings) != 0 {
				t.Fatalf("analyze: %v, warnings: %v", err, result.Warnings)
			}
			if len(result.Functions) != 1 || !reflect.DeepEqual(result.FunctionBuckets, []model.Bucket{test.bucket}) {
				t.Fatalf("functions = %#v, buckets = %#v; want %s", result.Functions, result.FunctionBuckets, test.bucket)
			}
			for _, token := range result.Tokens {
				if token.Bucket != test.bucket {
					t.Fatalf("token = %#v; want %s", token, test.bucket)
				}
			}
			lines := lang.SourceLines(source, result.Comments, result.TestSpans)
			if !reflect.DeepEqual(lines[test.bucket], []int{1, 2}) {
				t.Fatalf("lines = %#v; want both in %s", lines, test.bucket)
			}
		})
	}
}
