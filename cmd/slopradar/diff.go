package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	diffcalc "github.com/SolenesInc/slopradar/internal/diff"
	"github.com/SolenesInc/slopradar/internal/gitread"
	"github.com/SolenesInc/slopradar/internal/model"
	"github.com/SolenesInc/slopradar/internal/report"
	"github.com/SolenesInc/slopradar/internal/scan"
	trendcalc "github.com/SolenesInc/slopradar/internal/trend"
)

type diffOptions struct {
	base        string
	head        string
	format      string
	trendMonths int
	useCache    bool
}

func runDiff(ctx context.Context, args []string, output io.Writer) error {
	options, err := parseDiffArgs(args)
	if err != nil {
		return err
	}
	repository, err := gitread.Open(".")
	if err != nil {
		return err
	}
	base, err := repository.MergeBase(ctx, options.base, options.head)
	if err != nil {
		return err
	}
	head, err := repository.ResolveRevision(ctx, options.head)
	if err != nil {
		return err
	}
	touched, err := repository.ChangedFiles(ctx, base, head)
	if err != nil {
		return err
	}
	cache := cacheStore(options.useCache)
	baseSnapshot, err := scan.RevisionWithCache(ctx, ".", base, cache)
	if err != nil {
		return err
	}
	headSnapshot, err := scan.RevisionWithCache(ctx, ".", head, cache)
	if err != nil {
		return err
	}
	if err := model.ValidateComplete(baseSnapshot); err != nil {
		return fmt.Errorf("base snapshot: %w", err)
	}
	if err := model.ValidateComplete(headSnapshot); err != nil {
		return fmt.Errorf("head snapshot: %w", err)
	}
	lineChanges, err := repository.LineChanges(ctx, base, head, cloneMappingPaths(baseSnapshot, headSnapshot, touched))
	if err != nil {
		return err
	}
	result, err := diffcalc.BuildWithLineChanges(baseSnapshot, headSnapshot, touched, lineChanges)
	if err != nil {
		return err
	}
	if options.trendMonths != 0 {
		commits, err := trendcalc.Months(ctx, repository, head, options.trendMonths)
		if err != nil {
			return err
		}
		result.Trend, err = trendcalc.Build(ctx, commits, func(ctx context.Context, rev string) (model.Snapshot, error) {
			return scan.RevisionWithCache(ctx, ".", rev, cache)
		})
		if err != nil {
			return err
		}
	}
	return report.WriteDiff(output, options.format, result, report.ColorEnabled(output))
}

func cloneMappingPaths(base, head model.Snapshot, touched []string) []string {
	before := make(map[string]bool, len(base.AnalysisPaths))
	for _, path := range base.AnalysisPaths {
		before[path.File] = path.GitLineCoordinates
	}
	compatible := make(map[string]bool, len(head.AnalysisPaths))
	for _, path := range head.AnalysisPaths {
		compatible[path.File] = before[path.File] && path.GitLineCoordinates
	}
	cloneFiles := make(map[string]struct{}, len(base.Clones)+len(head.Clones))
	for _, pairs := range [][]model.ClonePair{base.Clones, head.Clones} {
		for _, pair := range pairs {
			cloneFiles[pair.A.File] = struct{}{}
			cloneFiles[pair.B.File] = struct{}{}
		}
	}
	touchedFiles := make(map[string]struct{}, len(touched))
	for _, file := range touched {
		touchedFiles[file] = struct{}{}
	}
	paths := make([]string, 0, len(cloneFiles))
	for file := range cloneFiles {
		_, isTouched := touchedFiles[file]
		if compatible[file] && isTouched {
			paths = append(paths, file)
		}
	}
	sort.Strings(paths)
	return paths
}

func parseDiffArgs(args []string) (diffOptions, error) {
	options := diffOptions{format: "text", useCache: true}
	for i := 0; i < len(args); i++ {
		argument := args[i]
		switch {
		case argument == "--base":
			if i+1 == len(args) {
				return diffOptions{}, errors.New("--base needs a revision")
			}
			i++
			options.base = args[i]
		case strings.HasPrefix(argument, "--base="):
			options.base = strings.TrimPrefix(argument, "--base=")
		case argument == "--head":
			if i+1 == len(args) {
				return diffOptions{}, errors.New("--head needs a revision")
			}
			i++
			options.head = args[i]
		case strings.HasPrefix(argument, "--head="):
			options.head = strings.TrimPrefix(argument, "--head=")
		case argument == "--format":
			if i+1 == len(args) {
				return diffOptions{}, errors.New("--format needs md, json, or text")
			}
			i++
			options.format = args[i]
		case strings.HasPrefix(argument, "--format="):
			options.format = strings.TrimPrefix(argument, "--format=")
		case argument == "--trend":
			if i+1 == len(args) {
				return diffOptions{}, errors.New("--trend needs a number of months")
			}
			i++
			months, err := positiveInt("trend months", args[i])
			if err != nil {
				return diffOptions{}, err
			}
			options.trendMonths = months
		case strings.HasPrefix(argument, "--trend="):
			months, err := positiveInt("trend months", strings.TrimPrefix(argument, "--trend="))
			if err != nil {
				return diffOptions{}, err
			}
			options.trendMonths = months
		case argument == "--no-cache":
			options.useCache = false
		case strings.HasPrefix(argument, "-"):
			return diffOptions{}, fmt.Errorf("unknown diff option %q", argument)
		default:
			return diffOptions{}, fmt.Errorf("diff accepts only options, got %q", argument)
		}
	}
	if options.base == "" || options.head == "" {
		return diffOptions{}, errors.New("diff requires --base <rev> and --head <rev>")
	}
	if err := report.ValidateFormat(options.format); err != nil {
		return diffOptions{}, err
	}
	return options, nil
}

func positiveInt(name, value string) (int, error) {
	number, err := strconv.Atoi(value)
	if err != nil || number <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer, got %q", name, value)
	}
	return number, nil
}
