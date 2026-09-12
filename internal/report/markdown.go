package report

import (
	"fmt"
	"html"
	"io"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/SolenesInc/slopradar/internal/model"
)

type markdownWriter struct {
	output io.Writer
	err    error
}

func (w *markdownWriter) line(format string, arguments ...any) {
	if w.err == nil {
		_, w.err = fmt.Fprintf(w.output, format+"\n", arguments...)
	}
}

func writeDiffMarkdown(output io.Writer, result model.Diff) error {
	w := &markdownWriter{output: output}
	w.line("<!-- slopradar -->")
	w.line("## slopradar")
	w.line("")
	writeHeadline(w, result)
	w.line("")
	writeBucketSummary(w, result)
	if len(result.Trend) != 0 {
		w.line("")
		writeSparklines(w, result.Trend)
	}
	w.line("")
	w.line("<details>")
	w.line("<summary>%d functions changed mass</summary>", len(result.Functions))
	w.line("")
	w.line("| function | bucket before → after | CC before → after | SLOC before → after | Δmass | note |")
	w.line("|---|---|---:|---:|---:|---|")
	for _, function := range result.Functions {
		w.line("| <code>%s</code> in <code>%s</code> | %s → %s | %s → %s | %s → %s | %+.3f | %s |", markdownInline(function.Name), markdownInline(function.File), functionBucketMetric(function.Before), functionBucketMetric(function.After), metric(function.Before, func(item *model.Function) int { return item.CC }), metric(function.After, func(item *model.Function) int { return item.CC }), metric(function.Before, func(item *model.Function) int { return item.SLOC }), metric(function.After, func(item *model.Function) int { return item.SLOC }), function.DeltaMass, markdownInline(function.Note))
	}
	w.line("")
	w.line("</details>")
	w.line("")
	writeCloneDetails(w, result)
	w.line("")
	writeExplainer(w)
	if len(result.Trend) != 0 {
		w.line("")
		writeChart(w, result.Trend)
	}
	return w.err
}

func writeHeadline(w *markdownWriter, result model.Diff) {
	added, removed := totalMass(result)
	cloneDelta := len(result.ClonesAdded) - len(result.ClonesRemoved)
	if added == 0 && removed == 0 && len(result.ClonesAdded) == 0 && len(result.ClonesRemoved) == 0 && len(result.Functions) == 0 {
		w.line("```diff")
		w.line("  No complexity or clone changes detected.")
		w.line("```")
		return
	}
	w.line("```diff")
	w.line("+ %.3f source mass added to functions over CC 10%s", added, contributor(result.Functions, true))
	w.line("- %.3f source mass removed from functions over CC 10%s", removed, contributor(result.Functions, false))
	w.line("± %s clone pairs (%d introduced, %d removed)", signed(cloneDelta), len(result.ClonesAdded), len(result.ClonesRemoved))
	w.line("```")
}

func writeBucketSummary(w *markdownWriter, result model.Diff) {
	w.line("| bucket | mass added over CC 10 | mass removed over CC 10 | clone lines in touched files |")
	w.line("|---|---:|---:|---:|")
	for _, bucket := range buckets() {
		delta := result.Buckets[bucket]
		w.line("| %s | +%.3f | -%.3f | %d → %d |", bucket, delta.MassAddedOverCC10, delta.MassRemovedOverCC10, delta.CloneLinesTouchedBefore, delta.CloneLinesTouchedAfter)
	}
}

func totalMass(result model.Diff) (float64, float64) {
	delta := result.Buckets[model.Source]
	return delta.MassAddedOverCC10, delta.MassRemovedOverCC10
}

func contributor(functions []model.FunctionDelta, added bool) string {
	var selected *model.FunctionDelta
	best := -1.0
	for i := range functions {
		value := functions[i].DeltaMass
		candidate := functions[i].After
		if !added {
			value = -value
			candidate = functions[i].Before
		}
		if value > best && value > 0 && candidate != nil && functionBucket(*candidate) == model.Source && candidate.CC > model.ErosionComplexityCutoff {
			selected = &functions[i]
			best = value
		}
	}
	if selected == nil {
		return ""
	}
	function := selected.After
	if !added {
		function = selected.Before
	}
	return fmt.Sprintf(" (%s in %s, CC %d, %d lines)", safeText(selected.Name), safeText(selected.File), function.CC, function.SLOC)
}

func functionBucket(function model.Function) model.Bucket {
	if function.Bucket == "" {
		return model.Source
	}
	return function.Bucket
}

