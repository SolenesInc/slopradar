package scan

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/SolenesInc/slopradar/internal/gitread"
	"github.com/SolenesInc/slopradar/internal/lang"
	"github.com/SolenesInc/slopradar/internal/lang/golang"
	"github.com/SolenesInc/slopradar/internal/lang/python"
	"github.com/SolenesInc/slopradar/internal/lang/rust"
	"github.com/SolenesInc/slopradar/internal/lang/typescript"
	"github.com/SolenesInc/slopradar/internal/model"
)

type analyzer func(string, []byte) (lang.Result, error)

func Directory(root string) (model.Snapshot, error) {
	blobs, err := gitread.ReadDirectory(root)
	if err != nil {
		return model.Snapshot{}, err
	}
	return Blobs("directory", blobs)
}

func Revision(ctx context.Context, root, rev string) (model.Snapshot, error) {
	repository, err := gitread.Open(root)
	if err != nil {
		return model.Snapshot{}, err
	}
	resolved, err := repository.ResolveRevision(ctx, rev)
	if err != nil {
		return model.Snapshot{}, err
	}
	blobs, err := repository.ReadTree(ctx, resolved)
	if err != nil {
		return model.Snapshot{}, err
	}
	return Blobs(resolved, blobs)
}

func Blobs(rev string, blobs []gitread.Blob) (model.Snapshot, error) {
	blobs = append([]gitread.Blob(nil), blobs...)
	sort.Slice(blobs, func(i, j int) bool { return blobs[i].Path < blobs[j].Path })
	config, err := configFrom(blobs)
	if err != nil {
		return model.Snapshot{}, err
	}
	snapshot := model.Snapshot{
		Rev: rev, Functions: []model.Function{}, Clones: []model.ClonePair{},
		Buckets: map[model.Bucket]model.Totals{model.Source: {}, model.Tests: {}}, Skipped: []string{}, SkippedDetails: []model.SkippedFile{}, Warnings: []string{},
	}
	functions := map[model.Bucket][]model.Function{model.Source: {}, model.Tests: {}}
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
		result, err := analyze(blob.Path, blob.Content)
		if err != nil {
			return model.Snapshot{}, err
		}
		snapshot.Warnings = append(snapshot.Warnings, result.Warnings...)
		lineCounts := lang.CountLines(blob.Content, result.Comments, result.TestSpans)
		if classification.Bucket == model.Tests {
			lineCounts[model.Tests] += lineCounts[model.Source]
			lineCounts[model.Source] = 0
		}
		for bucket, count := range lineCounts {
			totals := snapshot.Buckets[bucket]
			totals.SourceLines += count
			snapshot.Buckets[bucket] = totals
		}
		for i, function := range result.Functions {
			bucket := classification.Bucket
			if bucket == model.Source && i < len(result.FunctionBuckets) {
				bucket = result.FunctionBuckets[i]
			}
			functions[bucket] = append(functions[bucket], function)
			snapshot.Functions = append(snapshot.Functions, function)
		}
	}
	for _, bucket := range []model.Bucket{model.Source, model.Tests} {
		totals := model.Summarize(functions[bucket])
		totals.SourceLines = snapshot.Buckets[bucket].SourceLines
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

func configFrom(blobs []gitread.Blob) (model.Config, error) {
	for _, blob := range blobs {
		if path.Clean(strings.ReplaceAll(blob.Path, "\\", "/")) == model.ConfigFile {
			return model.ParseConfig(blob.Content)
		}
	}
	return model.Config{}, nil
}

func analyzerFor(file string) (analyzer, bool) {
	switch strings.ToLower(path.Ext(file)) {
	case ".go":
		return golang.Analyze, true
	case ".ts", ".tsx", ".mts", ".cts", ".js", ".jsx", ".mjs", ".cjs":
		return typescript.Analyze, true
	case ".py":
		return python.Analyze, true
	case ".rs":
		return rust.Analyze, true
	default:
		return nil, false
	}
}

func ValidateFormat(format string) error {
	if format != "json" && format != "text" {
		return fmt.Errorf("format must be json or text, got %q", format)
	}
	return nil
}
