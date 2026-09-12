package report

import (
	"fmt"
	"io"

	"github.com/SolenesInc/slopradar/internal/model"
)

func writeSnapshotText(output io.Writer, snapshot model.Snapshot, color bool) error {
	w := newTextWriter(output)
	w.line("revision\t%s", safeText(snapshot.Rev))
	w.line("bucket\tfunctions\tmass\tmass over CC 10\terosion\tsource lines\tclone lines\tclone share")
	for _, bucket := range buckets() {
		totals := snapshot.Buckets[bucket]
		w.line("%s\t%d\t%.3f\t%.3f\t%.3f\t%d\t%d\t%.3f", bucket, totals.Functions, totals.Mass, totals.MassOverCC10, totals.Erosion, totals.SourceLines, totals.CloneLines, totals.CloneShare)
	}
	w.line("")
	w.line("clone pairs")
	w.line("id\tfirst location\tsecond location\ttokens\tlines")
	for _, pair := range snapshot.Clones {
		w.line("%s\t%s\t%s\t%d\t%d", safeText(pair.ID), textLocation(pair.A), textLocation(pair.B), pair.Tokens, pair.Lines)
	}
	w.line("")
	w.line("functions")
	w.line("file\tline\tbucket\tname\tCC\tSLOC\tmass")
	for _, function := range snapshot.Functions {
		writeTextFunction(w, function, "")
	}
	for _, skipped := range snapshot.SkippedDetails {
		w.line("skipped\t%s\tmax_file_bytes=%d\tasked_bytes=%d", safeText(skipped.File), skipped.MaxBytes, skipped.AskedBytes)
	}
	for _, warning := range snapshot.Warnings {
		w.line("warning\t%s", safeText(warning))
	}
	return w.flush()
}

func writeTextFunction(w *textWriter, function model.Function, prefix string) {
	w.line("%s\t%d\t%s\t%s%s\t%d\t%d\t%.3f", safeText(function.File), function.Line, function.Bucket, prefix, safeText(function.Name), function.CC, function.SLOC, function.Mass)
	for _, nested := range function.Nested {
		writeTextFunction(w, nested, prefix+">")
	}
}

func writeDiffText(output io.Writer, result model.Diff, color bool) error {
	w := newTextWriter(output)
	w.line("base\t%s", safeText(result.Base))
	w.line("head\t%s", safeText(result.Head))
	w.line("bucket\tmass added over CC 10\tmass removed over CC 10\tclone lines touched before\tclone lines touched after\terosion before\terosion after\tclone share before\tclone share after")
	for _, bucket := range buckets() {
		delta := result.Buckets[bucket]
		added := colorize(color, "32", fmt.Sprintf("+%.3f", delta.MassAddedOverCC10))
		removed := colorize(color, "31", fmt.Sprintf("-%.3f", delta.MassRemovedOverCC10))
		w.line("%s\t%s\t%s\t%d\t%d\t%.3f\t%.3f\t%.3f\t%.3f", bucket, added, removed, delta.CloneLinesTouchedBefore, delta.CloneLinesTouchedAfter, delta.ErosionBefore, delta.ErosionAfter, delta.CloneShareBefore, delta.CloneShareAfter)
	}
	w.line("")
	w.line("functions")
	w.line("file\tname\tCC before\tCC after\tSLOC before\tSLOC after\tmass delta\tnote")
	for _, function := range result.Functions {
		delta := fmt.Sprintf("%+.3f", function.DeltaMass)
		if function.DeltaMass > 0 {
			delta = colorize(color, "32", delta)
		} else if function.DeltaMass < 0 {
			delta = colorize(color, "31", delta)
		}
		w.line("%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s", safeText(function.File), safeText(function.Name), metric(function.Before, func(item *model.Function) int { return item.CC }), metric(function.After, func(item *model.Function) int { return item.CC }), metric(function.Before, func(item *model.Function) int { return item.SLOC }), metric(function.After, func(item *model.Function) int { return item.SLOC }), delta, safeText(function.Note))
	}
	w.line("")
	w.line("clone pairs added\t%d", len(result.ClonesAdded))
	w.line("first location\tsecond location\ttokens\tlines")
	for _, pair := range result.ClonesAdded {
		w.line("%s\t%s\t%d\t%d", textLocation(pair.A), textLocation(pair.B), pair.Tokens, pair.Lines)
	}
	w.line("clone pairs removed\t%d", len(result.ClonesRemoved))
	w.line("first location\tsecond location\ttokens\tlines")
	for _, pair := range result.ClonesRemoved {
		w.line("%s\t%s\t%d\t%d", textLocation(pair.A), textLocation(pair.B), pair.Tokens, pair.Lines)
	}
	if len(result.Trend) != 0 {
		w.line("")
		writeTrendRows(w, result.Trend)
	}
	return w.flush()
}

func writeTrendText(output io.Writer, points []model.TrendPoint) error {
	w := newTextWriter(output)
	writeTrendRows(w, points)
	return w.flush()
}

func writeTrendRows(w *textWriter, points []model.TrendPoint) {
	w.line("date\trevision\tbucket\terosion\tclone share")
	for _, point := range points {
		for _, bucket := range buckets() {
			totals := point.Buckets[bucket]
			w.line("%s\t%s\t%s\t%.3f\t%.3f", safeText(point.Date), safeText(point.Rev), bucket, totals.Erosion, totals.CloneShare)
		}
	}
}

func textLocation(location model.Range) string {
	return fmt.Sprintf("%s:%d-%d", safeText(location.File), location.Start, location.End)
}