func functionBucketMetric(function *model.Function) string {
	if function == nil {
		return "·"
	}
	return string(functionBucket(*function))
}

func signed(value int) string {
	if value > 0 {
		return fmt.Sprintf("+%d", value)
	}
	return fmt.Sprint(value)
}

func writeSparklines(w *markdownWriter, points []model.TrendPoint) {
	w.line("```text")
	for _, bucket := range buckets() {
		values := trendValues(points, bucket, func(t model.Totals) float64 { return t.Erosion })
		w.line("erosion, %-6s  %.3f → %.3f  %s", bucket, values[0], values[len(values)-1], sparkline(values))
	}
	for _, bucket := range buckets() {
		values := trendValues(points, bucket, func(t model.Totals) float64 { return t.CloneShare })
		w.line("clone share, %-6s  %.1f%% → %.1f%%  %s", bucket, values[0]*100, values[len(values)-1]*100, sparkline(values))
	}
	w.line("```")
}

func trendValues(points []model.TrendPoint, bucket model.Bucket, selectValue func(model.Totals) float64) []float64 {
	values := make([]float64, len(points))
	for i, point := range points {
		values[i] = selectValue(point.Buckets[bucket])
	}
	return values
}

func sparkline(values []float64) string {
	const bars = "▁▂▃▄▅▆▇█"
	minimum, maximum := values[0], values[0]
	for _, value := range values[1:] {
		minimum = math.Min(minimum, value)
		maximum = math.Max(maximum, value)
	}
	var result strings.Builder
	for _, value := range values {
		index := 0
		if maximum != minimum {
			index = int(math.Round((value - minimum) / (maximum - minimum) * 7))
		}
		result.WriteRune([]rune(bars)[index])
	}
	return result.String()
}

func writeCloneDetails(w *markdownWriter, result model.Diff) {
	w.line("<details>")
	w.line("<summary>%d clone pairs introduced, %d removed</summary>", len(result.ClonesAdded), len(result.ClonesRemoved))
	w.line("")
	files := cloneFiles(result)
	labels := make(map[string]string, len(files))
	for i, file := range files {
		label := fmt.Sprintf("f%d", i+1)
		labels[file] = label
		w.line("<code>%s</code> = <code>%s</code><br>", label, markdownInline(file))
	}
	if len(files) != 0 {
		w.line("")
	}
	w.line("```text")
	for _, pair := range result.ClonesAdded {
		writeMarkdownPair(w, "+", pair, labels)
	}
	for _, pair := range result.ClonesRemoved {
		writeMarkdownPair(w, "-", pair, labels)
	}
	w.line("```")
	w.line("")
	w.line("</details>")
}

func cloneFiles(result model.Diff) []string {
	seen := map[string]struct{}{}
	for _, pairs := range [][]model.ClonePair{result.ClonesAdded, result.ClonesRemoved} {
		for _, pair := range pairs {
			seen[pair.A.File] = struct{}{}
			seen[pair.B.File] = struct{}{}
		}
	}
	files := make([]string, 0, len(seen))
	for file := range seen {
		files = append(files, file)
	}
	sort.Strings(files)
	return files
}

func writeMarkdownPair(w *markdownWriter, change string, pair model.ClonePair, labels map[string]string) {
	w.line("%s %s:%d-%d ↔ %s:%d-%d", change, labels[pair.A.File], pair.A.Start, pair.A.End, labels[pair.B.File], pair.B.Start, pair.B.End)
}

func writeExplainer(w *markdownWriter) {
	w.line("<details>")
	w.line("<summary>How to read these numbers</summary>")
	w.line("")
	w.line("%s", Explainer)
	w.line("")
	w.line("</details>")
}

func writeChart(w *markdownWriter, points []model.TrendPoint) {
	labels := make([]string, len(points))
	for i, point := range points {
		date := safeText(point.Date)
		if parsed, err := time.Parse(time.RFC3339, point.Date); err == nil {
			date = parsed.Format("2006-01")
		}
		labels[i] = fmt.Sprintf("\"%s\"", strings.ReplaceAll(date, "\"", "\\\""))
	}
	for index, bucket := range buckets() {
		values := trendValues(points, bucket, func(t model.Totals) float64 { return t.Erosion })
		formatted := make([]string, len(values))
		for i, value := range values {
			formatted[i] = fmt.Sprintf("%.6f", value)
		}
		if index != 0 {
			w.line("")
		}
		w.line("```mermaid")
		w.line("xychart-beta")
		w.line("    title \"%s erosion by month\"", strings.ToUpper(string(bucket[:1]))+string(bucket[1:]))
		w.line("    x-axis [%s]", strings.Join(labels, ", "))
		w.line("    y-axis \"erosion\" 0 --> 1")
		w.line("    line [%s]", strings.Join(formatted, ", "))
		w.line("```")
	}
}

