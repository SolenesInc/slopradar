package clones

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"hash/fnv"
	"math/bits"
	"sort"

	"github.com/SolenesInc/slopradar/internal/lang"
	"github.com/SolenesInc/slopradar/internal/model"
)

const (
	JscpdDefaultMinimumTokens = 50
	JscpdDefaultMinimumLines  = 5
	fnv64PrimeRabinKarpBase   = uint64(1099511628211)
)

type File struct {
	Path        string
	Language    string
	Tokens      []lang.Token
	SourceLines map[model.Bucket][]int
}

type Result struct {
	Pairs    []model.ClonePair
	Coverage []model.CloneCoverage
	Lines    map[string]map[model.Bucket][]int
	Total    map[model.Bucket]int
}

type segment struct {
	file   int
	tokens []lang.Token
}

type occurrence struct {
	segment int
	start   int
}

type occurrencePair struct {
	left  occurrence
	right occurrence
}

type exactWindowGroup struct {
	occurrences []occurrence
}

type predecessorGroup struct {
	occurrences []occurrence
}

type rankPair struct {
	left  int
	right int
}

type lexicalIndex struct {
	ranks  [][][]int
	active []bool
}

type sequenceKey struct {
	length int
	left   int
	right  int
}

type pairSequenceKey struct {
	fileA    string
	fileB    string
	sequence sequenceKey
}

type pairIDRequest struct {
	pairIndex int
	fileA     string
	fileB     string
	sequence  extent
}

type extent struct {
	segment int
	start   int
	end     int
}

type extentPair struct {
	a extent
	b extent
}

func Detect(input []File) Result {
	files := normalizeFiles(input)
	segments := buildSegments(files)
	byLanguage := map[string][]int{}
	for i, item := range segments {
		byLanguage[files[item.file].Language] = append(byLanguage[files[item.file].Language], i)
	}
	languages := make([]string, 0, len(byLanguage))
	for language := range byLanguage {
		languages = append(languages, language)
	}
	sort.Strings(languages)
	extents, index := maximalPairs(segments, byLanguage, languages)
	return buildResult(files, segments, extents, index)
}

func normalizeFiles(input []File) []File {
	files := append([]File(nil), input...)
	for i := range files {
		lines := map[model.Bucket][]int{model.Source: {}, model.Tests: {}}
		for _, bucket := range []model.Bucket{model.Source, model.Tests} {
			lines[bucket] = append(lines[bucket], files[i].SourceLines[bucket]...)
			sort.Ints(lines[bucket])
		}
		files[i].SourceLines = lines
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].Path != files[j].Path {
			return files[i].Path < files[j].Path
		}
		return files[i].Language < files[j].Language
	})
	return files
}

func buildResult(files []File, segments []segment, extents []extentPair, index lexicalIndex) Result {
	result := Result{
		Pairs:    []model.ClonePair{},
		Coverage: []model.CloneCoverage{},
		Lines:    map[string]map[model.Bucket][]int{},
		Total:    map[model.Bucket]int{model.Source: 0, model.Tests: 0},
	}
	cloneRanges := map[string][]model.Range{}
	for _, file := range files {
		cloneRanges[file.Path] = []model.Range{}
	}

	ids := pairIDs(files, segments, extents, index)
	for pairIndex, pair := range extents {
		clonePair := makePair(files, segments, pair, ids[pairIndex])
		if clonePair.Lines < JscpdDefaultMinimumLines {
			continue
		}
		result.Pairs = append(result.Pairs, clonePair)
		cloneRanges[clonePair.A.File] = append(cloneRanges[clonePair.A.File], clonePair.A)
		cloneRanges[clonePair.B.File] = append(cloneRanges[clonePair.B.File], clonePair.B)
	}

	sort.Slice(result.Pairs, func(i, j int) bool { return pairLess(result.Pairs[i], result.Pairs[j]) })
	for _, file := range files {
		if _, exists := result.Lines[file.Path]; exists {
			continue
		}
		result.Lines[file.Path] = map[model.Bucket][]int{model.Source: {}, model.Tests: {}}
		ranges := unionRanges(cloneRanges[file.Path])
		for _, bucket := range []model.Bucket{model.Source, model.Tests} {
			result.Lines[file.Path][bucket] = linesInRanges(file.SourceLines[bucket], ranges)
			result.Total[bucket] += len(result.Lines[file.Path][bucket])
		}
		if len(result.Lines[file.Path][model.Source])+len(result.Lines[file.Path][model.Tests]) != 0 {
			for _, bucket := range []model.Bucket{model.Source, model.Tests} {
				if len(result.Lines[file.Path][bucket]) != 0 {
					result.Coverage = append(result.Coverage, model.CloneCoverage{File: file.Path, Bucket: bucket, Lines: result.Lines[file.Path][bucket]})
				}
			}
		}
	}
	return result
}

