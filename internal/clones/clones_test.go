package clones

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/SolenesInc/slopradar/internal/lang"
	"github.com/SolenesInc/slopradar/internal/model"
)

func TestDetectMergesWindowsAndUnionsCloneLines(t *testing.T) {
	first := numberedTokens("shared", 1, 60)
	second := numberedTokens("shared", 11, 60)
	files := []File{
		{Path: "b.go", Language: "go", Tokens: second, SourceLines: lineRange(model.Source, 11, 70)},
		{Path: "a.go", Language: "go", Tokens: first, SourceLines: lineRange(model.Source, 1, 60)},
	}
	result := Detect(files)
	if len(result.Pairs) != 1 {
		t.Fatalf("pairs = %#v", result.Pairs)
	}
	pair := result.Pairs[0]
	if pair.A != (model.Range{File: "a.go", Start: 1, End: 60}) || pair.B != (model.Range{File: "b.go", Start: 11, End: 70}) || pair.Tokens != 60 || pair.Lines != 60 {
		t.Fatalf("pair = %#v", pair)
	}
	if result.Total[model.Source] != 120 || len(result.Lines["a.go"][model.Source]) != 60 || len(result.Lines["b.go"][model.Source]) != 60 {
		t.Fatalf("lines = %#v, total = %#v", result.Lines, result.Total)
	}
}

func TestDetectRequiresFiveLinesAndSameLanguage(t *testing.T) {
	fourLines := make([]lang.Token, 60)
	for i := range fourLines {
		fourLines[i] = lang.Token{Text: fmt.Sprintf("shared-%d", i), Line: i*4/len(fourLines) + 1, Bucket: model.Source}
	}
	otherLanguage := append([]lang.Token(nil), fourLines...)
	for i := range otherLanguage {
		otherLanguage[i].Line = i*5/len(otherLanguage) + 1
	}
	result := Detect([]File{
		{Path: "a.go", Language: "go", Tokens: fourLines, SourceLines: lineRange(model.Source, 1, 4)},
		{Path: "b.go", Language: "go", Tokens: fourLines, SourceLines: lineRange(model.Source, 1, 4)},
		{Path: "c.py", Language: "python", Tokens: otherLanguage, SourceLines: lineRange(model.Source, 1, 5)},
	})
	if len(result.Pairs) != 0 {
		t.Fatalf("pairs = %#v", result.Pairs)
	}
}

func TestDetectFindsDisjointSameFilePair(t *testing.T) {
	block := numberedTokens("same", 1, 55)
	tokens := append(append([]lang.Token(nil), block...), lang.Token{Text: "separator", Line: 56, Bucket: model.Source})
	second := numberedTokens("same", 57, 55)
	tokens = append(tokens, second...)
	result := Detect([]File{{Path: "same.go", Language: "go", Tokens: tokens, SourceLines: lineRange(model.Source, 1, 111)}})
	if len(result.Pairs) != 1 {
		t.Fatalf("pairs = %#v", result.Pairs)
	}
	if result.Pairs[0].A.File != "same.go" || result.Pairs[0].B.File != "same.go" || result.Pairs[0].Tokens != 55 {
		t.Fatalf("pair = %#v", result.Pairs[0])
	}
}

func TestDetectSeparatesBucketsWithoutPreventingMatches(t *testing.T) {
	source := numberedTokens("bucket", 1, 50)
	tests := numberedTokens("bucket", 20, 50)
	for i := range tests {
		tests[i].Bucket = model.Tests
	}
	result := Detect([]File{
		{Path: "source.go", Language: "go", Tokens: source, SourceLines: lineRange(model.Source, 1, 50)},
		{Path: "source_test.go", Language: "go", Tokens: tests, SourceLines: lineRange(model.Tests, 20, 69)},
	})
	if len(result.Pairs) != 1 || result.Total[model.Source] != 50 || result.Total[model.Tests] != 50 {
		t.Fatalf("result = %#v", result)
	}
}

func TestDetectIsIndependentOfInputOrder(t *testing.T) {
	files := []File{
		{Path: "b.go", Language: "go", Tokens: numberedTokens("order", 10, 50), SourceLines: lineRange(model.Source, 10, 59)},
		{Path: "a.go", Language: "go", Tokens: numberedTokens("order", 1, 50), SourceLines: lineRange(model.Source, 1, 50)},
	}
	first := Detect(files)
	second := Detect([]File{files[1], files[0]})
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("results differ\nfirst: %#v\nsecond: %#v", first, second)
	}
}

func TestPairIDSurvivesLineShifts(t *testing.T) {
	files := []File{
		{Path: "a.go", Language: "go", Tokens: numberedTokens("stable-id", 1, 50), SourceLines: lineRange(model.Source, 1, 50)},
		{Path: "b.go", Language: "go", Tokens: numberedTokens("stable-id", 1, 50), SourceLines: lineRange(model.Source, 1, 50)},
	}
	shifted := []File{
		{Path: "a.go", Language: "go", Tokens: numberedTokens("stable-id", 101, 50), SourceLines: lineRange(model.Source, 101, 150)},
		{Path: "b.go", Language: "go", Tokens: numberedTokens("stable-id", 201, 50), SourceLines: lineRange(model.Source, 201, 250)},
	}
	first := Detect(files)
	second := Detect(shifted)
	if len(first.Pairs) != 1 || len(second.Pairs) != 1 || first.Pairs[0].ID != second.Pairs[0].ID {
		t.Fatalf("first = %#v, shifted = %#v", first.Pairs, second.Pairs)
	}
}

