package diff

import (
	"math"
	"sort"

	"github.com/SolenesInc/slopradar/internal/model"
)

type functionKey struct {
	file string
	name string
}

func Build(base, head model.Snapshot, touched []string) model.Diff {
	touched = append([]string(nil), touched...)
	sort.Strings(touched)
	touchedSet := make(map[string]struct{}, len(touched))
	for _, file := range touched {
		touchedSet[file] = struct{}{}
	}

	result := model.Diff{
		Base: base.Rev, Head: head.Rev, Touched: touched,
		Buckets:   map[model.Bucket]model.BucketDelta{model.Source: {}, model.Tests: {}},
		Functions: []model.FunctionDelta{}, ClonesAdded: []model.ClonePair{}, ClonesRemoved: []model.ClonePair{}, Trend: []model.TrendPoint{},
	}
	for _, bucket := range []model.Bucket{model.Source, model.Tests} {
		before := base.Buckets[bucket]
		after := head.Buckets[bucket]
		result.Buckets[bucket] = model.BucketDelta{
			ErosionBefore: before.Erosion, ErosionAfter: after.Erosion,
			CloneShareBefore: before.CloneShare, CloneShareAfter: after.CloneShare,
		}
	}

	baseFunctions := groupFunctions(base.Functions, touchedSet)
	headFunctions := groupFunctions(head.Functions, touchedSet)
	keys := make([]functionKey, 0, len(baseFunctions)+len(headFunctions))
	seen := map[functionKey]struct{}{}
	for key := range baseFunctions {
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	for key := range headFunctions {
		if _, ok := seen[key]; ok {
			continue
		}
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].file != keys[j].file {
			return keys[i].file < keys[j].file
		}
		return keys[i].name < keys[j].name
	})
	for _, key := range keys {
		before := baseFunctions[key]
		after := headFunctions[key]
		count := max(len(before), len(after))
		for i := 0; i < count; i++ {
			var baseFunction, headFunction *model.Function
			if i < len(before) {
				item := before[i]
				baseFunction = &item
			}
			if i < len(after) {
				item := after[i]
				headFunction = &item
			}
			if equalFunctionMetrics(baseFunction, headFunction) {
				continue
			}
			delta := functionDelta(key, baseFunction, headFunction)
			result.Functions = append(result.Functions, delta)
			addFunctionMass(result.Buckets, delta)
		}
	}
	sort.Slice(result.Functions, func(i, j int) bool {
		left := math.Abs(result.Functions[i].DeltaMass)
		right := math.Abs(result.Functions[j].DeltaMass)
		if left != right {
			return left > right
		}
		if result.Functions[i].File != result.Functions[j].File {
			return result.Functions[i].File < result.Functions[j].File
		}
		if result.Functions[i].Name != result.Functions[j].Name {
			return result.Functions[i].Name < result.Functions[j].Name
		}
		return functionLine(result.Functions[i]) < functionLine(result.Functions[j])
	})

	result.ClonesAdded, result.ClonesRemoved = cloneChanges(base.Clones, head.Clones, touchedSet)
	baseCloneLines := touchedCloneLines(base.CloneCoverage, touchedSet)
	headCloneLines := touchedCloneLines(head.CloneCoverage, touchedSet)
	for _, bucket := range []model.Bucket{model.Source, model.Tests} {
		delta := result.Buckets[bucket]
		delta.CloneLinesTouchedBefore = baseCloneLines[bucket]
		delta.CloneLinesTouchedAfter = headCloneLines[bucket]
		result.Buckets[bucket] = delta
	}
	return result
}

func groupFunctions(functions []model.Function, touched map[string]struct{}) map[functionKey][]model.Function {
	groups := map[functionKey][]model.Function{}
	for _, function := range functions {
		if _, ok := touched[function.File]; !ok {
			continue
		}
		key := functionKey{file: function.File, name: function.Name}
		groups[key] = append(groups[key], function)
	}
	for key := range groups {
		sort.Slice(groups[key], func(i, j int) bool {
			if groups[key][i].Line != groups[key][j].Line {
				return groups[key][i].Line < groups[key][j].Line
			}
			if groups[key][i].CC != groups[key][j].CC {
				return groups[key][i].CC < groups[key][j].CC
			}
			return groups[key][i].SLOC < groups[key][j].SLOC
		})
	}
	return groups
}

func equalFunctionMetrics(before, after *model.Function) bool {
	return before != nil && after != nil && before.CC == after.CC && before.SLOC == after.SLOC && before.Mass == after.Mass && functionBucket(*before) == functionBucket(*after)
}

