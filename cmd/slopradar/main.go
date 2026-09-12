package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	analysiscache "github.com/SolenesInc/slopradar/internal/cache"
	"github.com/SolenesInc/slopradar/internal/model"
	"github.com/SolenesInc/slopradar/internal/report"
	"github.com/SolenesInc/slopradar/internal/scan"
)

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "slopradar:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, output io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: slopradar <scan|diff|trend> [options]")
	}
	switch args[0] {
	case "scan":
		return runScan(ctx, args[1:], output)
	case "diff":
		return runDiff(ctx, args[1:], output)
	case "trend":
		return runTrend(ctx, args[1:], output)
	default:
		return fmt.Errorf("unknown command %q; usage: slopradar <scan|diff|trend> [options]", args[0])
	}
}

func runScan(ctx context.Context, args []string, output io.Writer) error {
	target, format, useCache, err := scanArgs(args)
	if err != nil {
		return err
	}
	var snapshot model.Snapshot
	if info, statErr := os.Stat(target); statErr == nil && info.IsDir() {
		snapshot, err = scan.Directory(target)
	} else {
		snapshot, err = scan.RevisionWithCache(ctx, ".", target, cacheStore(useCache))
	}
	if err != nil {
		return err
	}
	return report.WriteSnapshot(output, format, snapshot, report.ColorEnabled(output))
}

func scanArgs(args []string) (string, string, bool, error) {
	target := "HEAD"
	format := "text"
	useCache := true
	targetSet := false
	for i := 0; i < len(args); i++ {
		argument := args[i]
		switch {
		case argument == "--format":
			if i+1 == len(args) {
				return "", "", false, errors.New("--format needs md, json, or text")
			}
			i++
			format = args[i]
		case strings.HasPrefix(argument, "--format="):
			format = strings.TrimPrefix(argument, "--format=")
		case argument == "--no-cache":
			useCache = false
		case strings.HasPrefix(argument, "-"):
			return "", "", false, fmt.Errorf("unknown scan option %q", argument)
		case targetSet:
			return "", "", false, fmt.Errorf("scan accepts one revision or directory, got %q", argument)
		default:
			target = argument
			targetSet = true
		}
	}
	if err := report.ValidateFormat(format); err != nil {
		return "", "", false, err
	}
	return target, format, useCache, nil
}

func cacheStore(enabled bool) *analysiscache.Store {
	if !enabled {
		return nil
	}
	return analysiscache.User()
}
