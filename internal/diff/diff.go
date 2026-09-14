package diff

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/SolenesInc/slopradar/internal/gitread"
	"github.com/SolenesInc/slopradar/internal/model"
)

type functionKey struct {
	file string
	name string
}

func Build(base, head model.Snapshot, touched []string) (model.Diff, error) {
	return BuildWithLineChanges(base, head, touched, nil)
}

func BuildWithLineChanges(base, head model.Snapshot, touched []string, lineChanges []gitread.LineChange) (model.Diff, error) {
	if err := model.ValidateComplete(base); err != nil {
		return model.Diff{}, fmt.Errorf("base snapshot: %w", err)
	}
	if err := model.ValidateComplete(head); err != nil {
		return model.Diff{}, fmt.Errorf("head snapshot: %w", err)
	}
	touched = analysisTouchedPaths(base, head, touched)
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

	mappings := newLineMappings(lineChanges, compatibleGitLineCoordinates(base.AnalysisPaths, head.AnalysisPaths))
	baseFunctions := groupFunctions(base.Functions, touchedSet)
	headFunctions := groupFunctions(head.Functions, touchedSet)
	for _, key := range functionKeys(baseFunctions, headFunctions) {
		for _, delta := range functionDeltas(key, baseFunctions[key], headFunctions[key], mappings) {
			result.Functions = append(result.Functions, delta)
			addFunctionMass(result.Buckets, delta)
		}
	}
	sortFunctionDeltas(result.Functions)

	result.ClonesAdded, result.ClonesRemoved = cloneChanges(base.Clones, head.Clones, touchedSet, mappings)
	baseCloneLines := touchedCloneLines(base.CloneCoverage, touchedSet)
	headCloneLines := touchedCloneLines(head.CloneCoverage, touchedSet)
	for _, bucket := range []model.Bucket{model.Source, model.Tests} {
		delta := result.Buckets[bucket]
		delta.CloneLinesTouchedBefore = baseCloneLines[bucket]
		delta.CloneLinesTouchedAfter = headCloneLines[bucket]
		result.Buckets[bucket] = delta
	}
	return result, nil
}

func sortFunctionDeltas(deltas []model.FunctionDelta) {
	sort.Slice(deltas, func(i, j int) bool {
		left := math.Abs(deltas[i].DeltaMass)
		right := math.Abs(deltas[j].DeltaMass)
		if left != right {
			return left > right
		}
		if deltas[i].File != deltas[j].File {
			return deltas[i].File < deltas[j].File
		}
		if deltas[i].Name != deltas[j].Name {
			return deltas[i].Name < deltas[j].Name
		}
		return functionLine(deltas[i]) < functionLine(deltas[j])
	})
}

func analysisTouchedPaths(base, head model.Snapshot, touched []string) []string {
	result := append([]string(nil), touched...)
	basePaths := analysisPathBuckets(base.AnalysisPaths)
	headPaths := analysisPathBuckets(head.AnalysisPaths)
	seen := make(map[string]struct{}, len(result)+len(basePaths)+len(headPaths))
	for _, file := range result {
		seen[file] = struct{}{}
	}
	for file, before := range basePaths {
		after, exists := headPaths[file]
		if exists && before == after {
			continue
		}
		if _, exists := seen[file]; !exists {
			seen[file] = struct{}{}
			result = append(result, file)
		}
	}
	for file := range headPaths {
		if _, existed := basePaths[file]; existed {
			continue
		}
		if _, exists := seen[file]; !exists {
			seen[file] = struct{}{}
			result = append(result, file)
		}
	}
	return result
}

func analysisPathBuckets(paths []model.AnalysisPath) map[string]model.Bucket {
	result := make(map[string]model.Bucket, len(paths))
	for _, item := range paths {
		result[item.File] = item.Bucket
	}
	return result
}

type functionMatch struct {
	before *model.Function
	after  *model.Function
}

