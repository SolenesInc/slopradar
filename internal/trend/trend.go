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

type Commit struct {
	gitread.Commit
	AxisLabel string
}

func Merges(ctx context.Context, repository *gitread.Repository, head string, count int) ([]Commit, error) {
	if count <= 0 {
		return nil, fmt.Errorf("merges must be greater than zero, got %d", count)
	}
	commits, err := repository.FirstParentMerges(ctx, head, count)
	if err != nil {
		return nil, err
	}
	reverse(commits)
	selected := make([]Commit, len(commits))
	for i, commit := range commits {
		selected[i] = Commit{Commit: commit}
	}
	return selected, nil
}

func Months(ctx context.Context, repository *gitread.Repository, head string, count int) ([]Commit, error) {
	if count <= 0 {
		return nil, fmt.Errorf("months must be greater than zero, got %d", count)
	}
	commits, err := repository.FirstParentCommits(ctx, head)
	if err != nil {
		return nil, err
	}
	if len(commits) == 0 {
		return []Commit{}, nil
	}
	type datedCommit struct {
		commit           gitread.Commit
		date             time.Time
		firstParentOrder int
	}
	dated := make([]datedCommit, len(commits))
	for i, commit := range commits {
		date, err := time.Parse(time.RFC3339, commit.Date)
		if err != nil {
			return nil, fmt.Errorf("parse commit date %q for %s: %w", commit.Date, commit.Rev, err)
		}
		dated[i] = datedCommit{commit: commit, date: date, firstParentOrder: i}
	}
	sort.Slice(dated, func(i, j int) bool {
		if !dated[i].date.Equal(dated[j].date) {
			return dated[i].date.Before(dated[j].date)
		}
		return dated[i].firstParentOrder > dated[j].firstParentOrder
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
	selected := make([]Commit, 0, boundaryCount)
	seen := map[string]int{}
	for boundary := firstBoundary; !boundary.After(currentBoundary); boundary = boundary.AddDate(0, 1, 0) {
		nextBoundary := boundary.AddDate(0, 1, 0)
		for i := len(dated) - 1; i >= 0; i-- {
			item := dated[i]
			if !item.date.Before(nextBoundary) {
				continue
			}
			if _, ok := seen[item.commit.Rev]; !ok {
				seen[item.commit.Rev] = len(selected)
				selected = append(selected, Commit{Commit: item.commit, AxisLabel: boundary.Format("2006-01")})
			}
			break
		}
	}
	headIndex, ok := seen[headCommit.Rev]
	if !ok {
		headIndex = len(selected)
		selected = append(selected, Commit{Commit: headCommit})
	}
	if !headDate.UTC().Equal(currentBoundary) {
		selected[headIndex].AxisLabel = "head"
	}
	return selected, nil
}

func Build(ctx context.Context, commits []Commit, scan Scanner) ([]model.TrendPoint, error) {
	points := make([]model.TrendPoint, 0, len(commits))
	foundSource := false
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
		foundSource = foundSource || buckets[model.Source].SourceLines != 0 || buckets[model.Tests].SourceLines != 0
		if foundSource {
			points = append(points, model.TrendPoint{Rev: commit.Rev, Date: commit.Date, Buckets: buckets, AxisLabel: commit.AxisLabel})
		}
	}
	return points, nil
}

func reverse[T any](items []T) {
	for left, right := 0, len(items)-1; left < right; left, right = left+1, right-1 {
		items[left], items[right] = items[right], items[left]
	}
}