func functionDelta(key functionKey, before, after *model.Function) model.FunctionDelta {
	delta := model.FunctionDelta{File: key.file, Name: key.name, Before: before, After: after}
	if before != nil {
		delta.DeltaMass -= before.Mass
	}
	if after != nil {
		delta.DeltaMass += after.Mass
	}
	switch {
	case before == nil:
		delta.Note = "new"
	case after == nil:
		delta.Note = "removed"
	case before.CC <= 10 && after.CC > 10:
		delta.Note = "crossed CC 10"
	case before.CC > 10 && after.CC <= 10:
		delta.Note = "back under CC 10"
	}
	return delta
}

func addFunctionMass(buckets map[model.Bucket]model.BucketDelta, function model.FunctionDelta) {
	if function.Before != nil && function.After != nil && functionBucket(*function.Before) != functionBucket(*function.After) {
		if function.After.CC > 10 {
			bucket := functionBucket(*function.After)
			delta := buckets[bucket]
			delta.MassAddedOverCC10 += function.After.Mass
			buckets[bucket] = delta
		}
		if function.Before.CC > 10 {
			bucket := functionBucket(*function.Before)
			delta := buckets[bucket]
			delta.MassRemovedOverCC10 += function.Before.Mass
			buckets[bucket] = delta
		}
		return
	}
	if function.After != nil && function.After.CC > 10 && function.DeltaMass > 0 {
		bucket := functionBucket(*function.After)
		delta := buckets[bucket]
		delta.MassAddedOverCC10 += function.DeltaMass
		buckets[bucket] = delta
	}
	if function.Before != nil && function.Before.CC > 10 && function.DeltaMass < 0 {
		bucket := functionBucket(*function.Before)
		delta := buckets[bucket]
		delta.MassRemovedOverCC10 += -function.DeltaMass
		buckets[bucket] = delta
	}
}

func functionBucket(function model.Function) model.Bucket {
	if function.Bucket == "" {
		return model.Source
	}
	return function.Bucket
}

func functionLine(delta model.FunctionDelta) int {
	if delta.After != nil {
		return delta.After.Line
	}
	return delta.Before.Line
}

func cloneChanges(base, head []model.ClonePair, touched map[string]struct{}) ([]model.ClonePair, []model.ClonePair) {
	baseGroups := groupClones(base)
	headGroups := groupClones(head)
	ids := make([]string, 0, len(baseGroups)+len(headGroups))
	seen := map[string]struct{}{}
	for id := range baseGroups {
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	for id := range headGroups {
		if _, ok := seen[id]; !ok {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	added := []model.ClonePair{}
	removed := []model.ClonePair{}
	for _, id := range ids {
		before := baseGroups[id]
		after := headGroups[id]
		shared := min(len(before), len(after))
		for _, pair := range after[shared:] {
			if cloneTouches(pair, touched) {
				added = append(added, pair)
			}
		}
		for _, pair := range before[shared:] {
			if cloneTouches(pair, touched) {
				removed = append(removed, pair)
			}
		}
	}
	return added, removed
}

func groupClones(pairs []model.ClonePair) map[string][]model.ClonePair {
	groups := map[string][]model.ClonePair{}
	for _, pair := range pairs {
		groups[pair.ID] = append(groups[pair.ID], pair)
	}
	for id := range groups {
		sort.Slice(groups[id], func(i, j int) bool { return cloneLess(groups[id][i], groups[id][j]) })
	}
	return groups
}

func cloneLess(left, right model.ClonePair) bool {
	if left.ID != right.ID {
		return left.ID < right.ID
	}
	if left.A.File != right.A.File {
		return left.A.File < right.A.File
	}
	if left.A.Start != right.A.Start {
		return left.A.Start < right.A.Start
	}
	if left.B.File != right.B.File {
		return left.B.File < right.B.File
	}
	return left.B.Start < right.B.Start
}

func cloneTouches(pair model.ClonePair, touched map[string]struct{}) bool {
	_, a := touched[pair.A.File]
	_, b := touched[pair.B.File]
	return a || b
}

func touchedCloneLines(coverage []model.CloneCoverage, touched map[string]struct{}) map[model.Bucket]int {
	totals := map[model.Bucket]int{model.Source: 0, model.Tests: 0}
	for _, item := range coverage {
		if _, ok := touched[item.File]; ok {
			totals[item.Bucket] += len(item.Lines)
		}
	}
	return totals
}
