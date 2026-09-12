package scan

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	analysiscache "github.com/SolenesInc/slopradar/internal/cache"
	"github.com/SolenesInc/slopradar/internal/clones"
	"github.com/SolenesInc/slopradar/internal/gitread"
	"github.com/SolenesInc/slopradar/internal/lang"
	"github.com/SolenesInc/slopradar/internal/lang/golang"
	"github.com/SolenesInc/slopradar/internal/lang/python"
	"github.com/SolenesInc/slopradar/internal/lang/rust"
	"github.com/SolenesInc/slopradar/internal/lang/typescript"
	"github.com/SolenesInc/slopradar/internal/model"
)

type analyzer struct {
	language string
	dialect  string
	run      func(string, []byte) (lang.Result, error)
}

func Directory(root string) (model.Snapshot, error) {
	config, err := directoryConfig(root)
	if err != nil {
		return model.Snapshot{}, err
	}
	skipped := []model.SkippedFile{}
	filter := func(file string, size int64, directory bool) bool {
		classificationPath := file
		if directory {
			classificationPath += "/file"
		}
		if model.Classify(classificationPath, nil, config).Excluded {
			return false
		}
		if directory {
			return true
		}
		if _, ok := analyzerFor(file); !ok {
			return false
		}
		if size > model.MaxFileBytes {
			skipped = append(skipped, model.SkippedFile{File: file, MaxBytes: model.MaxFileBytes, AskedBytes: size})
			return false
		}
		return true
	}
	blobs, err := gitread.ReadDirectoryFiltered(root, filter)
	if err != nil {
		return model.Snapshot{}, err
	}
	return blobsWithConfig("directory", blobs, config, skipped, nil)
}

func Revision(ctx context.Context, root, rev string) (model.Snapshot, error) {
	return RevisionWithCache(ctx, root, rev, nil)
}

func RevisionWithCache(ctx context.Context, root, rev string, cache *analysiscache.Store) (model.Snapshot, error) {
	repository, err := gitread.Open(root)
	if err != nil {
		return model.Snapshot{}, err
	}
	resolved, err := repository.ResolveRevision(ctx, rev)
	if err != nil {
		return model.Snapshot{}, err
	}
	infos, err := repository.ListTree(ctx, resolved)
	if err != nil {
		return model.Snapshot{}, err
	}
	config, err := revisionConfig(ctx, repository, infos)
	if err != nil {
		return model.Snapshot{}, err
	}
	selected := make([]gitread.BlobInfo, 0, len(infos))
	skipped := []model.SkippedFile{}
	for _, info := range infos {
		if model.Classify(info.Path, nil, config).Excluded {
			continue
		}
		if _, ok := analyzerFor(info.Path); !ok {
			continue
		}
		if info.Size > model.MaxFileBytes {
			skipped = append(skipped, model.SkippedFile{File: info.Path, MaxBytes: model.MaxFileBytes, AskedBytes: info.Size})
			continue
		}
		selected = append(selected, info)
	}
	blobs, err := repository.ReadBlobs(ctx, selected)
	if err != nil {
		return model.Snapshot{}, err
	}
	return blobsWithConfig(resolved, blobs, config, skipped, cache)
}

func Blobs(rev string, blobs []gitread.Blob) (model.Snapshot, error) {
	blobs = append([]gitread.Blob(nil), blobs...)
	sort.Slice(blobs, func(i, j int) bool { return blobs[i].Path < blobs[j].Path })
	config, err := configFrom(blobs)
	if err != nil {
		return model.Snapshot{}, err
	}
	return blobsWithConfig(rev, blobs, config, nil, nil)
}