func unionRanges(ranges []model.Range) []model.Range {
	if len(ranges) == 0 {
		return nil
	}
	ranges = append([]model.Range(nil), ranges...)
	sort.Slice(ranges, func(i, j int) bool {
		if ranges[i].Start != ranges[j].Start {
			return ranges[i].Start < ranges[j].Start
		}
		return ranges[i].End < ranges[j].End
	})
	union := []model.Range{ranges[0]}
	for _, item := range ranges[1:] {
		last := &union[len(union)-1]
		if item.Start <= last.End {
			last.End = max(last.End, item.End)
			continue
		}
		union = append(union, item)
	}
	return union
}

func linesInRanges(sourceLines []int, ranges []model.Range) []int {
	lines := []int{}
	rangeIndex := 0
	for _, line := range sourceLines {
		for rangeIndex < len(ranges) && ranges[rangeIndex].End < line {
			rangeIndex++
		}
		if rangeIndex == len(ranges) {
			return lines
		}
		if ranges[rangeIndex].Start <= line {
			lines = append(lines, line)
		}
	}
	return lines
}

func maximalPairs(segments []segment, byLanguage map[string][]int, languages []string) ([]extentPair, lexicalIndex) {
	candidates := []occurrencePair{}
	indexedSegments := make([]bool, len(segments))
	for _, language := range languages {
		windows := exactWindows(segments, byLanguage[language])
		for _, groups := range windows {
			for _, group := range groups {
				groupCandidates := leftMaximalPairs(segments, group.occurrences)
				markSharedExtensionSegments(groupCandidates, indexedSegments)
				candidates = append(candidates, groupCandidates...)
			}
		}
	}
	index := newLexicalIndex(segments, indexedSegments)
	seen := map[extentPair]struct{}{}
	pairs := []extentPair{}
	for _, candidate := range candidates {
		var pair extentPair
		var ok bool
		if index.active[candidate.left.segment] && index.active[candidate.right.segment] {
			pair, ok = leftMaximalPair(segments, index, candidate.left, candidate.right)
		} else {
			pair, ok = maximalPair(segments, candidate.left, candidate.right)
		}
		if !ok {
			continue
		}
		if _, exists := seen[pair]; exists {
			continue
		}
		seen[pair] = struct{}{}
		pairs = append(pairs, pair)
	}
	return pairs, index
}

func markSharedExtensionSegments(pairs []occurrencePair, marked []bool) {
	uses := map[occurrence]int{}
	for _, pair := range pairs {
		uses[pair.left]++
		uses[pair.right]++
	}
	for _, pair := range pairs {
		if uses[pair.left] == 1 && uses[pair.right] == 1 {
			continue
		}
		marked[pair.left.segment] = true
		marked[pair.right.segment] = true
	}
}

func exactWindows(segments []segment, segmentIndexes []int) map[uint64][]exactWindowGroup {
	windows := map[uint64][]exactWindowGroup{}
	for _, segmentIndex := range segmentIndexes {
		item := segments[segmentIndex]
		if len(item.tokens) < JscpdDefaultMinimumTokens {
			continue
		}
		hash, power := firstWindow(item.tokens)
		windows[hash] = addExactWindow(segments, windows[hash], occurrence{segment: segmentIndex})
		for start := 1; start+JscpdDefaultMinimumTokens <= len(item.tokens); start++ {
			hash = nextWindow(hash, tokenHash(item.tokens[start-1].Text), tokenHash(item.tokens[start+JscpdDefaultMinimumTokens-1].Text), power)
			windows[hash] = addExactWindow(segments, windows[hash], occurrence{segment: segmentIndex, start: start})
		}
	}
	return windows
}

