package gitread

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

func ReadDirectory(root string) ([]Blob, error) {
	return ReadDirectoryFiltered(root, nil)
}

type DirectoryFilter func(path string, size int64, directory bool) bool

func ReadDirectoryFiltered(root string, filter DirectoryFilter) ([]Blob, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve directory %q: %w", root, err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("resolve directory symlinks %q: %w", root, err)
	}
	blobs := []Blob{}
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = pathSlash(rel)
		if entry.IsDir() {
			if filter != nil && !filter(rel, 0, true) {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if filter != nil && !filter(rel, info.Size(), false) {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		blobs = append(blobs, Blob{
			BlobInfo: BlobInfo{Path: rel, Size: int64(len(content))},
			Content:  content,
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read directory %q: %w", root, err)
	}
	sort.Slice(blobs, func(i, j int) bool { return blobs[i].Path < blobs[j].Path })
	return blobs, nil
}
