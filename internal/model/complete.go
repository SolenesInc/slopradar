package model

import (
	"fmt"
	"sort"
	"strings"
)

func ValidateComplete(snapshot Snapshot) error {
	if len(snapshot.Skipped) == 0 && len(snapshot.SkippedDetails) == 0 && len(snapshot.Warnings) == 0 {
		return nil
	}
	problems := make([]string, 0, len(snapshot.Skipped)+len(snapshot.SkippedDetails)+len(snapshot.Warnings))
	detailed := make(map[string]struct{}, len(snapshot.SkippedDetails))
	for _, skipped := range snapshot.SkippedDetails {
		detailed[skipped.File] = struct{}{}
		problems = append(problems, fmt.Sprintf("file %q: max_file_bytes=%d, asked_bytes=%d", skipped.File, skipped.MaxBytes, skipped.AskedBytes))
	}
	for _, file := range snapshot.Skipped {
		if _, ok := detailed[file]; !ok {
			problems = append(problems, fmt.Sprintf("file %q: skipped without size details", file))
		}
	}
	problems = append(problems, snapshot.Warnings...)
	sort.Strings(problems)
	return fmt.Errorf("analysis of revision %q incomplete: %s", snapshot.Rev, strings.Join(problems, "; "))
}
