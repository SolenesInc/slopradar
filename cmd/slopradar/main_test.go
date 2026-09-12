package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/SolenesInc/slopradar/internal/model"
)

func TestScanDirectoryJSONIsDeterministic(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "source.go", "package fixture\n\nfunc run(value bool) {\n\tif value {\n\t\treturn\n\t}\n}\n")
	writeFile(t, dir, "source_test.go", "package fixture\n\nfunc TestRun() {}\n")
	var first bytes.Buffer
	if err := run(context.Background(), []string{"scan", dir, "--format", "json"}, &first); err != nil {
		t.Fatal(err)
	}
	var second bytes.Buffer
	if err := run(context.Background(), []string{"scan", "--format=json", dir}, &second); err != nil {
		t.Fatal(err)
	}
	if first.String() != second.String() {
		t.Fatalf("outputs differ\nfirst: %s\nsecond: %s", first.String(), second.String())
	}
	var snapshot model.Snapshot
	if err := json.Unmarshal(first.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Rev != "directory" || snapshot.Buckets[model.Source].Functions != 1 || snapshot.Buckets[model.Tests].Functions != 1 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestScanArgsRejectsVisibleLimits(t *testing.T) {
	_, _, err := scanArgs([]string{"one", "two"})
	if err == nil || err.Error() != `scan accepts one revision or directory, got "two"` {
		t.Fatalf("error = %v", err)
	}
}

func TestWriteTextIncludesSkipLimitAndAsk(t *testing.T) {
	snapshot := model.Snapshot{
		Rev: "abc", Buckets: map[model.Bucket]model.Totals{model.Source: {}, model.Tests: {}},
		SkippedDetails: []model.SkippedFile{{File: "large.go", MaxBytes: model.MaxFileBytes, AskedBytes: model.MaxFileBytes + 1}},
	}
	var output bytes.Buffer
	writeText(&output, snapshot)
	want := fmt.Sprintf("skipped\tlarge.go\tmax_file_bytes=%d\tasked_bytes=%d\n", model.MaxFileBytes, model.MaxFileBytes+1)
	if !bytes.Contains(output.Bytes(), []byte(want)) {
		t.Fatalf("output = %q, want line %q", output.String(), want)
	}
}

func TestWriteTextIncludesClonePairs(t *testing.T) {
	snapshot := model.Snapshot{
		Rev: "abc",
		Buckets: map[model.Bucket]model.Totals{
			model.Source: {SourceLines: 8, CloneLines: 4, CloneShare: 0.5},
			model.Tests:  {},
		},
		Clones: []model.ClonePair{{
			ID: "stable", A: model.Range{File: "a.go", Start: 2, End: 5}, B: model.Range{File: "b.go", Start: 7, End: 10}, Tokens: 50, Lines: 4,
		}},
	}
	var output bytes.Buffer
	writeText(&output, snapshot)
	want := "stable\ta.go\t2\t5\tb.go\t7\t10\t50\t4\n"
	if !bytes.Contains(output.Bytes(), []byte(want)) {
		t.Fatalf("output = %q, want %q", output.String(), want)
	}
}

func writeFile(t *testing.T, root, name, content string) {
	t.Helper()
	file := filepath.Join(root, name)
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestScanArgsAllowsOptionsOnEitherSide(t *testing.T) {
	firstTarget, firstFormat, firstErr := scanArgs([]string{"HEAD", "--format", "json"})
	secondTarget, secondFormat, secondErr := scanArgs([]string{"--format=json", "HEAD"})
	if firstErr != nil || secondErr != nil || !reflect.DeepEqual([]string{firstTarget, firstFormat}, []string{secondTarget, secondFormat}) {
		t.Fatalf("first = %q %q %v, second = %q %q %v", firstTarget, firstFormat, firstErr, secondTarget, secondFormat, secondErr)
	}
}

func TestDiffResolvesBaseToMergeBase(t *testing.T) {
	dir := t.TempDir()
	gitCommand(t, dir, "init", "-b", "main")
	gitCommand(t, dir, "config", "user.name", "Slopradar Test")
	gitCommand(t, dir, "config", "user.email", "test@slopradar.invalid")
	writeFile(t, dir, "source.go", "package fixture\n")
	gitCommand(t, dir, "add", ".")
	gitCommand(t, dir, "commit", "-m", "base")
	mergeBase := strings.TrimSpace(gitCommand(t, dir, "rev-parse", "HEAD"))

	gitCommand(t, dir, "checkout", "-b", "feature")
	writeFile(t, dir, "source.go", cc12Source())
	gitCommand(t, dir, "add", ".")
	gitCommand(t, dir, "commit", "-m", "feature")
	feature := strings.TrimSpace(gitCommand(t, dir, "rev-parse", "HEAD"))

	gitCommand(t, dir, "checkout", "main")
	writeFile(t, dir, "main_only.go", "package fixture\n")
	gitCommand(t, dir, "add", ".")
	gitCommand(t, dir, "commit", "-m", "main moved")

	t.Chdir(dir)
	var output bytes.Buffer
	if err := run(context.Background(), []string{"diff", "--base", "main", "--head", "feature", "--format", "json"}, &output); err != nil {
		t.Fatal(err)
	}
	var got model.Diff
	if err := json.Unmarshal(output.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Base != mergeBase || got.Head != feature || !reflect.DeepEqual(got.Touched, []string{"source.go"}) {
		t.Fatalf("diff revisions = base %q head %q touched %#v", got.Base, got.Head, got.Touched)
	}
	if len(got.Functions) != 1 || got.Functions[0].Name != "complex" || got.Functions[0].Note != "new" || got.Functions[0].After.CC != 12 {
		t.Fatalf("function deltas = %#v", got.Functions)
	}
	if got.Buckets[model.Source].MassAddedOverCC10 != got.Functions[0].After.Mass {
		t.Fatalf("source delta = %#v", got.Buckets[model.Source])
	}
}

func TestTrendArgsRequireOnePositiveSelector(t *testing.T) {
	for _, args := range [][]string{{}, {"--merges", "0"}, {"--months", "2", "--merges", "2"}} {
		if _, err := parseTrendArgs(args); err == nil {
			t.Fatalf("parseTrendArgs(%#v) succeeded", args)
		}
	}
}

func TestDiffTracksFunctionLifecycleAcrossThrowawayCommits(t *testing.T) {
	dir := t.TempDir()
	gitCommand(t, dir, "init", "-b", "main")
	gitCommand(t, dir, "config", "user.name", "Slopradar Test")
	gitCommand(t, dir, "config", "user.email", "test@slopradar.invalid")
	writeFile(t, dir, "source.go", "package fixture\n\nfunc stable() int { return 1 }\n")
	gitCommand(t, dir, "add", ".")
	gitCommand(t, dir, "commit", "-m", "base")
	base := strings.TrimSpace(gitCommand(t, dir, "rev-parse", "HEAD"))

	writeFile(t, dir, "source.go", lifecycleAddedSource())
	gitCommand(t, dir, "add", ".")
	gitCommand(t, dir, "commit", "-m", "add complex function and duplicate block")
	added := strings.TrimSpace(gitCommand(t, dir, "rev-parse", "HEAD"))

	writeFile(t, dir, "source.go", lifecycleSplitSource())
	gitCommand(t, dir, "add", ".")
	gitCommand(t, dir, "commit", "-m", "split complex function")
	split := strings.TrimSpace(gitCommand(t, dir, "rev-parse", "HEAD"))

	writeFile(t, dir, "source.go", lifecycleDeletedSource())
	gitCommand(t, dir, "add", ".")
	gitCommand(t, dir, "commit", "-m", "delete duplicate block")
	deleted := strings.TrimSpace(gitCommand(t, dir, "rev-parse", "HEAD"))

	t.Chdir(dir)
	addedDiff := commandDiff(t, base, added)
	assertFunctionNotes(t, addedDiff, map[string]string{"complex": "new", "duplicateA": "new", "duplicateB": "new"})
	if functionByName(t, addedDiff, "complex").After.CC != 12 || addedDiff.Buckets[model.Source].MassAddedOverCC10 != functionByName(t, addedDiff, "complex").After.Mass {
		t.Fatalf("added complex delta = %#v", addedDiff)
	}

	splitDiff := commandDiff(t, added, split)
	assertFunctionNotes(t, splitDiff, map[string]string{"complex": "removed", "splitA": "new", "splitB": "new"})
	if splitDiff.Buckets[model.Source].MassRemovedOverCC10 != functionByName(t, splitDiff, "complex").Before.Mass {
		t.Fatalf("split mass delta = %#v", splitDiff.Buckets[model.Source])
	}

	deletedDiff := commandDiff(t, split, deleted)
	assertFunctionNotes(t, deletedDiff, map[string]string{"duplicateB": "removed"})
}

func TestTrendCommandRendersMonthlySnapshotsInJSONAndText(t *testing.T) {
	dir := t.TempDir()
	gitCommand(t, dir, "init", "-b", "main")
	gitCommand(t, dir, "config", "user.name", "Slopradar Test")
	gitCommand(t, dir, "config", "user.email", "test@slopradar.invalid")
	commits := []struct {
		date   string
		source string
	}{
		{"2026-01-10T12:00:00Z", "package fixture\n\nfunc simple() {}\n"},
		{"2026-02-05T12:00:00Z", cc12Source()},
		{"2026-02-20T12:00:00Z", cc12Source() + "\nfunc another() {}\n"},
	}
	for _, item := range commits {
		writeFile(t, dir, "source.go", item.source)
		gitCommand(t, dir, "add", ".")
		gitCommandAt(t, dir, item.date, "commit", "-m", item.date)
	}
	head := strings.TrimSpace(gitCommand(t, dir, "rev-parse", "HEAD"))
	t.Chdir(dir)
	var jsonOutput bytes.Buffer
	if err := run(context.Background(), []string{"trend", "--months", "2", "--format", "json"}, &jsonOutput); err != nil {
		t.Fatal(err)
	}
	var points []model.TrendPoint
	if err := json.Unmarshal(jsonOutput.Bytes(), &points); err != nil {
		t.Fatal(err)
	}
	if len(points) != 3 || points[len(points)-1].Rev != head || points[1].Buckets[model.Source].Erosion != 1 {
		t.Fatalf("trend points = %#v", points)
	}
	var textOutput bytes.Buffer
	if err := run(context.Background(), []string{"trend", "--months=2", "--format=text"}, &textOutput); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(textOutput.String(), "date\trevision\tbucket\terosion\tclone_share\n") || !strings.Contains(textOutput.String(), head) {
		t.Fatalf("text trend = %q", textOutput.String())
	}
}

func cc12Source() string {
	return `package fixture

func complex(value int) int {
	if value > 0 { value++ }
	if value > 1 { value++ }
	if value > 2 { value++ }
	if value > 3 { value++ }
	if value > 4 { value++ }
	if value > 5 { value++ }
	if value > 6 { value++ }
	if value > 7 { value++ }
	if value > 8 { value++ }
	if value > 9 { value++ }
	if value > 10 { value++ }
	return value
}
`
}

func lifecycleAddedSource() string {
	return "package fixture\n\nfunc stable() int { return 1 }\n\n" + strings.TrimPrefix(cc12Source(), "package fixture\n\n") + duplicateFunctions()
}

func lifecycleSplitSource() string {
	return `package fixture

func stable() int { return 1 }

func splitA(value int) int {
	if value > 0 { value++ }
	if value > 1 { value++ }
	if value > 2 { value++ }
	if value > 3 { value++ }
	if value > 4 { value++ }
	return value
}

func splitB(value int) int {
	if value > 5 { value++ }
	if value > 6 { value++ }
	if value > 7 { value++ }
	if value > 8 { value++ }
	if value > 9 { value++ }
	if value > 10 { value++ }
	return value
}
` + duplicateFunctions()
}

func lifecycleDeletedSource() string {
	return strings.Replace(lifecycleSplitSource(), duplicateB(), "", 1)
}

func duplicateFunctions() string {
	return duplicateA() + duplicateB()
}

func duplicateA() string {
	return strings.Replace(duplicateB(), "duplicateB", "duplicateA", 1)
}

func duplicateB() string {
	return `
func duplicateB(value int) int {
	value = value + 1
	value = value + 2
	value = value + 3
	value = value + 4
	value = value + 5
	value = value + 6
	value = value + 7
	value = value + 8
	value = value + 9
	value = value + 10
	return value
}
`
}

func commandDiff(t *testing.T, base, head string) model.Diff {
	t.Helper()
	var output bytes.Buffer
	if err := run(context.Background(), []string{"diff", "--base", base, "--head", head, "--format", "json"}, &output); err != nil {
		t.Fatal(err)
	}
	var result model.Diff
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func assertFunctionNotes(t *testing.T, result model.Diff, want map[string]string) {
	t.Helper()
	got := make(map[string]string, len(result.Functions))
	for _, function := range result.Functions {
		got[function.Name] = function.Note
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("function notes = %#v, want %#v", got, want)
	}
}

func functionByName(t *testing.T, result model.Diff, name string) model.FunctionDelta {
	t.Helper()
	for _, function := range result.Functions {
		if function.Name == name {
			return function
		}
	}
	t.Fatalf("function %q not found in %#v", name, result.Functions)
	return model.FunctionDelta{}
}

func gitCommand(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", dir}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, output)
	}
	return string(output)
}

func gitCommandAt(t *testing.T, dir, date string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", dir}, args...)...)
	command.Env = append(os.Environ(), "GIT_AUTHOR_DATE="+date, "GIT_COMMITTER_DATE="+date)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, output)
	}
	return string(output)
}