func blobsWithConfig(rev string, blobs []gitread.Blob, config model.Config, skipped []model.SkippedFile, cache *analysiscache.Store) (model.Snapshot, error) {
	blobs = append([]gitread.Blob(nil), blobs...)
	sort.Slice(blobs, func(i, j int) bool { return blobs[i].Path < blobs[j].Path })
	snapshot := model.Snapshot{
		Rev: rev, Functions: []model.Function{}, Clones: []model.ClonePair{}, CloneCoverage: []model.CloneCoverage{},
		Buckets: map[model.Bucket]model.Totals{model.Source: {}, model.Tests: {}}, Skipped: []string{}, SkippedDetails: append([]model.SkippedFile(nil), skipped...), Warnings: []string{},
	}
	for _, item := range skipped {
		snapshot.Skipped = append(snapshot.Skipped, item.File)
	}
	functions := map[model.Bucket][]model.Function{model.Source: {}, model.Tests: {}}
	cloneFiles := []clones.File{}
	for _, blob := range blobs {
		classification := model.Classify(blob.Path, blob.Content, config)
		if classification.Excluded || classification.Generated {
			continue
		}
		if blob.Size > model.MaxFileBytes {
			snapshot.Skipped = append(snapshot.Skipped, blob.Path)
			snapshot.SkippedDetails = append(snapshot.SkippedDetails, model.SkippedFile{File: blob.Path, MaxBytes: model.MaxFileBytes, AskedBytes: blob.Size})
			continue
		}
		analyze, ok := analyzerFor(blob.Path)
		if !ok {
			continue
		}
		result, found := cache.Get(blob.OID, analyze.dialect, blob.Path)
		if !found {
			analyzed, err := analyze.run(blob.Path, blob.Content)
			if err != nil {
				return model.Snapshot{}, err
			}
			result = analyzed
			cache.Put(blob.OID, analyze.dialect, result)
		}
		snapshot.Warnings = append(snapshot.Warnings, result.Warnings...)
		sourceLines := lang.SourceLines(blob.Content, result.Comments, result.TestSpans)
		if classification.Bucket == model.Tests {
			sourceLines[model.Tests] = append(sourceLines[model.Tests], sourceLines[model.Source]...)
			sort.Ints(sourceLines[model.Tests])
			sourceLines[model.Source] = nil
			for i := range result.Tokens {
				result.Tokens[i].Bucket = model.Tests
			}
		}
		for bucket, lines := range sourceLines {
			totals := snapshot.Buckets[bucket]
			totals.SourceLines += len(lines)
			snapshot.Buckets[bucket] = totals
		}
		cloneFiles = append(cloneFiles, clones.File{Path: blob.Path, Language: analyze.language, Tokens: result.Tokens, SourceLines: sourceLines})
		for i, function := range result.Functions {
			bucket := classification.Bucket
			if bucket == model.Source && i < len(result.FunctionBuckets) {
				bucket = result.FunctionBuckets[i]
			}
			functions[bucket] = append(functions[bucket], function)
			snapshot.Functions = append(snapshot.Functions, withBucket(function, bucket))
		}
	}
	cloneResult := clones.Detect(cloneFiles)
	snapshot.Clones = cloneResult.Pairs
	snapshot.CloneCoverage = cloneResult.Coverage
	for _, bucket := range []model.Bucket{model.Source, model.Tests} {
		totals := model.Summarize(functions[bucket])
		totals.SourceLines = snapshot.Buckets[bucket].SourceLines
		totals.CloneLines = cloneResult.Total[bucket]
		if totals.SourceLines != 0 {
			totals.CloneShare = float64(totals.CloneLines) / float64(totals.SourceLines)
		}
		snapshot.Buckets[bucket] = totals
	}
	sort.Slice(snapshot.Functions, func(i, j int) bool {
		if snapshot.Functions[i].File != snapshot.Functions[j].File {
			return snapshot.Functions[i].File < snapshot.Functions[j].File
		}
		if snapshot.Functions[i].Line != snapshot.Functions[j].Line {
			return snapshot.Functions[i].Line < snapshot.Functions[j].Line
		}
		return snapshot.Functions[i].Name < snapshot.Functions[j].Name
	})
	sort.Strings(snapshot.Skipped)
	sort.Slice(snapshot.SkippedDetails, func(i, j int) bool { return snapshot.SkippedDetails[i].File < snapshot.SkippedDetails[j].File })
	sort.Strings(snapshot.Warnings)
	return snapshot, nil
}

func withBucket(function model.Function, bucket model.Bucket) model.Function {
	function.Bucket = bucket
	for i := range function.Nested {
		function.Nested[i] = withBucket(function.Nested[i], bucket)
	}
	return function
}

func directoryConfig(root string) (model.Config, error) {
	file := filepath.Join(root, model.ConfigFile)
	info, err := os.Stat(file)
	if errors.Is(err, os.ErrNotExist) {
		return model.Config{}, nil
	}
	if err != nil {
		return model.Config{}, fmt.Errorf("inspect %s: %w", model.ConfigFile, err)
	}
	if info.Size() > model.MaxFileBytes {
		return model.Config{}, fmt.Errorf("read %s: max_file_bytes=%d, asked_bytes=%d", model.ConfigFile, model.MaxFileBytes, info.Size())
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return model.Config{}, fmt.Errorf("read %s: %w", model.ConfigFile, err)
	}
	return model.ParseConfig(data)
}

func revisionConfig(ctx context.Context, repository *gitread.Repository, infos []gitread.BlobInfo) (model.Config, error) {
	for _, info := range infos {
		if info.Path != model.ConfigFile {
			continue
		}
		if info.Size > model.MaxFileBytes {
			return model.Config{}, fmt.Errorf("read %s: max_file_bytes=%d, asked_bytes=%d", model.ConfigFile, model.MaxFileBytes, info.Size)
		}
		blobs, err := repository.ReadBlobs(ctx, []gitread.BlobInfo{info})
		if err != nil {
			return model.Config{}, err
		}
		return model.ParseConfig(blobs[0].Content)
	}
	return model.Config{}, nil
}

func configFrom(blobs []gitread.Blob) (model.Config, error) {
	for _, blob := range blobs {
		if path.Clean(strings.ReplaceAll(blob.Path, "\\", "/")) == model.ConfigFile {
			asked := max(blob.Size, int64(len(blob.Content)))
			if asked > model.MaxFileBytes {
				return model.Config{}, fmt.Errorf("read %s: max_file_bytes=%d, asked_bytes=%d", model.ConfigFile, model.MaxFileBytes, asked)
			}
			return model.ParseConfig(blob.Content)
		}
	}
	return model.Config{}, nil
}

func analyzerFor(file string) (analyzer, bool) {
	lower := strings.ToLower(file)
	extension := path.Ext(lower)
	switch extension {
	case ".go":
		return analyzer{language: "go", dialect: "go", run: golang.Analyze}, true
	case ".ts", ".tsx", ".mts", ".cts", ".js", ".jsx", ".mjs", ".cjs":
		dialect := "typescript:" + strings.TrimPrefix(extension, ".")
		for _, suffix := range []string{".d.ts", ".d.mts", ".d.cts"} {
			if strings.HasSuffix(lower, suffix) {
				dialect = "typescript:" + strings.TrimPrefix(suffix, ".")
				break
			}
		}
		return analyzer{language: "typescript", dialect: dialect, run: typescript.Analyze}, true
	case ".py":
		return analyzer{language: "python", dialect: "python", run: python.Analyze}, true
	case ".rs":
		return analyzer{language: "rust", dialect: "rust", run: rust.Analyze}, true
	default:
		return analyzer{}, false
	}
}

func ValidateFormat(format string) error {
	if format != "json" && format != "text" {
		return fmt.Errorf("format must be json or text, got %q", format)
	}
	return nil
}
