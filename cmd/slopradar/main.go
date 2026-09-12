package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/SolenesInc/slopradar/internal/model"
	"github.com/SolenesInc/slopradar/internal/scan"
)

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "slopradar:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, output io.Writer) error {
	if len(args) == 0 || args[0] != "scan" {
		return errors.New("usage: slopradar scan [<rev>|<dir>] --format json|text")
	}
	target, format, err := scanArgs(args[1:])
	if err != nil {
		return err
	}
	var snapshot model.Snapshot
	if info, statErr := os.Stat(target); statErr == nil && info.IsDir() {
		snapshot, err = scan.Directory(target)
	} else {
		snapshot, err = scan.Revision(ctx, ".", target)
	}
	if err != nil {
		return err
	}
	if format == "json" {
		encoder := json.NewEncoder(output)
		encoder.SetEscapeHTML(false)
		encoder.SetIndent("", "  ")
		return encoder.Encode(snapshot)
	}
	writeText(output, snapshot)
	return nil
}

func scanArgs(args []string) (string, string, error) {
	target := "HEAD"
	format := "text"
	targetSet := false
	for i := 0; i < len(args); i++ {
		argument := args[i]
		switch {
		case argument == "--format":
			if i+1 == len(args) {
				return "", "", errors.New("--format needs json or text")
			}
			i++
			format = args[i]
		case strings.HasPrefix(argument, "--format="):
			format = strings.TrimPrefix(argument, "--format=")
		case strings.HasPrefix(argument, "-"):
			return "", "", fmt.Errorf("unknown scan option %q", argument)
		case targetSet:
			return "", "", fmt.Errorf("scan accepts one revision or directory, got %q", argument)
		default:
			target = argument
			targetSet = true
		}
	}
	if err := scan.ValidateFormat(format); err != nil {
		return "", "", err
	}
	return target, format, nil
}

func writeText(output io.Writer, snapshot model.Snapshot) {
	fmt.Fprintln(output, "revision\t"+snapshot.Rev)
	fmt.Fprintln(output, "bucket\tfunctions\tmass\tmass_over_cc_10\terosion\tsource_lines\tclone_lines\tclone_share")
	for _, bucket := range []model.Bucket{model.Source, model.Tests} {
		totals := snapshot.Buckets[bucket]
		fmt.Fprintf(output, "%s\t%d\t%.6f\t%.6f\t%.6f\t%d\t%d\t%.6f\n", bucket, totals.Functions, totals.Mass, totals.MassOverCC10, totals.Erosion, totals.SourceLines, totals.CloneLines, totals.CloneShare)
	}
	fmt.Fprintln(output, "file\tline\tname\tcc\tsloc\tmass")
	for _, function := range snapshot.Functions {
		writeFunction(output, function, "")
	}
	for _, skipped := range snapshot.SkippedDetails {
		fmt.Fprintf(output, "skipped\t%s\tmax_file_bytes=%s\tasked_bytes=%s\n", skipped.File, strconv.FormatInt(skipped.MaxBytes, 10), strconv.FormatInt(skipped.AskedBytes, 10))
	}
	for _, warning := range snapshot.Warnings {
		fmt.Fprintln(output, "warning\t"+warning)
	}
}

func writeFunction(output io.Writer, function model.Function, prefix string) {
	fmt.Fprintf(output, "%s\t%d\t%s%s\t%d\t%d\t%.6f\n", function.File, function.Line, prefix, function.Name, function.CC, function.SLOC, function.Mass)
	for _, nested := range function.Nested {
		writeFunction(output, nested, prefix+">")
	}
}