func matchFunctions(before, after []model.Function, mappings lineMappings) []functionMatch {
	afterByLine := make(map[int][]int, len(after))
	for i, function := range after {
		afterByLine[function.Line] = append(afterByLine[function.Line], i)
	}
	matchedBefore := make([]bool, len(before))
	matchedAfter := make([]bool, len(after))
	matches := make([]functionMatch, 0, max(len(before), len(after)))
	match := func(i, j int) {
		matchedBefore[i], matchedAfter[j] = true, true
		matches = append(matches, functionMatch{&before[i], &after[j]})
	}
	for i, function := range before {
		line, mapped := mappings.line(function.File, function.Line)
		if !mapped {
			continue
		}
		candidates := afterByLine[line]
		if len(candidates) != 0 {
			match(i, candidates[0])
			afterByLine[line] = candidates[1:]
		}
	}
	afterBySignature := make(map[string][]int, len(after))
	for i, function := range after {
		if !matchedAfter[i] {
			signature := functionTreeSignature(function)
			afterBySignature[signature] = append(afterBySignature[signature], i)
		}
	}
	var remainingBefore, remainingAfter []int
	for i, function := range before {
		if matchedBefore[i] {
			continue
		}
		signature := functionTreeSignature(function)
		candidates := afterBySignature[signature]
		if len(candidates) == 0 {
			remainingBefore = append(remainingBefore, i)
			continue
		}
		match(i, candidates[0])
		afterBySignature[signature] = candidates[1:]
	}
	for i := range after {
		if !matchedAfter[i] {
			remainingAfter = append(remainingAfter, i)
		}
	}
	for i := 0; i < max(len(remainingBefore), len(remainingAfter)); i++ {
		var pair functionMatch
		if i < len(remainingBefore) {
			pair.before = &before[remainingBefore[i]]
		}
		if i < len(remainingAfter) {
			pair.after = &after[remainingAfter[i]]
		}
		matches = append(matches, pair)
	}
	return matches
}

func functionTreeSignature(function model.Function) string {
	nested := make([]string, len(function.Nested))
	for i, child := range function.Nested {
		nested[i] = functionTreeSignature(child)
	}
	sort.Strings(nested)
	var signature strings.Builder
	writeSignatureString(&signature, function.File)
	writeSignatureString(&signature, function.Name)
	writeSignatureString(&signature, string(functionBucket(function)))
	signature.WriteString(strconv.Itoa(function.CC))
	signature.WriteByte(':')
	signature.WriteString(strconv.Itoa(function.SLOC))
	signature.WriteByte(':')
	signature.WriteString(strconv.FormatUint(math.Float64bits(function.Mass), 16))
	signature.WriteByte(':')
	signature.WriteString(strconv.Itoa(len(nested)))
	signature.WriteByte(':')
	for _, child := range nested {
		writeSignatureString(&signature, child)
	}
	return signature.String()
}

func writeSignatureString(signature *strings.Builder, value string) {
	signature.WriteString(strconv.Itoa(len(value)))
	signature.WriteByte(':')
	signature.WriteString(value)
}

func functionDeltas(key functionKey, before, after []model.Function, mappings lineMappings) []model.FunctionDelta {
	deltas := make([]model.FunctionDelta, 0, max(len(before), len(after)))
	for _, pair := range matchFunctions(before, after, mappings) {
		delta := functionDelta(key, pair.before, pair.after, mappings)
		if pair.before != nil && pair.after != nil && len(delta.Nested) == 0 &&
			pair.before.CC == pair.after.CC && pair.before.SLOC == pair.after.SLOC &&
			pair.before.Mass == pair.after.Mass && functionBucket(*pair.before) == functionBucket(*pair.after) {
			continue
		}
		deltas = append(deltas, delta)
	}
	return deltas
}

