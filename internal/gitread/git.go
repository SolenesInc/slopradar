package gitread

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type BlobInfo struct {
	Path string
	OID  string
	Size int64
}

type Blob struct {
	BlobInfo
	Content []byte
}

type Commit struct {
	Rev  string
	Date string
}

type Merge = Commit

type LineChange struct {
	File        string
	BeforeStart int
	BeforeCount int
	AfterStart  int
	AfterCount  int
}

type Repository struct {
	dir string
}

const (
	lsTreeModeField = iota
	lsTreeTypeField
	lsTreeOIDField
	lsTreeSizeField
	lsTreeFieldCount
)

const (
	catFileOIDField = iota
	catFileTypeField
	catFileSizeField
	catFileFieldCount
)

const (
	logRevisionField = iota
	logDateField
	logFieldCount
)

const (
	gitRegularFileMode    = "100644"
	gitExecutableFileMode = "100755"
)

func Open(dir string) (*Repository, error) {
	root, err := gitOutput(context.Background(), dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, err
	}
	return &Repository{dir: strings.TrimSuffix(string(root), "\n")}, nil
}

func (r *Repository) ListTree(ctx context.Context, rev string) ([]BlobInfo, error) {
	out, err := gitOutput(ctx, r.dir, "ls-tree", "-r", "-l", "-z", "--full-tree", rev)
	if err != nil {
		return nil, err
	}
	entries := bytes.Split(out, []byte{0})
	blobs := make([]BlobInfo, 0, len(entries))
	for _, entry := range entries {
		if len(entry) == 0 {
			continue
		}
		header, path, ok := bytes.Cut(entry, []byte{'\t'})
		if !ok {
			return nil, fmt.Errorf("git ls-tree returned an entry without a path: %q", entry)
		}
		fields := bytes.Fields(header)
		if len(fields) != lsTreeFieldCount {
			return nil, fmt.Errorf("git ls-tree returned an invalid header: %q", header)
		}
		if string(fields[lsTreeTypeField]) != "blob" || !regularBlobMode(string(fields[lsTreeModeField])) {
			continue
		}
		size, err := strconv.ParseInt(string(fields[lsTreeSizeField]), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parse size for %q: %w", path, err)
		}
		blobs = append(blobs, BlobInfo{Path: string(path), OID: string(fields[lsTreeOIDField]), Size: size})
	}
	sort.Slice(blobs, func(i, j int) bool { return blobs[i].Path < blobs[j].Path })
	return blobs, nil
}