func TestDetectDoesNotNormalizeTokens(t *testing.T) {
	first := numberedTokens("exact", 1, 50)
	second := numberedTokens("exact", 1, 50)
	second[len(second)/2].Text = "renamed-identifier"
	result := Detect([]File{
		{Path: "a.go", Language: "go", Tokens: first, SourceLines: lineRange(model.Source, 1, 50)},
		{Path: "b.go", Language: "go", Tokens: second, SourceLines: lineRange(model.Source, 1, 50)},
	})
	if len(result.Pairs) != 0 {
		t.Fatalf("pairs = %#v", result.Pairs)
	}
}

func TestDetectMatchesExhaustiveReference(t *testing.T) {
	repeated := periodicTokens([]string{";"}, 1, 120)
	mixedBlock := periodicTokens([]string{"a", "b", "a", "c", "a", "b", "d"}, 1, 84)
	mixedSameFile := append([]lang.Token(nil), mixedBlock...)
	mixedSameFile = append(mixedSameFile, lang.Token{Text: "separator", Line: len(mixedSameFile) + 1, Bucket: model.Source})
	mixedSameFile = append(mixedSameFile, periodicTokens([]string{"a", "b", "a", "c", "a", "b", "d"}, len(mixedSameFile)+1, 84)...)
	cases := map[string][]File{
		"repeated token in one file": {
			{Path: "same.js", Language: "javascript", Tokens: repeated, SourceLines: lineRange(model.Source, 1, len(repeated))},
		},
		"mixed period in one file": {
			{Path: "mixed.go", Language: "go", Tokens: mixedSameFile, SourceLines: lineRange(model.Source, 1, len(mixedSameFile))},
		},
		"repeated token across files": {
			{Path: "a.py", Language: "python", Tokens: repeated, SourceLines: lineRange(model.Source, 1, len(repeated))},
			{Path: "b.py", Language: "python", Tokens: periodicTokens([]string{";"}, 201, len(repeated)), SourceLines: lineRange(model.Source, 201, 200+len(repeated))},
		},
		"mixed period across files": {
			{Path: "a.rs", Language: "rust", Tokens: mixedBlock, SourceLines: lineRange(model.Source, 1, len(mixedBlock))},
			{Path: "b.rs", Language: "rust", Tokens: periodicTokens([]string{"a", "b", "a", "c", "a", "b", "d"}, 101, len(mixedBlock)), SourceLines: lineRange(model.Source, 101, 100+len(mixedBlock))},
		},
	}
	for name, files := range cases {
		t.Run(name, func(t *testing.T) {
			got := Detect(files)
			want := exhaustiveDetect(files)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("optimized result differs from exhaustive reference\ngot:  %#v\nwant: %#v", got, want)
			}
			if len(got.Pairs) == 0 {
				t.Fatal("fixture produced no clone pairs")
			}
		})
	}
}

func exhaustiveDetect(input []File) Result {
	files := normalizeFiles(input)
	segments := buildSegments(files)
	active := make([]bool, len(segments))
	for segment := range active {
		active[segment] = true
	}
	index := newLexicalIndex(segments, active)
	byLanguage := map[string][]int{}
	for index, item := range segments {
		byLanguage[files[item.file].Language] = append(byLanguage[files[item.file].Language], index)
	}
	seen := map[extentPair]struct{}{}
	extents := []extentPair{}
	for _, segmentIndexes := range byLanguage {
		windows := map[uint64][]occurrence{}
		for _, segmentIndex := range segmentIndexes {
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
			for left := range occurrences {
				for right := left + 1; right < len(occurrences); right++ {
					a, b := occurrences[left], occurrences[right]
					if !equalWindow(segments[a.segment].tokens[a.start:], segments[b.segment].tokens[b.start:]) {
						continue
					}
					pair, ok := maximalPair(segments, a, b)
					if !ok {
						continue
					}
					if _, exists := seen[pair]; exists {
						continue
					}
					seen[pair] = struct{}{}
					extents = append(extents, pair)
				}
			}
		}
	}
	return buildResult(files, segments, extents, index)
}

func numberedTokens(text string, firstLine, count int) []lang.Token {
	tokens := make([]lang.Token, count)
	for i := range tokens {
		tokens[i] = lang.Token{Text: fmt.Sprintf("%s-%d", text, i), Line: firstLine + i, Bucket: model.Source}
	}
	return tokens
}

func periodicTokens(pattern []string, firstLine, count int) []lang.Token {
	tokens := make([]lang.Token, count)
	for index := range tokens {
		tokens[index] = lang.Token{Text: pattern[index%len(pattern)], Line: firstLine + index, Bucket: model.Source}
	}
	return tokens
}

func lineRange(bucket model.Bucket, first, last int) map[model.Bucket][]int {
	lines := map[model.Bucket][]int{model.Source: {}, model.Tests: {}}
	for line := first; line <= last; line++ {
		lines[bucket] = append(lines[bucket], line)
	}
	return lines
}
