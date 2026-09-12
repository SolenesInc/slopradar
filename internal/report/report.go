package report

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"unicode"

	"github.com/SolenesInc/slopradar/internal/model"
)

const Explainer = "Cyclomatic complexity (CC) counts decision paths through a function. SLOC is its non-blank, non-comment source lines. Mass is CC × √SLOC; erosion is the share of repository function mass in functions with CC over 10. A clone pair is two ranges with the same tokens after comments are removed, while clone share is the share of source lines in such ranges. Lower erosion and clone share are generally easier to maintain, but duplication is not always wrong. Absolute erosion varies by language, so compare this repository against its own history. This report is information for the reviewer, not a merge gate."

func ValidateFormat(format string) error {
	if format != "md" && format != "json" && format != "text" {
		return fmt.Errorf("format must be md, json, or text, got %q", format)
	}
	return nil
}

func ColorEnabled(output io.Writer) bool {
	file, ok := output.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func WriteSnapshot(output io.Writer, format string, snapshot model.Snapshot, color bool) error {
	switch format {
	case "json":
		return writeJSON(output, snapshot)
	case "md":
		return writeSnapshotMarkdown(output, snapshot)
	case "text":
		return writeSnapshotText(output, snapshot, color)
	default:
		return ValidateFormat(format)
	}
}

func WriteDiff(output io.Writer, format string, result model.Diff, color bool) error {
	switch format {
	case "json":
		return writeJSON(output, result)
	case "md":
		return writeDiffMarkdown(output, result)
	case "text":
		return writeDiffText(output, result, color)
	default:
		return ValidateFormat(format)
	}
}

func WriteTrend(output io.Writer, format string, points []model.TrendPoint, color bool) error {
	switch format {
	case "json":
		return writeJSON(output, points)
	case "md":
		return writeTrendMarkdown(output, points)
	case "text":
		return writeTrendText(output, points)
	default:
		return ValidateFormat(format)
	}
}

func writeJSON(output io.Writer, value any) error {
	encoder := json.NewEncoder(output)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

type textWriter struct {
	tabs *tabwriter.Writer
	err  error
}

func newTextWriter(output io.Writer) *textWriter {
	return &textWriter{tabs: tabwriter.NewWriter(output, 0, 4, 2, ' ', 0)}
}

func (w *textWriter) line(format string, arguments ...any) {
	if w.err == nil {
		_, w.err = fmt.Fprintf(w.tabs, format+"\n", arguments...)
	}
}

func (w *textWriter) flush() error {
	if w.err != nil {
		return w.err
	}
	return w.tabs.Flush()
}

func safeText(value string) string {
	var result strings.Builder
	for _, character := range value {
		switch character {
		case '\\':
			result.WriteString("\\\\")
		case '\n':
			result.WriteString("\\n")
		case '\r':
			result.WriteString("\\r")
		case '\t':
			result.WriteString("\\t")
		default:
			if unicode.IsControl(character) || character == '\u2028' || character == '\u2029' {
				fmt.Fprintf(&result, "\\u%04x", character)
			} else {
				result.WriteRune(character)
			}
		}
	}
	return result.String()
}

func metric(function *model.Function, value func(*model.Function) int) string {
	if function == nil {
		return "·"
	}
	return fmt.Sprint(value(function))
}

func buckets() []model.Bucket {
	return []model.Bucket{model.Source, model.Tests}
}