func (r *Repository) ReadBlobs(ctx context.Context, infos []BlobInfo) ([]Blob, error) {
	if len(infos) == 0 {
		return []Blob{}, nil
	}
	childCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(childCtx, "git", "-C", r.dir, "cat-file", "--batch")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("open git cat-file input: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("open git cat-file output: %w", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start git cat-file: %w", err)
	}
	writeErr := make(chan error, 1)
	go func() {
		writer := bufio.NewWriter(stdin)
		for _, info := range infos {
			if _, err := fmt.Fprintln(writer, info.OID); err != nil {
				writeErr <- err
				_ = stdin.Close()
				return
			}
		}
		err := errors.Join(writer.Flush(), stdin.Close())
		writeErr <- err
	}()
	abort := func() {
		cancel()
		_ = stdin.Close()
		_ = stdout.Close()
		_ = cmd.Wait()
		<-writeErr
	}

	reader := bufio.NewReader(stdout)
	blobs := make([]Blob, 0, len(infos))
	for _, info := range infos {
		header, err := reader.ReadString('\n')
		if err != nil {
			abort()
			return nil, fmt.Errorf("read git cat-file header for %q: %w", info.Path, err)
		}
		fields := strings.Fields(header)
		if len(fields) != catFileFieldCount || fields[catFileTypeField] != "blob" {
			abort()
			return nil, fmt.Errorf("git cat-file returned an invalid blob header for %q: %q", info.Path, strings.TrimSpace(header))
		}
		if fields[catFileOIDField] != info.OID {
			abort()
			return nil, fmt.Errorf("git cat-file returned %s for %q, want %s", fields[catFileOIDField], info.Path, info.OID)
		}
		size, err := strconv.ParseInt(fields[catFileSizeField], 10, 64)
		if err != nil || size < 0 {
			abort()
			return nil, fmt.Errorf("git cat-file returned an invalid size for %q: %q", info.Path, fields[catFileSizeField])
		}
		content := make([]byte, size)
		if _, err := io.ReadFull(reader, content); err != nil {
			abort()
			return nil, fmt.Errorf("read git blob %q: %w", info.Path, err)
		}
		terminator, err := reader.ReadByte()
		if err != nil || terminator != '\n' {
			abort()
			return nil, fmt.Errorf("git cat-file returned an invalid terminator for %q", info.Path)
		}
		blobs = append(blobs, Blob{BlobInfo: BlobInfo{Path: info.Path, OID: info.OID, Size: size}, Content: content})
	}
	if err := <-writeErr; err != nil {
		_ = cmd.Wait()
		return nil, fmt.Errorf("write git cat-file input: %w", err)
	}
	if err := cmd.Wait(); err != nil {
		return nil, fmt.Errorf("git cat-file: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return blobs, nil
}

func (r *Repository) ReadTree(ctx context.Context, rev string) ([]Blob, error) {
	infos, err := r.ListTree(ctx, rev)
	if err != nil {
		return nil, err
	}
	return r.ReadBlobs(ctx, infos)
}

func (r *Repository) ResolveRevision(ctx context.Context, rev string) (string, error) {
	out, err := gitOutput(ctx, r.dir, "rev-parse", "--verify", rev+"^{commit}")
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(string(out), "\n"), nil
}

func (r *Repository) MergeBase(ctx context.Context, base, head string) (string, error) {
	out, err := gitOutput(ctx, r.dir, "merge-base", base, head)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (r *Repository) ChangedFiles(ctx context.Context, base, head string) ([]string, error) {
	out, err := gitOutput(ctx, r.dir, "diff", "--no-renames", "--name-only", "-z", base, head, "--")
	if err != nil {
		return nil, err
	}
	fields := bytes.Split(out, []byte{0})
	files := make([]string, 0, len(fields))
	for _, field := range fields {
		if len(field) != 0 {
			files = append(files, string(field))
		}
	}
	sort.Strings(files)
	return files, nil
}

func (r *Repository) LineChanges(ctx context.Context, base, head string) ([]LineChange, error) {
	out, err := gitOutput(ctx, r.dir, "diff", "--no-renames", "--diff-algorithm=myers", "--no-indent-heuristic", "--unified=0", "--no-color", "--no-ext-diff", "--no-textconv", "--src-prefix=a/", "--dst-prefix=b/", base, head, "--")
	if err != nil {
		return nil, err
	}
	return parseLineChanges(out)
}

func parseLineChanges(patch []byte) ([]LineChange, error) {
	changes := []LineChange{}
	oldFile := ""
	file := ""
	header := false
	for len(patch) != 0 {
		line := patch
		if end := bytes.IndexByte(patch, '\n'); end >= 0 {
			line = patch[:end]
			patch = patch[end+1:]
		} else {
			patch = nil
		}
		switch {
		case bytes.HasPrefix(line, []byte("diff --git ")):
			oldFile = ""
			file = ""
			header = true
		case header && bytes.HasPrefix(line, []byte("--- ")):
			parsed, parseErr := patchPath(string(line[len("--- "):]), "a/")
			if parseErr != nil {
				return nil, parseErr
			}
			oldFile = parsed
		case header && bytes.HasPrefix(line, []byte("+++ ")):
			parsed, parseErr := patchPath(string(line[len("+++ "):]), "b/")
			if parseErr != nil {
				return nil, parseErr
			}
			file = parsed
			if file == "" {
				file = oldFile
			}
		case bytes.HasPrefix(line, []byte("@@ -")):
			header = false
			beforeStart, beforeCount, afterStart, afterCount, parseErr := parseHunkHeader(string(line))
			if parseErr != nil {
				return nil, parseErr
			}
			if file == "" {
				return nil, fmt.Errorf("parse git diff hunk without a file: %q", line)
			}
			changes = append(changes, LineChange{File: file, BeforeStart: beforeStart, BeforeCount: beforeCount, AfterStart: afterStart, AfterCount: afterCount})
		}
	}
	return changes, nil
}

func patchPath(value, prefix string) (string, error) {
	if value == "/dev/null" {
		return "", nil
	}
	if strings.HasPrefix(value, "\"") {
		decoded, err := strconv.Unquote(value)
		if err != nil {
			return "", fmt.Errorf("parse quoted git diff path %q: %w", value, err)
		}
		value = decoded
	}
	if !strings.HasPrefix(value, prefix) {
		return "", fmt.Errorf("parse git diff path %q without prefix %q", value, prefix)
	}
	return strings.TrimPrefix(value, prefix), nil
}

func parseHunkHeader(header string) (int, int, int, int, error) {
	fields := strings.Fields(header)
	if len(fields) < 4 || fields[0] != "@@" || fields[3] != "@@" {
		return 0, 0, 0, 0, fmt.Errorf("parse git diff hunk header %q", header)
	}
	beforeStart, beforeCount, err := parseHunkRange(fields[1], '-')
	if err != nil {
		return 0, 0, 0, 0, fmt.Errorf("parse git diff hunk header %q: %w", header, err)
	}
	afterStart, afterCount, err := parseHunkRange(fields[2], '+')
	if err != nil {
		return 0, 0, 0, 0, fmt.Errorf("parse git diff hunk header %q: %w", header, err)
	}
	return beforeStart, beforeCount, afterStart, afterCount, nil
}

func parseHunkRange(value string, prefix byte) (int, int, error) {
	if len(value) < 2 || value[0] != prefix {
		return 0, 0, fmt.Errorf("invalid range %q", value)
	}
	parts := strings.SplitN(value[1:], ",", 2)
	start, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, fmt.Errorf("invalid range %q", value)
	}
	count := 1
	if len(parts) == 2 {
		count, err = strconv.Atoi(parts[1])
		if err != nil {
			return 0, 0, fmt.Errorf("invalid range %q", value)
		}
	}
	return start, count, nil
}

func (r *Repository) FirstParentMerges(ctx context.Context, rev string, limit int) ([]Merge, error) {
	return r.firstParentHistory(ctx, rev, limit, true)
}

func (r *Repository) FirstParentCommits(ctx context.Context, rev string) ([]Commit, error) {
	return r.firstParentHistory(ctx, rev, 0, false)
}

func (r *Repository) firstParentHistory(ctx context.Context, rev string, limit int, mergesOnly bool) ([]Commit, error) {
	args := []string{"log", "--first-parent"}
	if mergesOnly {
		args = append(args, "--merges")
	}
	args = append(args, "-z", "--format=%H%x00%cI")
	if limit > 0 {
		args = append(args, "--max-count="+strconv.Itoa(limit))
	}
	args = append(args, rev)
	out, err := gitOutput(ctx, r.dir, args...)
	if err != nil {
		return nil, err
	}
	fields := bytes.Split(out, []byte{0})
	commits := make([]Commit, 0, len(fields)/logFieldCount)
	for len(fields) > 0 && len(fields[len(fields)-1]) == 0 {
		fields = fields[:len(fields)-1]
	}
	if len(fields)%logFieldCount != 0 {
		return nil, fmt.Errorf("git log returned an invalid first-parent history record")
	}
	for i := 0; i < len(fields); i += logFieldCount {
		revision := string(fields[i+logRevisionField])
		date, err := time.Parse(time.RFC3339, string(fields[i+logDateField]))
		if err != nil {
			return nil, fmt.Errorf("parse git commit date for %s: %w", revision, err)
		}
		commits = append(commits, Commit{Rev: revision, Date: date.Format(time.RFC3339)})
	}
	return commits, nil
}

func regularBlobMode(mode string) bool {
	return mode == gitRegularFileMode || mode == gitExecutableFileMode
}

func gitOutput(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

func pathSlash(path string) string {
	return filepath.ToSlash(path)
}