func nestedFunctionDeltas(before, after *model.Function, mappings lineMappings) []model.FunctionDelta {
	var baseGroups, headGroups map[functionKey][]model.Function
	if before != nil {
		baseGroups = groupNestedFunctions(before.Nested)
	} else {
		baseGroups = map[functionKey][]model.Function{}
	}
	if after != nil {
		headGroups = groupNestedFunctions(after.Nested)
	} else {
		headGroups = map[functionKey][]model.Function{}
	}
	keys := functionKeys(baseGroups, headGroups)
	deltas := []model.FunctionDelta{}
	for _, key := range keys {
		deltas = append(deltas, functionDeltas(key, baseGroups[key], headGroups[key], mappings)...)
	}
	sortFunctionDeltas(deltas)
	return deltas
}

func groupNestedFunctions(functions []model.Function) map[functionKey][]model.Function {
	groups := map[functionKey][]model.Function{}
	for _, function := range functions {
		key := functionKey{file: function.File, name: function.Name}
		groups[key] = append(groups[key], function)
	}
	for key := range groups {
		sort.Slice(groups[key], func(i, j int) bool {
			return functionLess(groups[key][i], groups[key][j])
		})
	}
	return groups
}

func functionKeys(before, after map[functionKey][]model.Function) []functionKey {
	keys := make([]functionKey, 0, len(before)+len(after))
	seen := map[functionKey]struct{}{}
	for key := range before {
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	for key := range after {
		if _, exists := seen[key]; !exists {
			keys = append(keys, key)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].file != keys[j].file {
			return keys[i].file < keys[j].file
		}
		return keys[i].name < keys[j].name
	})
	return keys
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
			return functionLess(groups[key][i], groups[key][j])
		})
	}
	return groups
}

func functionLess(left, right model.Function) bool {
	if left.Line != right.Line {
		return left.Line < right.Line
	}
	if left.CC != right.CC {
		return left.CC < right.CC
	}
	return left.SLOC < right.SLOC
}

func functionDelta(key functionKey, before, after *model.Function, mappings lineMappings) model.FunctionDelta {
	delta := model.FunctionDelta{File: key.file, Name: key.name, Before: before, After: after}
	delta.Nested = nestedFunctionDeltas(before, after, mappings)
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
	case before.CC <= model.ErosionComplexityCutoff && after.CC > model.ErosionComplexityCutoff:
		delta.Note = fmt.Sprintf("crossed CC %d", model.ErosionComplexityCutoff)
	case before.CC > model.ErosionComplexityCutoff && after.CC <= model.ErosionComplexityCutoff:
		delta.Note = fmt.Sprintf("back under CC %d", model.ErosionComplexityCutoff)
	}
	return delta
}

