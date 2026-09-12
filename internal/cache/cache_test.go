package cache

import (
	"os"
	"reflect"
	"testing"

	"github.com/SolenesInc/slopradar/internal/lang"
	"github.com/SolenesInc/slopradar/internal/model"
)

func TestStoreKeysDialectAndVersionAndRebindsEveryFunctionPath(t *testing.T) {
	root := t.TempDir()
	store := New(root, "one")
	function := model.NewFunction("old.ts", "outer", 1, 2, 3, []model.Function{model.NewFunction("old.ts", "inner", 2, 1, 1, nil)})
	result := lang.Result{
		Functions:       []model.Function{function},
		FunctionBuckets: []model.Bucket{model.Source},
		Comments:        []lang.Span{},
		Tokens:          []lang.Token{{Text: "value", Line: 1, Bucket: model.Source}},
		TestSpans:       []lang.Span{},
		Warnings:        []string{},
	}
	store.Put("blob", "typescript:ts", result)
	got, ok := store.Get("blob", "typescript:ts", "new.ts")
	if !ok {
		t.Fatal("cache miss")
	}
	want := result
	want.Functions = cloneFunctions(result.Functions)
	rebindFunctions(want.Functions, "new.ts")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("cached result = %#v, want %#v", got, want)
	}
	if _, ok := store.Get("blob", "typescript:tsx", "new.tsx"); ok {
		t.Fatal("TypeScript cache entry reused for TSX")
	}
	if _, ok := New(root, "two").Get("blob", "typescript:ts", "new.ts"); ok {
		t.Fatal("cache entry reused across analyzer versions")
	}
}

func TestStoreRecomputesCorruptEntriesAndDoesNotStoreWarnings(t *testing.T) {
	store := New(t.TempDir(), "one")
	if err := os.WriteFile(store.path("corrupt", "go"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.Get("corrupt", "go", "source.go"); ok {
		t.Fatal("corrupt cache entry loaded")
	}
	store.Put("warning", "go", lang.Result{Warnings: []string{"parse old.go: recoverable syntax"}})
	if _, err := os.Stat(store.path("warning", "go")); !os.IsNotExist(err) {
		t.Fatalf("warning cache entry stat error = %v", err)
	}
}
