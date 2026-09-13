package trend

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/SolenesInc/slopradar/internal/gitread"
	"github.com/SolenesInc/slopradar/internal/model"
)

func TestMonthsSelectsFirstCommitAtBoundariesAndCurrentHead(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init", "-b", "main")
	git(t, dir, "config", "user.name", "Slopradar Test")
	git(t, dir, "config", "user.email", "test@slopradar.invalid")
	commits := []struct {
		date string
		name string
	}{
		{"2026-01-10T12:00:00Z", "jan"},
		{"2026-01-20T12:00:00Z", "jan-late"},
		{"2026-02-05T12:00:00Z", "feb"},
		{"2026-03-05T12:00:00Z", "mar"},
		{"2026-03-20T12:00:00Z", "head"},
	}
	revs := map[string]string{}
	for _, item := range commits {
		if err := os.WriteFile(filepath.Join(dir, "history.txt"), []byte(item.name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		git(t, dir, "add", "history.txt")
		gitAt(t, dir, item.date, "commit", "-m", item.name)
		revs[item.name] = strings.TrimSpace(git(t, dir, "rev-parse", "HEAD"))
	}
	repository, err := gitread.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Months(context.Background(), repository, "HEAD", 3)
	if err != nil {
		t.Fatal(err)
	}
	gotRevs := make([]string, len(got))
	for i := range got {
		gotRevs[i] = got[i].Rev
	}
	want := []string{revs["jan"], revs["feb"], revs["mar"], revs["head"]}
	if !reflect.DeepEqual(gotRevs, want) {
		t.Fatalf("monthly revisions = %#v, want %#v", gotRevs, want)
	}
	all, err := Months(context.Background(), repository, "HEAD", int(^uint(0)>>1))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(all, got) {
		t.Fatalf("oversized history request = %#v, want %#v", all, got)
	}
}

func TestMergesReturnsRequestedFirstParentMergesInTrendOrder(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init", "-b", "main")
	git(t, dir, "config", "user.name", "Slopradar Test")
	git(t, dir, "config", "user.email", "test@slopradar.invalid")
	if err := os.WriteFile(filepath.Join(dir, "history.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "base")
	mergeRevs := make([]string, 0, 2)
	for _, branch := range []string{"first", "second"} {
		git(t, dir, "checkout", "-b", branch)
		file := filepath.Join(dir, branch+".txt")
		if err := os.WriteFile(file, []byte(branch+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		git(t, dir, "add", ".")
		git(t, dir, "commit", "-m", branch)
		git(t, dir, "checkout", "main")
		git(t, dir, "merge", "--no-ff", branch, "-m", "merge "+branch)
		mergeRevs = append(mergeRevs, strings.TrimSpace(git(t, dir, "rev-parse", "HEAD")))
	}
	repository, err := gitread.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Merges(context.Background(), repository, "HEAD", 2)
	if err != nil {
		t.Fatal(err)
	}
	gotRevs := []string{got[0].Rev, got[1].Rev}
	if !reflect.DeepEqual(gotRevs, mergeRevs) {
		t.Fatalf("merge revisions = %#v, want %#v", gotRevs, mergeRevs)
	}
}

func TestBuildKeepsCommitIdentitiesAndTotalsForIdenticalTrees(t *testing.T) {
	commits := []gitread.Commit{{Rev: "one", Date: "2026-01-01T00:00:00Z"}, {Rev: "two", Date: "2026-02-01T00:00:00Z"}}
	got, err := Build(context.Background(), commits, func(_ context.Context, rev string) (model.Snapshot, error) {
		return model.Snapshot{
			Rev: "shared-tree", Functions: []model.Function{{Name: "discarded"}},
			Buckets: map[model.Bucket]model.Totals{model.Source: {Erosion: 0.25}, model.Tests: {CloneShare: 0.5}},
		}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Rev != "one" || got[1].Rev != "two" || got[1].Date != commits[1].Date || got[0].Buckets[model.Source].Erosion != 0.25 || got[0].Buckets[model.Tests].CloneShare != 0.5 {
		t.Fatalf("points = %#v", got)
	}
}

func TestBuildRejectsWarningSnapshotAndPreservesDiagnostic(t *testing.T) {
	commits := []gitread.Commit{{Rev: "requested", Date: "2026-01-01T00:00:00Z"}}
	points, err := Build(context.Background(), commits, func(_ context.Context, _ string) (model.Snapshot, error) {
		return model.Snapshot{Rev: "resolved", Warnings: []string{"parse source.go: invalid Go syntax; analyzed recoverable syntax"}}, nil
	})
	if err == nil || err.Error() != `scan requested: analysis of revision "resolved" incomplete: parse source.go: invalid Go syntax; analyzed recoverable syntax` {
		t.Fatalf("error = %v", err)
	}
	if points != nil {
		t.Fatalf("partial trend points = %#v", points)
	}
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func gitAt(t *testing.T, dir, date string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_DATE="+date, "GIT_COMMITTER_DATE="+date)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return string(out)
}
