package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/SolenesInc/slopradar/internal/gitread"
	"github.com/SolenesInc/slopradar/internal/model"
	"github.com/SolenesInc/slopradar/internal/report"
	"github.com/SolenesInc/slopradar/internal/scan"
	trendcalc "github.com/SolenesInc/slopradar/internal/trend"
)

type trendOptions struct {
	merges   int
	months   int
	format   string
	useCache bool
}

func runTrend(ctx context.Context, args []string, output io.Writer) error {
	options, err := parseTrendArgs(args)
	if err != nil {
		return err
	}
	repository, err := gitread.Open(".")
	if err != nil {
		return err
	}
	head, err := repository.ResolveRevision(ctx, "HEAD")
	if err != nil {
		return err
	}
	var commits []gitread.Commit
	if options.merges != 0 {
		commits, err = trendcalc.Merges(ctx, repository, head, options.merges)
	} else {
		commits, err = trendcalc.Months(ctx, repository, head, options.months)
	}
	if err != nil {
		return err
	}
	cache := cacheStore(options.useCache)
	points, err := trendcalc.Build(ctx, commits, func(ctx context.Context, rev string) (model.Snapshot, error) {
		return scan.RevisionWithCache(ctx, ".", rev, cache)
	})
	if err != nil {
		return err
	}
	return report.WriteTrend(output, options.format, points, report.ColorEnabled(output))
}

func parseTrendArgs(args []string) (trendOptions, error) {
	options := trendOptions{format: "text", useCache: true}
	for i := 0; i < len(args); i++ {
		argument := args[i]
		switch {
		case argument == "--merges":
			if i+1 == len(args) {
				return trendOptions{}, errors.New("--merges needs a count")
			}
			i++
			merges, err := positiveInt("merges", args[i])
			if err != nil {
				return trendOptions{}, err
			}
			options.merges = merges
		case strings.HasPrefix(argument, "--merges="):
			var err error
			options.merges, err = positiveInt("merges", strings.TrimPrefix(argument, "--merges="))
			if err != nil {
				return trendOptions{}, err
			}
		case argument == "--months":
			if i+1 == len(args) {
				return trendOptions{}, errors.New("--months needs a count")
			}
			i++
			var err error
			options.months, err = positiveInt("months", args[i])
			if err != nil {
				return trendOptions{}, err
			}
		case strings.HasPrefix(argument, "--months="):
			var err error
			options.months, err = positiveInt("months", strings.TrimPrefix(argument, "--months="))
			if err != nil {
				return trendOptions{}, err
			}
		case argument == "--format":
			if i+1 == len(args) {
				return trendOptions{}, errors.New("--format needs md, json, or text")
			}
			i++
			options.format = args[i]
		case strings.HasPrefix(argument, "--format="):
			options.format = strings.TrimPrefix(argument, "--format=")
		case argument == "--no-cache":
			options.useCache = false
		case strings.HasPrefix(argument, "-"):
			return trendOptions{}, fmt.Errorf("unknown trend option %q", argument)
		default:
			return trendOptions{}, fmt.Errorf("trend accepts only options, got %q", argument)
		}
	}
	if (options.merges == 0) == (options.months == 0) {
		return trendOptions{}, errors.New("trend requires exactly one of --merges <n> or --months <n>")
	}
	if err := report.ValidateFormat(options.format); err != nil {
		return trendOptions{}, err
	}
	return options, nil
}
