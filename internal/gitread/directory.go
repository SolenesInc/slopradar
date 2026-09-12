package gitread

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

func ReadDirectory(root string) ([]Blob, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve directory %q: %w", root, err)
	}
	blobs := []Blob{}
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		blobs = append(blobs, Blob{
			BlobInfo: BlobInfo{Path: pathSlash(rel), Size: int64(len(content))},
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
