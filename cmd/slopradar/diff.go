package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	diffcalc "github.com/SolenesInc/slopradar/internal/diff"
	"github.com/SolenesInc/slopradar/internal/gitread"
	"github.com/SolenesInc/slopradar/internal/model"
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
	result, err := diffcalc.Build(baseSnapshot, headSnapshot, touched)
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
	if options.format == "json" {
		return writeJSON(output, result)
	}
	writeDiffText(output, result)
	return nil
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
				return diffOptions{}, errors.New("--format needs json or text")
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
	if err := scan.ValidateFormat(options.format); err != nil {
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

func writeJSON(output io.Writer, value any) error {
	encoder := json.NewEncoder(output)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func writeDiffText(output io.Writer, result model.Diff) {
	fmt.Fprintln(output, "base\t"+result.Base)
	fmt.Fprintln(output, "head\t"+result.Head)
	fmt.Fprintln(output, "bucket\tmass_added_over_cc_10\tmass_removed_over_cc_10\tclone_lines_touched_before\tclone_lines_touched_after")
	for _, bucket := range []model.Bucket{model.Source, model.Tests} {
		delta := result.Buckets[bucket]
		fmt.Fprintf(output, "%s\t+%.6f\t-%.6f\t%d\t%d\n", bucket, delta.MassAddedOverCC10, delta.MassRemovedOverCC10, delta.CloneLinesTouchedBefore, delta.CloneLinesTouchedAfter)
	}
	fmt.Fprintf(output, "clone_pairs\tadded=%d\tremoved=%d\n", len(result.ClonesAdded), len(result.ClonesRemoved))
	fmt.Fprintln(output, "file\tname\tcc_before\tcc_after\tsloc_before\tsloc_after\tdelta_mass\tnote")
	for _, function := range result.Functions {
		fmt.Fprintf(output, "%s\t%s\t%s\t%s\t%s\t%s\t%+.6f\t%s\n",
			function.File, function.Name, functionMetric(function.Before, func(item *model.Function) int { return item.CC }), functionMetric(function.After, func(item *model.Function) int { return item.CC }),
			functionMetric(function.Before, func(item *model.Function) int { return item.SLOC }), functionMetric(function.After, func(item *model.Function) int { return item.SLOC }), function.DeltaMass, function.Note)
	}
	fmt.Fprintln(output, "repository\tbucket\terosion_before\terosion_after\tclone_share_before\tclone_share_after")
	for _, bucket := range []model.Bucket{model.Source, model.Tests} {
		delta := result.Buckets[bucket]
		fmt.Fprintf(output, "repository\t%s\t%.6f\t%.6f\t%.6f\t%.6f\n", bucket, delta.ErosionBefore, delta.ErosionAfter, delta.CloneShareBefore, delta.CloneShareAfter)
	}
}

func functionMetric(function *model.Function, metric func(*model.Function) int) string {
	if function == nil {
		return "·"
	}
	return strconv.Itoa(metric(function))
}
