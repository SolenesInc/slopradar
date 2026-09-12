package trend

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/SolenesInc/slopradar/internal/gitread"
	"github.com/SolenesInc/slopradar/internal/model"
)

type Scanner func(context.Context, string) (model.Snapshot, error)

func Merges(ctx context.Context, repository *gitread.Repository, head string, count int) ([]gitread.Commit, error) {
	if count <= 0 {
		return nil, fmt.Errorf("merges must be greater than zero, got %d", count)
	}
	commits, err := repository.FirstParentMerges(ctx, head, count)
	if err != nil {
		return nil, err
	}
	reverse(commits)
	return commits, nil
}

func Months(ctx context.Context, repository *gitread.Repository, head string, count int) ([]gitread.Commit, error) {
	if count <= 0 {
		return nil, fmt.Errorf("months must be greater than zero, got %d", count)
	}
	commits, err := repository.FirstParentCommits(ctx, head)
	if err != nil {
		return nil, err
	}
	if len(commits) == 0 {
		return []gitread.Commit{}, nil
	}
	type datedCommit struct {
		commit gitread.Commit
		date   time.Time
	}
	dated := make([]datedCommit, len(commits))
	for i, commit := range commits {
		date, err := time.Parse(time.RFC3339, commit.Date)
		if err != nil {
			return nil, fmt.Errorf("parse commit date %q for %s: %w", commit.Date, commit.Rev, err)
		}
		dated[i] = datedCommit{commit: commit, date: date}
	}
	sort.Slice(dated, func(i, j int) bool {
		if !dated[i].date.Equal(dated[j].date) {
			return dated[i].date.Before(dated[j].date)
		}
		return dated[i].commit.Rev < dated[j].commit.Rev
	})
	headCommit := commits[0]
	headDate, err := time.Parse(time.RFC3339, headCommit.Date)
	if err != nil {
		return nil, fmt.Errorf("parse head date %q for %s: %w", headCommit.Date, headCommit.Rev, err)
	}
	currentBoundary := time.Date(headDate.UTC().Year(), headDate.UTC().Month(), 1, 0, 0, 0, 0, time.UTC)
	earliest := dated[0].date.UTC()
	availableMonths := (currentBoundary.Year()-earliest.Year())*12 + int(currentBoundary.Month()-earliest.Month()) + 1
	availableMonths = max(availableMonths, 1)
	boundaryCount := min(count, availableMonths)
	firstBoundary := currentBoundary.AddDate(0, -(boundaryCount - 1), 0)
	selected := make([]gitread.Commit, 0, boundaryCount+1)
	seen := map[string]struct{}{}
	for boundary := firstBoundary; !boundary.After(currentBoundary); boundary = boundary.AddDate(0, 1, 0) {
		for _, item := range dated {
			if item.date.Before(boundary) {
				continue
			}
			if _, ok := seen[item.commit.Rev]; !ok {
				selected = append(selected, item.commit)
				seen[item.commit.Rev] = struct{}{}
			}
			break
		}
	}
	if _, ok := seen[headCommit.Rev]; !ok {
		selected = append(selected, headCommit)
	}
	return selected, nil
}

func Build(ctx context.Context, commits []gitread.Commit, scan Scanner) ([]model.TrendPoint, error) {
	points := make([]model.TrendPoint, 0, len(commits))
	for _, commit := range commits {
		snapshot, err := scan(ctx, commit.Rev)
		if err != nil {
			return nil, fmt.Errorf("scan %s: %w", commit.Rev, err)
		}
		if err := model.ValidateComplete(snapshot); err != nil {
			return nil, fmt.Errorf("scan %s: %w", commit.Rev, err)
		}
		buckets := map[model.Bucket]model.Totals{
			model.Source: snapshot.Buckets[model.Source],
			model.Tests:  snapshot.Buckets[model.Tests],
		}
		points = append(points, model.TrendPoint{Rev: snapshot.Rev, Date: commit.Date, Buckets: buckets})
	}
	return points, nil
}

func reverse[T any](items []T) {
	for left, right := 0, len(items)-1; left < right; left, right = left+1, right-1 {
		items[left], items[right] = items[right], items[left]
	}
}