func writeSnapshotMarkdown(output io.Writer, snapshot model.Snapshot) error {
	w := &markdownWriter{output: output}
	w.line("## slopradar scan")
	w.line("")
	w.line("Revision: <code>%s</code>", markdownInline(snapshot.Rev))
	w.line("")
	w.line("| bucket | functions | mass | mass over CC 10 | erosion | source lines | clone lines | clone share |")
	w.line("|---|---:|---:|---:|---:|---:|---:|---:|")
	for _, bucket := range buckets() {
		totals := snapshot.Buckets[bucket]
		w.line("| %s | %d | %.3f | %.3f | %.3f | %d | %d | %.3f |", bucket, totals.Functions, totals.Mass, totals.MassOverCC10, totals.Erosion, totals.SourceLines, totals.CloneLines, totals.CloneShare)
	}
	w.line("")
	w.line("<details>")
	w.line("<summary>%d functions</summary>", len(snapshot.Functions))
	w.line("")
	w.line("| function | location | bucket | CC | SLOC | mass |")
	w.line("|---|---|---|---:|---:|---:|")
	for _, function := range snapshot.Functions {
		writeMarkdownFunction(w, function, "")
	}
	w.line("")
	w.line("</details>")
	w.line("")
	w.line("<details>")
	w.line("<summary>%d clone pairs</summary>", len(snapshot.Clones))
	w.line("")
	w.line("| first location | second location | tokens | lines |")
	w.line("|---|---|---:|---:|")
	for _, pair := range snapshot.Clones {
		w.line("| <code>%s:%d–%d</code> | <code>%s:%d–%d</code> | %d | %d |", markdownInline(pair.A.File), pair.A.Start, pair.A.End, markdownInline(pair.B.File), pair.B.Start, pair.B.End, pair.Tokens, pair.Lines)
	}
	w.line("")
	w.line("</details>")
	if len(snapshot.SkippedDetails) != 0 || len(snapshot.Warnings) != 0 {
		w.line("")
		w.line("<details>")
		w.line("<summary>%d skipped files, %d warnings</summary>", len(snapshot.SkippedDetails), len(snapshot.Warnings))
		w.line("")
		for _, skipped := range snapshot.SkippedDetails {
			w.line("- Skipped <code>%s</code>: <code>max_file_bytes=%d</code>, <code>asked_bytes=%d</code>", markdownInline(skipped.File), skipped.MaxBytes, skipped.AskedBytes)
		}
		for _, warning := range snapshot.Warnings {
			w.line("- Warning: %s", markdownInline(warning))
		}
		w.line("")
		w.line("</details>")
	}
	return w.err
}

func writeMarkdownFunction(w *markdownWriter, function model.Function, prefix string) {
	w.line("| <code>%s%s</code> | <code>%s:%d</code> | %s | %d | %d | %.3f |", prefix, markdownInline(function.Name), markdownInline(function.File), function.Line, function.Bucket, function.CC, function.SLOC, function.Mass)
	for _, nested := range function.Nested {
		writeMarkdownFunction(w, nested, "↳ "+prefix)
	}
}

func writeTrendMarkdown(output io.Writer, points []model.TrendPoint) error {
	w := &markdownWriter{output: output}
	w.line("## slopradar trend")
	if len(points) == 0 {
		w.line("")
		w.line("No history points.")
		return w.err
	}
	w.line("")
	writeSparklines(w, points)
	w.line("")
	w.line("| date | revision | bucket | erosion | clone share |")
	w.line("|---|---|---|---:|---:|")
	for _, point := range points {
		for _, bucket := range buckets() {
			totals := point.Buckets[bucket]
			w.line("| %s | <code>%s</code> | %s | %.3f | %.3f |", markdownInline(point.Date), markdownInline(point.Rev), bucket, totals.Erosion, totals.CloneShare)
		}
	}
	w.line("")
	writeChart(w, points)
	return w.err
}

func markdownInline(value string) string {
	escaped := html.EscapeString(safeText(value))
	return strings.ReplaceAll(escaped, "|", "&#124;")
}
