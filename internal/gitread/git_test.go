package gitread

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestRepositoryReadsTreesAndHistory(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init", "-b", "main")
	git(t, dir, "config", "user.name", "Slopradar Test")
	git(t, dir, "config", "user.email", "test@slopradar.invalid")
	write(t, dir, "a.txt", "first\n")
	write(t, dir, "dir/line\nb.txt", "second\x00half\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "base")
	base := strings.TrimSpace(git(t, dir, "rev-parse", "HEAD"))

	git(t, dir, "checkout", "-b", "side")
	write(t, dir, "side.txt", "side\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "side")
	git(t, dir, "checkout", "main")
	write(t, dir, "main.txt", "main\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "main")
	git(t, dir, "merge", "--no-ff", "side", "-m", "merge side")
	head := strings.TrimSpace(git(t, dir, "rev-parse", "HEAD"))

	repo, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	infos, err := repo.ListTree(context.Background(), head)
	if err != nil {
		t.Fatal(err)
	}
	paths := make([]string, len(infos))
	for i := range infos {
		paths[i] = infos[i].Path
	}
	wantPaths := []string{"a.txt", "dir/line\nb.txt", "main.txt", "side.txt"}
	if !reflect.DeepEqual(paths, wantPaths) {
		t.Fatalf("paths = %#v, want %#v", paths, wantPaths)
	}
	blobs, err := repo.ReadBlobs(context.Background(), []BlobInfo{infos[1], infos[0]})
	if err != nil {
		t.Fatal(err)
	}
	if got := string(blobs[0].Content); got != "second\x00half\n" {
		t.Fatalf("blob content = %q", got)
	}
	if got := string(blobs[1].Content); got != "first\n" {
		t.Fatalf("blob content = %q", got)
	}
	mergeBase, err := repo.MergeBase(context.Background(), base, head)
	if err != nil {
		t.Fatal(err)
	}
	if mergeBase != base {
		t.Fatalf("merge base = %s, want %s", mergeBase, base)
	}
	changed, err := repo.ChangedFiles(context.Background(), base, head)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"main.txt", "side.txt"}; !reflect.DeepEqual(changed, want) {
		t.Fatalf("changed files = %#v, want %#v", changed, want)
	}
	merges, err := repo.FirstParentMerges(context.Background(), head, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(merges) != 1 || merges[0].Rev != head || merges[0].Date == "" {
		t.Fatalf("merges = %#v", merges)
	}
	commits, err := repo.FirstParentCommits(context.Background(), head)
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 3 || commits[0].Rev != head || commits[2].Rev != base {
		t.Fatalf("first-parent commits = %#v", commits)
	}
}

func TestHistoryDatesUseCanonicalOffsets(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init", "-b", "main")
	git(t, dir, "config", "user.name", "Slopradar Test")
	git(t, dir, "config", "user.email", "test@slopradar.invalid")
	for _, date := range []string{"2026-01-10T12:00:00+00:00", "2026-02-20T12:00:00+02:00"} {
		t.Setenv("GIT_AUTHOR_DATE", date)
		t.Setenv("GIT_COMMITTER_DATE", date)
		git(t, dir, "commit", "--allow-empty", "-m", date)
	}
	repo, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	commits, err := repo.FirstParentCommits(context.Background(), "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 2 || commits[0].Date != "2026-02-20T12:00:00+02:00" || commits[1].Date != "2026-01-10T12:00:00Z" {
		t.Fatalf("history dates = %#v", commits)
	}
}

func TestReadDirectoryIsDeterministicAndSkipsSymlinks(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "z.txt", "z")
	write(t, dir, "a/b.txt", "b")
	if runtime.GOOS != "windows" {
		if err := os.Symlink(filepath.Join(dir, "z.txt"), filepath.Join(dir, "link.txt")); err != nil {
			t.Fatal(err)
		}
	}
	blobs, err := ReadDirectory(dir)
	if err != nil {
		t.Fatal(err)
	}
	paths := make([]string, len(blobs))
	for i := range blobs {
		paths[i] = blobs[i].Path
	}
	if want := []string{"a/b.txt", "z.txt"}; !reflect.DeepEqual(paths, want) {
		t.Fatalf("paths = %#v, want %#v", paths, want)
	}
}

func TestReadDirectoryResolvesRootSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink permissions are not portable on Windows")
	}
	target := t.TempDir()
	write(t, target, "source.go", "package fixture\n")
	link := filepath.Join(t.TempDir(), "root")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	blobs, err := ReadDirectory(link)
	if err != nil {
		t.Fatal(err)
	}
	if len(blobs) != 1 || blobs[0].Path != "source.go" {
		t.Fatalf("blobs = %#v", blobs)
	}
}

func TestListTreeSkipsTrackedSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink modes are not portable on Windows")
	}
	dir := t.TempDir()
	git(t, dir, "init", "-b", "main")
	git(t, dir, "config", "user.name", "Slopradar Test")
	git(t, dir, "config", "user.email", "test@slopradar.invalid")
	write(t, dir, "source.go", "package fixture\n")
	if err := os.Symlink("source.go", filepath.Join(dir, "linked.go")); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "source and link")
	repo, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	infos, err := repo.ListTree(context.Background(), "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 || infos[0].Path != "source.go" {
		t.Fatalf("infos = %#v", infos)
	}
}

func TestReadDirectoryFilteredDoesNotReadRejectedContent(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "keep.txt", "keep")
	write(t, dir, "excluded/unreadable.txt", "unreadable")
	unreadable := filepath.Join(dir, "excluded", "unreadable.txt")
	if err := os.Chmod(unreadable, 0); err != nil {
		t.Fatal(err)
	}
	blobs, err := ReadDirectoryFiltered(dir, func(path string, _ int64, directory bool) bool {
		return path != "excluded" || !directory
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(blobs) != 1 || blobs[0].Path != "keep.txt" {
		t.Fatalf("blobs = %#v", blobs)
	}
}

func TestOpenPreservesTrailingWhitespaceInRoot(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "trailing ")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "init", "-b", "main")
	git(t, dir, "config", "user.name", "Slopradar Test")
	git(t, dir, "config", "user.email", "test@slopradar.invalid")
	write(t, dir, "a.txt", "content\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "base")
	repo, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	blobs, err := repo.ReadTree(context.Background(), "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if len(blobs) != 1 || string(blobs[0].Content) != "content\n" {
		t.Fatalf("blobs = %#v", blobs)
	}
}

func TestChangedFilesTreatsRenameAsRemovedAndAdded(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init", "-b", "main")
	git(t, dir, "config", "user.name", "Slopradar Test")
	git(t, dir, "config", "user.email", "test@slopradar.invalid")
	write(t, dir, "old.go", "package fixture\n\nfunc moved() {}\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "base")
	base := strings.TrimSpace(git(t, dir, "rev-parse", "HEAD"))
	git(t, dir, "mv", "old.go", "new.go")
	git(t, dir, "commit", "-m", "rename")
	head := strings.TrimSpace(git(t, dir, "rev-parse", "HEAD"))
	repository, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := repository.ChangedFiles(context.Background(), base, head)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"new.go", "old.go"}; !reflect.DeepEqual(changed, want) {
		t.Fatalf("changed files = %#v, want %#v", changed, want)
	}
}

func TestReadBlobsStopsMalformedStreamingProducer(t *testing.T) {
	bin := t.TempDir()
	fakeGit := filepath.Join(bin, "git")
	script := "#!/bin/sh\nprintf 'invalid header\\n'\nwhile printf 'undrained payload\\n'; do :; done\n"
	if err := os.WriteFile(fakeGit, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	repo := &Repository{dir: t.TempDir()}
	infos := []BlobInfo{{Path: "malformed", OID: "malformed"}}
	if _, err := repo.ReadBlobs(context.Background(), infos); err == nil {
		t.Fatal("ReadBlobs accepted malformed output")
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

func write(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