func addFunctionMass(buckets map[model.Bucket]model.BucketDelta, function model.FunctionDelta) {
	if function.Before != nil && function.After != nil && functionBucket(*function.Before) != functionBucket(*function.After) {
		if function.After.CC > model.ErosionComplexityCutoff {
			bucket := functionBucket(*function.After)
			delta := buckets[bucket]
			delta.MassAddedOverCC10 += function.After.Mass
			buckets[bucket] = delta
		}
		if function.Before.CC > model.ErosionComplexityCutoff {
			bucket := functionBucket(*function.Before)
			delta := buckets[bucket]
			delta.MassRemovedOverCC10 += function.Before.Mass
			buckets[bucket] = delta
		}
		return
	}
	if function.After != nil && function.After.CC > model.ErosionComplexityCutoff && function.DeltaMass > 0 {
		bucket := functionBucket(*function.After)
		delta := buckets[bucket]
		delta.MassAddedOverCC10 += function.DeltaMass
		buckets[bucket] = delta
	}
	if function.Before != nil && function.Before.CC > model.ErosionComplexityCutoff && function.DeltaMass < 0 {
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

type mappedLineChange struct {
	beforeStart int
	beforeCount int
	boundary    int
	delta       int
}

type lineMappings struct {
	changes    map[string][]mappedLineChange
	compatible map[string]bool
	restricted bool
}

type clonePosition struct {
	aFile  string
	aStart int
	aEnd   int
	bFile  string
	bStart int
	bEnd   int
}

func newLineMappings(changes []gitread.LineChange, compatible map[string]bool) lineMappings {
	mappings := lineMappings{changes: map[string][]mappedLineChange{}, compatible: compatible, restricted: changes != nil}
	for _, change := range changes {
		boundary := change.BeforeStart
		if change.BeforeCount == 0 {
			boundary++
		}
		mappings.changes[change.File] = append(mappings.changes[change.File], mappedLineChange{
			beforeStart: change.BeforeStart,
			beforeCount: change.BeforeCount,
			boundary:    boundary,
			delta:       change.AfterCount - change.BeforeCount,
		})
	}
	for file := range mappings.changes {
		sort.Slice(mappings.changes[file], func(i, j int) bool { return mappings.changes[file][i].boundary < mappings.changes[file][j].boundary })
		delta := 0
		for i := range mappings.changes[file] {
			delta += mappings.changes[file][i].delta
			mappings.changes[file][i].delta = delta
		}
	}
	return mappings
}

func compatibleGitLineCoordinates(base, head []model.AnalysisPath) map[string]bool {
	before := map[string]bool{}
	for _, item := range base {
		before[item.File] = item.GitLineCoordinates
	}
	compatible := map[string]bool{}
	for _, item := range head {
		compatible[item.File] = item.GitLineCoordinates && before[item.File]
	}
	return compatible
}

func (mappings lineMappings) line(file string, before int) (int, bool) {
	if mappings.restricted && !mappings.compatible[file] {
		return 0, false
	}
	changes := mappings.changes[file]
	index := sort.Search(len(changes), func(i int) bool { return changes[i].boundary > before }) - 1
	if index < 0 {
		return before, true
	}
	change := changes[index]
	if change.beforeCount != 0 && before >= change.beforeStart && before < change.beforeStart+change.beforeCount {
		return 0, false
	}
	return before + change.delta, true
}

func (mappings lineMappings) clone(pair model.ClonePair) (clonePosition, bool) {
	aStart, aStartMapped := mappings.line(pair.A.File, pair.A.Start)
	aEnd, aEndMapped := mappings.line(pair.A.File, pair.A.End)
	bStart, bStartMapped := mappings.line(pair.B.File, pair.B.Start)
	bEnd, bEndMapped := mappings.line(pair.B.File, pair.B.End)
	return clonePosition{aFile: pair.A.File, aStart: aStart, aEnd: aEnd, bFile: pair.B.File, bStart: bStart, bEnd: bEnd}, aStartMapped && aEndMapped && bStartMapped && bEndMapped
}

func positionOf(pair model.ClonePair) clonePosition {
	return clonePosition{aFile: pair.A.File, aStart: pair.A.Start, aEnd: pair.A.End, bFile: pair.B.File, bStart: pair.B.Start, bEnd: pair.B.End}
}

func cloneChanges(base, head []model.ClonePair, touched map[string]struct{}, mappings lineMappings) ([]model.ClonePair, []model.ClonePair) {
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
		afterByPosition := map[clonePosition][]int{}
		for index, pair := range after {
			position := positionOf(pair)
			afterByPosition[position] = append(afterByPosition[position], index)
		}
		matchedAfter := make([]bool, len(after))
		unmatchedBefore := make([]model.ClonePair, 0, len(before))
		for _, pair := range before {
			position, mapped := mappings.clone(pair)
			candidates := afterByPosition[position]
			if !mapped || len(candidates) == 0 {
				unmatchedBefore = append(unmatchedBefore, pair)
				continue
			}
			matchedAfter[candidates[0]] = true
			afterByPosition[position] = candidates[1:]
		}
		unmatchedAfter := make([]model.ClonePair, 0, len(after))
		for index, pair := range after {
			if !matchedAfter[index] {
				unmatchedAfter = append(unmatchedAfter, pair)
			}
		}
		shared := min(len(unmatchedBefore), len(unmatchedAfter))
		for _, pair := range unmatchedAfter[shared:] {
			if cloneTouches(pair, touched) {
				added = append(added, pair)
			}
		}
		for _, pair := range unmatchedBefore[shared:] {
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