func newLexicalIndex(segments []segment, active []bool) lexicalIndex {
	textRanks := map[string]int{}
	base := make([][]int, len(segments))
	maxTokens := 0
	for segmentIndex, item := range segments {
		if !active[segmentIndex] {
			continue
		}
		base[segmentIndex] = make([]int, len(item.tokens))
		maxTokens = max(maxTokens, len(item.tokens))
		for tokenIndex, token := range item.tokens {
			rank, exists := textRanks[token.Text]
			if !exists {
				rank = len(textRanks) + 1
				textRanks[token.Text] = rank
			}
			base[segmentIndex][tokenIndex] = rank
		}
	}
	index := lexicalIndex{ranks: [][][]int{base}, active: active}
	for width := 2; width <= maxTokens; width *= 2 {
		half := width / 2
		pairRanks := map[rankPair]int{}
		level := make([][]int, len(segments))
		for segmentIndex, item := range segments {
			if !active[segmentIndex] {
				continue
			}
			count := len(item.tokens) - width + 1
			if count <= 0 {
				continue
			}
			level[segmentIndex] = make([]int, count)
			previous := index.ranks[len(index.ranks)-1][segmentIndex]
			for start := range level[segmentIndex] {
				key := rankPair{left: previous[start], right: previous[start+half]}
				rank, exists := pairRanks[key]
				if !exists {
					rank = len(pairRanks) + 1
					pairRanks[key] = rank
				}
				level[segmentIndex][start] = rank
			}
		}
		index.ranks = append(index.ranks, level)
	}
	return index
}

func (index lexicalIndex) commonPrefix(segments []segment, a, b occurrence) int {
	matched := 0
	for level := len(index.ranks) - 1; level >= 0; level-- {
		width := 1 << level
		aStart := a.start + matched
		bStart := b.start + matched
		if aStart+width > len(segments[a.segment].tokens) || bStart+width > len(segments[b.segment].tokens) {
			continue
		}
		if index.ranks[level][a.segment][aStart] == index.ranks[level][b.segment][bStart] {
			matched += width
		}
	}
	return matched
}

func (index lexicalIndex) sequence(item extent) sequenceKey {
	length := item.end - item.start
	level := bits.Len(uint(length)) - 1
	width := 1 << level
	return sequenceKey{
		length: length,
		left:   index.ranks[level][item.segment][item.start],
		right:  index.ranks[level][item.segment][item.end-width],
	}
}

func addExactWindow(segments []segment, groups []exactWindowGroup, item occurrence) []exactWindowGroup {
	for index := range groups {
		representative := groups[index].occurrences[0]
		if equalWindow(segments[item.segment].tokens[item.start:], segments[representative.segment].tokens[representative.start:]) {
			groups[index].occurrences = append(groups[index].occurrences, item)
			return groups
		}
	}
	return append(groups, exactWindowGroup{occurrences: []occurrence{item}})
}

func leftMaximalPairs(segments []segment, occurrences []occurrence) []occurrencePair {
	groups := []predecessorGroup{}
	byPredecessor := map[string]int{}
	boundary := -1
	for _, item := range occurrences {
		if item.start == 0 {
			if boundary < 0 {
				boundary = len(groups)
				groups = append(groups, predecessorGroup{})
			}
			groups[boundary].occurrences = append(groups[boundary].occurrences, item)
			continue
		}
		text := segments[item.segment].tokens[item.start-1].Text
		index, exists := byPredecessor[text]
		if !exists {
			index = len(groups)
			byPredecessor[text] = index
			groups = append(groups, predecessorGroup{})
		}
		groups[index].occurrences = append(groups[index].occurrences, item)
	}
	pairs := []occurrencePair{}
	visit := func(left, right occurrence) {
		pairs = append(pairs, occurrencePair{left: left, right: right})
	}
	if boundary >= 0 {
		visitPairsWithin(groups[boundary].occurrences, visit)
	}
	for left := range groups {
		for right := left + 1; right < len(groups); right++ {
			visitPairsAcross(groups[left].occurrences, groups[right].occurrences, visit)
		}
	}
	return pairs
}

