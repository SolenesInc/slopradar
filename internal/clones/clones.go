package clones

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"hash/fnv"
	"sort"
	"strings"

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
	result := Result{
		Pairs:    []model.ClonePair{},
		Coverage: []model.CloneCoverage{},
		Lines:    map[string]map[model.Bucket][]int{},
		Total:    map[model.Bucket]int{model.Source: 0, model.Tests: 0},
	}
	lineSets := map[string]map[model.Bucket]map[int]struct{}{}
	for _, file := range files {
		lineSets[file.Path] = map[model.Bucket]map[int]struct{}{
			model.Source: {},
			model.Tests:  {},
		}
	}

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
	seen := map[extentPair]struct{}{}
	for _, language := range languages {
		windows := map[uint64][]occurrence{}
		for _, segmentIndex := range byLanguage[language] {
			item := segments[segmentIndex]
			if len(item.tokens) < JscpdDefaultMinimumTokens {
				continue
			}
			hash, power := firstWindow(item.tokens)
			windows[hash] = append(windows[hash], occurrence{segment: segmentIndex})
			for start := 1; start+JscpdDefaultMinimumTokens <= len(item.tokens); start++ {
				hash = nextWindow(hash, tokenHash(item.tokens[start-1].Text), tokenHash(item.tokens[start+JscpdDefaultMinimumTokens-1].Text), power)
				windows[hash] = append(windows[hash], occurrence{segment: segmentIndex, start: start})
			}
		}
		for _, occurrences := range windows {
			for i := range occurrences {
				for j := i + 1; j < len(occurrences); j++ {
					left, right := occurrences[i], occurrences[j]
					if !equalWindow(segments[left.segment].tokens[left.start:], segments[right.segment].tokens[right.start:]) {
						continue
					}
					pair, ok := maximalPair(segments, left, right)
					if !ok {
						continue
					}
					if _, exists := seen[pair]; exists {
						continue
					}
					seen[pair] = struct{}{}
					clonePair := makePair(files, segments, pair)
					if clonePair.Lines < JscpdDefaultMinimumLines {
						continue
					}
					result.Pairs = append(result.Pairs, clonePair)
					markLines(files, segments, pair.a, clonePair.A, lineSets)
					markLines(files, segments, pair.b, clonePair.B, lineSets)
				}
			}
		}
	}

	sort.Slice(result.Pairs, func(i, j int) bool { return pairLess(result.Pairs[i], result.Pairs[j]) })
	for _, file := range files {
		if _, exists := result.Lines[file.Path]; exists {
			continue
		}
		result.Lines[file.Path] = map[model.Bucket][]int{model.Source: {}, model.Tests: {}}
		for _, bucket := range []model.Bucket{model.Source, model.Tests} {
			for line := range lineSets[file.Path][bucket] {
				result.Lines[file.Path][bucket] = append(result.Lines[file.Path][bucket], line)
			}
			sort.Ints(result.Lines[file.Path][bucket])
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

func extentLess(segments []segment, a, b extent) bool {
	if segments[a.segment].file != segments[b.segment].file {
		return segments[a.segment].file < segments[b.segment].file
	}
	if a.segment != b.segment {
		return a.segment < b.segment
	}
	return a.start < b.start
}

func makePair(files []File, segments []segment, pair extentPair) model.ClonePair {
	a := makeRange(files, segments, pair.a)
	b := makeRange(files, segments, pair.b)
	lines := min(sourceLineCount(files[segments[pair.a.segment].file], a), sourceLineCount(files[segments[pair.b.segment].file], b))
	tokens := segments[pair.a.segment].tokens[pair.a.start:pair.a.end]
	return model.ClonePair{ID: pairID(a.File, b.File, tokens), A: a, B: b, Tokens: len(tokens), Lines: lines}
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
	first := tokens[item.start]
	last := tokens[item.end-1]
	return model.Range{
		File:  files[segments[item.segment].file].Path,
		Start: first.Line,
		End:   last.Line + strings.Count(last.Text, "\n"),
	}
}

func pairID(fileA, fileB string, tokens []lang.Token) string {
	if fileB < fileA {
		fileA, fileB = fileB, fileA
	}
	hash := sha256.New()
	writeHashPart(hash, fileA)
	writeHashPart(hash, fileB)
	for _, token := range tokens {
		writeHashPart(hash, token.Text)
	}
	return hex.EncodeToString(hash.Sum(nil))
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

func markLines(files []File, segments []segment, item extent, lines model.Range, sets map[string]map[model.Bucket]map[int]struct{}) {
	segment := segments[item.segment]
	file := files[segment.file]
	for _, bucket := range []model.Bucket{model.Source, model.Tests} {
		bucketLines := file.SourceLines[bucket]
		start := sort.SearchInts(bucketLines, lines.Start)
		end := sort.SearchInts(bucketLines, lines.End+1)
		for _, line := range bucketLines[start:end] {
			sets[file.Path][bucket][line] = struct{}{}
		}
	}
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
