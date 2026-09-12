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
	"github.com/SolenesInc/slopradar/internal/report"
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
	_, _, _, err := scanArgs([]string{"one", "two"})
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
	if err := report.WriteSnapshot(&output, "text", snapshot, false); err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("skipped  large.go  max_file_bytes=%d  asked_bytes=%d\n", model.MaxFileBytes, model.MaxFileBytes+1)
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
	if err := report.WriteSnapshot(&output, "text", snapshot, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "stable") || !strings.Contains(output.String(), "a.go:2-5") || !strings.Contains(output.String(), "b.go:7-10") {
		t.Fatalf("output = %q", output.String())
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
	firstTarget, firstFormat, firstCache, firstErr := scanArgs([]string{"HEAD", "--format", "json"})
	secondTarget, secondFormat, secondCache, secondErr := scanArgs([]string{"--format=json", "--no-cache", "HEAD"})
	if firstErr != nil || secondErr != nil || !reflect.DeepEqual([]string{firstTarget, firstFormat}, []string{secondTarget, secondFormat}) || !firstCache || secondCache {
		t.Fatalf("first = %q %q cache=%t %v, second = %q %q cache=%t %v", firstTarget, firstFormat, firstCache, firstErr, secondTarget, secondFormat, secondCache, secondErr)
	}
}

