package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"unicode/utf8"

	"github.com/SolenesInc/slopradar/internal/lang"
	"github.com/SolenesInc/slopradar/internal/model"
)

const AnalyzerVersion = "25"

type Store struct {
	root    string
	version string
}

func User() *Store {
	root, err := os.UserCacheDir()
	if err != nil {
		return nil
	}
	return New(filepath.Join(root, "slopradar"), AnalyzerVersion)
}

func New(root, version string) *Store {
	return &Store{root: root, version: version}
}

func (s *Store) Get(blobSHA, dialect, file string) (lang.Result, bool) {
	if s == nil || blobSHA == "" {
		return lang.Result{}, false
	}
	data, err := os.ReadFile(s.path(blobSHA, dialect))
	if err != nil {
		return lang.Result{}, false
	}
	var result lang.Result
	if err := json.Unmarshal(data, &result); err != nil {
		return lang.Result{}, false
	}
	rebindFunctions(result.Functions, file)
	return result, true
}

func (s *Store) Put(blobSHA, dialect string, result lang.Result) {
	if s == nil || blobSHA == "" || len(result.Warnings) != 0 {
		return
	}
	for _, token := range result.Tokens {
		if !utf8.ValidString(token.Text) {
			return
		}
	}
	normalized := result
	normalized.Functions = cloneFunctions(result.Functions)
	rebindFunctions(normalized.Functions, "")
	data, err := json.Marshal(normalized)
	if err != nil {
		return
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return
	}
	temporary, err := os.CreateTemp(s.root, ".analysis-*")
	if err != nil {
		return
	}
	name := temporary.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return
	}
	if err := temporary.Close(); err != nil {
		return
	}
	_ = os.Rename(name, s.path(blobSHA, dialect))
}

func (s *Store) path(blobSHA, dialect string) string {
	digest := sha256.Sum256([]byte(s.version + "\x00" + dialect + "\x00" + blobSHA))
	return filepath.Join(s.root, hex.EncodeToString(digest[:])+".json")
}

func cloneFunctions(functions []model.Function) []model.Function {
	if functions == nil {
		return nil
	}
	cloned := make([]model.Function, len(functions))
	for i, function := range functions {
		cloned[i] = function
		cloned[i].Nested = cloneFunctions(function.Nested)
	}
	return cloned
}

func rebindFunctions(functions []model.Function, file string) {
	for i := range functions {
		functions[i].File = file
		rebindFunctions(functions[i].Nested, file)
	}
}