func visitPairsWithin(items []occurrence, visit func(occurrence, occurrence)) {
	for left := range items {
		for right := left + 1; right < len(items); right++ {
			visit(items[left], items[right])
		}
	}
}

func visitPairsAcross(left, right []occurrence, visit func(occurrence, occurrence)) {
	for _, a := range left {
		for _, b := range right {
			visit(a, b)
		}
	}
}

func buildSegments(files []File) []segment {
	segments := make([]segment, 0, len(files))
	for fileIndex, file := range files {
		if len(file.Tokens) != 0 {
			segments = append(segments, segment{file: fileIndex, tokens: file.Tokens})
		}
	}
	return segments
}

func firstWindow(tokens []lang.Token) (uint64, uint64) {
	var hash uint64
	power := uint64(1)
	for i := 0; i < JscpdDefaultMinimumTokens; i++ {
		hash = hash*fnv64PrimeRabinKarpBase + tokenHash(tokens[i].Text)
		if i+1 < JscpdDefaultMinimumTokens {
			power *= fnv64PrimeRabinKarpBase
		}
	}
	return hash, power
}

func nextWindow(hash, removed, added, power uint64) uint64 {
	return (hash-removed*power)*fnv64PrimeRabinKarpBase + added
}

func tokenHash(text string) uint64 {
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(text))
	return hash.Sum64()
}

func equalWindow(a, b []lang.Token) bool {
	for i := 0; i < JscpdDefaultMinimumTokens; i++ {
		if a[i].Text != b[i].Text {
			return false
		}
	}
	return true
}

func maximalPair(segments []segment, a, b occurrence) (extentPair, bool) {
	aStart, bStart := a.start, b.start
	aTokens, bTokens := segments[a.segment].tokens, segments[b.segment].tokens
	for aStart > 0 && bStart > 0 && aTokens[aStart-1].Text == bTokens[bStart-1].Text {
		aStart--
		bStart--
	}
	aEnd, bEnd := a.start+JscpdDefaultMinimumTokens, b.start+JscpdDefaultMinimumTokens
	for aEnd < len(aTokens) && bEnd < len(bTokens) && aTokens[aEnd].Text == bTokens[bEnd].Text {
		aEnd++
		bEnd++
	}
	left := extent{segment: a.segment, start: aStart, end: aEnd}
	right := extent{segment: b.segment, start: bStart, end: bEnd}
	if extentLess(segments, right, left) {
		left, right = right, left
	}
	if left.segment == right.segment && left.end > right.start {
		return extentPair{}, false
	}
	return extentPair{a: left, b: right}, true
}

func leftMaximalPair(segments []segment, index lexicalIndex, a, b occurrence) (extentPair, bool) {
	length := index.commonPrefix(segments, a, b)
	if length < JscpdDefaultMinimumTokens {
		return extentPair{}, false
	}
	left := extent{segment: a.segment, start: a.start, end: a.start + length}
	right := extent{segment: b.segment, start: b.start, end: b.start + length}
	if extentLess(segments, right, left) {
		left, right = right, left
	}
	if left.segment == right.segment && left.end > right.start {
		return extentPair{}, false
	}
	return extentPair{a: left, b: right}, true
}

func extentLess(segments []segment, a, b extent) bool {
	if segments[a.segment].file != segments[b.segment].file {
		return segments[a.segment].file < segments[b.segment].file
	}
	if a.segment != b.segment {
		return a.segment < b.segment
	}
	return a.start < b.start
}

func makePair(files []File, segments []segment, pair extentPair, id string) model.ClonePair {
	a := makeRange(files, segments, pair.a)
	b := makeRange(files, segments, pair.b)
	lines := min(sourceLineCount(files[segments[pair.a.segment].file], a), sourceLineCount(files[segments[pair.b.segment].file], b))
	return model.ClonePair{ID: id, A: a, B: b, Tokens: pair.a.end - pair.a.start, Lines: lines}
}

func sourceLineCount(file File, lines model.Range) int {
	count := 0
	for _, bucket := range []model.Bucket{model.Source, model.Tests} {
		bucketLines := file.SourceLines[bucket]
		start := sort.SearchInts(bucketLines, lines.Start)
		end := sort.SearchInts(bucketLines, lines.End+1)
		count += end - start
	}
	return count
}