func TestEveryCommandAcceptsMarkdownFormat(t *testing.T) {
	if _, format, _, err := scanArgs([]string{"--format", "md"}); err != nil || format != "md" {
		t.Fatalf("scan format = %q, error = %v", format, err)
	}
	if options, err := parseDiffArgs([]string{"--base", "main", "--head", "HEAD", "--format=md"}); err != nil || options.format != "md" {
		t.Fatalf("diff options = %#v, error = %v", options, err)
	}
	if options, err := parseTrendArgs([]string{"--months", "1", "--format=md"}); err != nil || options.format != "md" {
		t.Fatalf("trend options = %#v, error = %v", options, err)
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
	if err := run(context.Background(), []string{"diff", "--base", "main", "--head", "feature", "--format", "json", "--no-cache"}, &output); err != nil {
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

func TestDiffRejectsHeadThatCrossesFileSizeTripwire(t *testing.T) {
	dir := t.TempDir()
	gitCommand(t, dir, "init", "-b", "main")
	gitCommand(t, dir, "config", "user.name", "Slopradar Test")
	gitCommand(t, dir, "config", "user.email", "test@slopradar.invalid")
	writeFile(t, dir, "source.go", cc12Source())
	gitCommand(t, dir, "add", ".")
	gitCommand(t, dir, "commit", "-m", "base")
	base := strings.TrimSpace(gitCommand(t, dir, "rev-parse", "HEAD"))
	askedBytes := int64(model.MaxFileBytes) + 1
	if err := os.Truncate(filepath.Join(dir, "source.go"), askedBytes); err != nil {
		t.Fatal(err)
	}
	gitCommand(t, dir, "add", ".")
	gitCommand(t, dir, "commit", "-m", "cross size tripwire")
	head := strings.TrimSpace(gitCommand(t, dir, "rev-parse", "HEAD"))
	t.Chdir(dir)
	var output bytes.Buffer
	err := run(context.Background(), []string{"diff", "--base", base, "--head", head, "--format", "json", "--no-cache"}, &output)
	want := fmt.Sprintf("head snapshot: analysis of revision %q incomplete: file %q: max_file_bytes=%d, asked_bytes=%d", head, "source.go", model.MaxFileBytes, askedBytes)
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
	if output.Len() != 0 {
		t.Fatalf("partial diff output = %q", output.String())
	}
}

func TestDiffReportsConfigOnlyExclusionAndBucketChanges(t *testing.T) {
	dir := t.TempDir()
	gitCommand(t, dir, "init", "-b", "main")
	gitCommand(t, dir, "config", "user.name", "Slopradar Test")
	gitCommand(t, dir, "config", "user.email", "test@slopradar.invalid")
	writeFile(t, dir, "configured.go", cc12Source()+duplicateA())
	writeFile(t, dir, "peer.go", "package fixture\n"+duplicateB())
	gitCommand(t, dir, "add", ".")
	gitCommand(t, dir, "commit", "-m", "base")
	base := strings.TrimSpace(gitCommand(t, dir, "rev-parse", "HEAD"))

	writeFile(t, dir, model.ConfigFile, `{"excludes":["configured.go"]}`)
	gitCommand(t, dir, "add", model.ConfigFile)
	gitCommand(t, dir, "commit", "-m", "exclude configured source")
	excluded := strings.TrimSpace(gitCommand(t, dir, "rev-parse", "HEAD"))

	writeFile(t, dir, model.ConfigFile, `{"test_globs":["configured.go"]}`)
	gitCommand(t, dir, "add", model.ConfigFile)
	gitCommand(t, dir, "commit", "-m", "move configured source to tests")
	tests := strings.TrimSpace(gitCommand(t, dir, "rev-parse", "HEAD"))

	t.Chdir(dir)
	wantTouched := []string{model.ConfigFile, "configured.go"}
	excludedDiff := commandDiff(t, base, excluded)
	if !reflect.DeepEqual(excludedDiff.Touched, wantTouched) {
		t.Fatalf("excluded touched = %#v, want %#v", excludedDiff.Touched, wantTouched)
	}
	assertFunctionNotes(t, excludedDiff, map[string]string{"complex": "removed", "duplicateA": "removed"})
	complexMass := functionByName(t, excludedDiff, "complex").Before.Mass
	excludedSource := excludedDiff.Buckets[model.Source]
	if excludedSource.MassRemovedOverCC10 != complexMass || excludedSource.CloneLinesTouchedBefore == 0 || excludedSource.CloneLinesTouchedAfter != 0 || len(excludedDiff.ClonesRemoved) != 1 || len(excludedDiff.ClonesAdded) != 0 {
		t.Fatalf("excluded diff = %#v", excludedDiff)
	}

	bucketDiff := commandDiff(t, base, tests)
	if !reflect.DeepEqual(bucketDiff.Touched, wantTouched) {
		t.Fatalf("bucket touched = %#v, want %#v", bucketDiff.Touched, wantTouched)
	}
	assertFunctionNotes(t, bucketDiff, map[string]string{"complex": "", "duplicateA": ""})
	source := bucketDiff.Buckets[model.Source]
	testBucket := bucketDiff.Buckets[model.Tests]
	if source.MassRemovedOverCC10 != complexMass || testBucket.MassAddedOverCC10 != complexMass || source.CloneLinesTouchedBefore == 0 || source.CloneLinesTouchedAfter != 0 || testBucket.CloneLinesTouchedBefore != 0 || testBucket.CloneLinesTouchedAfter != source.CloneLinesTouchedBefore || len(bucketDiff.ClonesAdded) != 0 || len(bucketDiff.ClonesRemoved) != 0 {
		t.Fatalf("bucket diff = %#v", bucketDiff)
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
	if len(addedDiff.ClonesAdded) != 1 || len(addedDiff.ClonesRemoved) != 0 || addedDiff.Buckets[model.Source].CloneLinesTouchedBefore != 0 || addedDiff.Buckets[model.Source].CloneLinesTouchedAfter != 26 {
		t.Fatalf("added clone delta = %#v", addedDiff)
	}

	splitDiff := commandDiff(t, added, split)
	assertFunctionNotes(t, splitDiff, map[string]string{"complex": "removed", "splitA": "new", "splitB": "new"})
	if splitDiff.Buckets[model.Source].MassRemovedOverCC10 != functionByName(t, splitDiff, "complex").Before.Mass {
		t.Fatalf("split mass delta = %#v", splitDiff.Buckets[model.Source])
	}
	if len(splitDiff.ClonesAdded) != 0 || len(splitDiff.ClonesRemoved) != 0 || splitDiff.Buckets[model.Source].CloneLinesTouchedBefore != 26 || splitDiff.Buckets[model.Source].CloneLinesTouchedAfter != 26 {
		t.Fatalf("line-shifted clone delta = %#v", splitDiff)
	}

	deletedDiff := commandDiff(t, split, deleted)
	assertFunctionNotes(t, deletedDiff, map[string]string{"duplicateB": "removed"})
	if len(deletedDiff.ClonesAdded) != 0 || len(deletedDiff.ClonesRemoved) != 1 || deletedDiff.ClonesRemoved[0].ID != addedDiff.ClonesAdded[0].ID || deletedDiff.Buckets[model.Source].CloneLinesTouchedBefore != 26 || deletedDiff.Buckets[model.Source].CloneLinesTouchedAfter != 0 {
		t.Fatalf("removed clone delta = %#v", deletedDiff)
	}
}

func TestDiffPreservesRepeatedFunctionsWhenOneIsInsertedAndRemoved(t *testing.T) {
	dir := t.TempDir()
	gitCommand(t, dir, "init", "-b", "main")
	gitCommand(t, dir, "config", "user.name", "Slopradar Test")
	gitCommand(t, dir, "config", "user.email", "test@slopradar.invalid")
	writeFile(t, dir, "repeated.go", repeatedInitSource(false))
	gitCommand(t, dir, "add", ".")
	gitCommand(t, dir, "commit", "-m", "base")
	base := strings.TrimSpace(gitCommand(t, dir, "rev-parse", "HEAD"))

	writeFile(t, dir, "repeated.go", repeatedInitSource(true))
	gitCommand(t, dir, "add", ".")
	gitCommand(t, dir, "commit", "-m", "insert init")
	head := strings.TrimSpace(gitCommand(t, dir, "rev-parse", "HEAD"))
	writeFile(t, dir, "repeated.go", repeatedInitSource(false))
	gitCommand(t, dir, "add", ".")
	gitCommand(t, dir, "commit", "-m", "remove init")
	removedHead := strings.TrimSpace(gitCommand(t, dir, "rev-parse", "HEAD"))

	t.Chdir(dir)
	inserted := commandDiff(t, base, head)
	if len(inserted.Functions) != 1 || inserted.Functions[0].Name != "init" || inserted.Functions[0].Before != nil || inserted.Functions[0].After.CC != 4 || inserted.Functions[0].Note != "new" {
		t.Fatalf("inserted repeated function diff = %#v", inserted.Functions)
	}
	removed := commandDiff(t, head, removedHead)
	if len(removed.Functions) != 1 || removed.Functions[0].Name != "init" || removed.Functions[0].Before.CC != 4 || removed.Functions[0].After != nil || removed.Functions[0].Note != "removed" {
		t.Fatalf("removed repeated function diff = %#v", removed.Functions)
	}
}

func TestDiffReportGoldensFromThrowawayRepository(t *testing.T) {
	goldenRoot, err := filepath.Abs(filepath.Join("..", "..", "testdata", "report"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	gitCommand(t, dir, "init", "-b", "main")
	gitCommand(t, dir, "config", "user.name", "Slopradar Test")
	gitCommand(t, dir, "config", "user.email", "test@slopradar.invalid")
	writeFile(t, dir, "source.go", "package fixture\n\nfunc stable() int { return 1 }\n")
	gitCommand(t, dir, "add", ".")
	gitCommandAt(t, dir, "2026-01-10T12:00:00Z", "commit", "-m", "base")
	base := strings.TrimSpace(gitCommand(t, dir, "rev-parse", "HEAD"))
	writeFile(t, dir, "source.go", lifecycleAddedSource())
	gitCommand(t, dir, "add", ".")
	gitCommandAt(t, dir, "2026-02-20T12:00:00Z", "commit", "-m", "add complexity and clones")
	head := strings.TrimSpace(gitCommand(t, dir, "rev-parse", "HEAD"))
	t.Chdir(dir)
	for _, format := range []string{"md", "text", "json"} {
		var output bytes.Buffer
		if err := run(context.Background(), []string{"diff", "--base", base, "--head", head, "--trend", "2", "--format", format, "--no-cache"}, &output); err != nil {
			t.Fatal(err)
		}
		basePlaceholder := "<base>" + strings.Repeat("_", len(base)-len("<base>"))
		headPlaceholder := "<head>" + strings.Repeat("_", len(head)-len("<head>"))
		got := strings.ReplaceAll(strings.ReplaceAll(output.String(), base, basePlaceholder), head, headPlaceholder)
		golden := filepath.Join(goldenRoot, "diff."+format)
		want, err := os.ReadFile(golden)
		if err != nil {
			t.Fatal(err)
		}
		if got != string(want) {
			t.Fatalf("%s report differs from %s\n%s", format, golden, got)
		}
	}
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
	if err := run(context.Background(), []string{"trend", "--months", "2", "--format", "json", "--no-cache"}, &jsonOutput); err != nil {
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
	if err := run(context.Background(), []string{"trend", "--months=2", "--format=text", "--no-cache"}, &textOutput); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(textOutput.String(), "date") || !strings.Contains(textOutput.String(), "revision") || !strings.Contains(textOutput.String(), head) {
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

func repeatedInitSource(inserted bool) string {
	newFunction := ""
	if inserted {
		newFunction = `func init() {
	if first() {}
	if second() {}
	if third() {}
}

`
	}
	return `package fixture

` + newFunction + `func init() {}

func init() {
	if existing() {}
}
`
}

func commandDiff(t *testing.T, base, head string) model.Diff {
	t.Helper()
	var output bytes.Buffer
	if err := run(context.Background(), []string{"diff", "--base", base, "--head", head, "--format", "json", "--no-cache"}, &output); err != nil {
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