func makeRange(files []File, segments []segment, item extent) model.Range {
	tokens := segments[item.segment].tokens
	file := files[segments[item.segment].file]
	first := tokens[item.start]
	last := tokens[item.end-1]
	return model.Range{
		File:  file.Path,
		Start: first.Line,
		End:   last.Line + lang.CountLineTerminators([]byte(last.Text), file.Language == "typescript" || file.Language == "javascript"),
	}
}

func pairIDs(files []File, segments []segment, pairs []extentPair, index lexicalIndex) []string {
	ids := make([]string, len(pairs))
	requests := make([]pairIDRequest, 0, len(pairs))
	for pairIndex, pair := range pairs {
		fileA := files[segments[pair.a.segment].file].Path
		fileB := files[segments[pair.b.segment].file].Path
		if fileB < fileA {
			fileA, fileB = fileB, fileA
		}
		if !index.active[pair.a.segment] {
			ids[pairIndex] = pairID(fileA, fileB, segments[pair.a.segment].tokens[pair.a.start:pair.a.end])
			continue
		}
		requests = append(requests, pairIDRequest{pairIndex: pairIndex, fileA: fileA, fileB: fileB, sequence: pair.a})
	}
	sort.Slice(requests, func(i, j int) bool {
		if requests[i].fileA != requests[j].fileA {
			return requests[i].fileA < requests[j].fileA
		}
		if requests[i].fileB != requests[j].fileB {
			return requests[i].fileB < requests[j].fileB
		}
		if requests[i].sequence.segment != requests[j].sequence.segment {
			return requests[i].sequence.segment < requests[j].sequence.segment
		}
		if requests[i].sequence.start != requests[j].sequence.start {
			return requests[i].sequence.start < requests[j].sequence.start
		}
		return requests[i].sequence.end < requests[j].sequence.end
	})
	known := map[pairSequenceKey]string{}
	for first := 0; first < len(requests); {
		last := first + 1
		for last < len(requests) && requests[last].fileA == requests[first].fileA && requests[last].fileB == requests[first].fileB && requests[last].sequence.segment == requests[first].sequence.segment && requests[last].sequence.start == requests[first].sequence.start {
			last++
		}
		digest := sha256.New()
		writeHashPart(digest, requests[first].fileA)
		writeHashPart(digest, requests[first].fileB)
		hashedEnd := requests[first].sequence.start
		for _, request := range requests[first:last] {
			key := pairSequenceKey{fileA: request.fileA, fileB: request.fileB, sequence: index.sequence(request.sequence)}
			if id, exists := known[key]; exists {
				ids[request.pairIndex] = id
				continue
			}
			for hashedEnd < request.sequence.end {
				writeHashPart(digest, segments[request.sequence.segment].tokens[hashedEnd].Text)
				hashedEnd++
			}
			id := hex.EncodeToString(digest.Sum(nil))
			known[key] = id
			ids[request.pairIndex] = id
		}
		first = last
	}
	return ids
}

func pairID(fileA, fileB string, tokens []lang.Token) string {
	digest := sha256.New()
	writeHashPart(digest, fileA)
	writeHashPart(digest, fileB)
	for _, token := range tokens {
		writeHashPart(digest, token.Text)
	}
	return hex.EncodeToString(digest.Sum(nil))
}

type hashWriter interface {
	Write([]byte) (int, error)
}

func writeHashPart(writer hashWriter, value string) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(value)))
	_, _ = writer.Write(size[:])
	_, _ = writer.Write([]byte(value))
}

func pairLess(a, b model.ClonePair) bool {
	if a.A.File != b.A.File {
		return a.A.File < b.A.File
	}
	if a.A.Start != b.A.Start {
		return a.A.Start < b.A.Start
	}
	if a.A.End != b.A.End {
		return a.A.End < b.A.End
	}
	if a.B.File != b.B.File {
		return a.B.File < b.B.File
	}
	if a.B.Start != b.B.Start {
		return a.B.Start < b.B.Start
	}
	if a.B.End != b.B.End {
		return a.B.End < b.B.End
	}
	return a.ID < b.ID
}
