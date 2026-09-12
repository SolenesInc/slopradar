package model

import "math"

type Bucket string

const (
	Source                  Bucket = "source"
	Tests                   Bucket = "tests"
	ErosionComplexityCutoff        = 10
)

type Function struct {
	File   string     `json:"file"`
	Name   string     `json:"name"`
	Bucket Bucket     `json:"bucket,omitempty"`
	Line   int        `json:"line"`
	CC     int        `json:"cc"`
	SLOC   int        `json:"sloc"`
	Mass   float64    `json:"mass"`
	Nested []Function `json:"nested,omitempty"`
}

type Range struct {
	File  string `json:"file"`
	Start int    `json:"start"`
	End   int    `json:"end"`
}

type ClonePair struct {
	ID     string `json:"id"`
	A      Range  `json:"a"`
	B      Range  `json:"b"`
	Tokens int    `json:"tokens"`
	Lines  int    `json:"lines"`
}

type CloneCoverage struct {
	File   string
	Bucket Bucket
	Lines  []int
}

type Totals struct {
	Functions    int     `json:"functions"`
	Mass         float64 `json:"mass"`
	MassOverCC10 float64 `json:"mass_over_cc_10"`
	Erosion      float64 `json:"erosion"`
	SourceLines  int     `json:"source_lines"`
	CloneLines   int     `json:"clone_lines"`
	CloneShare   float64 `json:"clone_share"`
}

type SkippedFile struct {
	File       string `json:"file"`
	MaxBytes   int64  `json:"max_bytes"`
	AskedBytes int64  `json:"asked_bytes"`
}

type Snapshot struct {
	Rev            string            `json:"rev"`
	Functions      []Function        `json:"functions"`
	Clones         []ClonePair       `json:"clones"`
	CloneCoverage  []CloneCoverage   `json:"-"`
	Buckets        map[Bucket]Totals `json:"buckets"`
	Skipped        []string          `json:"skipped"`
	SkippedDetails []SkippedFile     `json:"skipped_details"`
	Warnings       []string          `json:"warnings"`
}

type FunctionDelta struct {
	File      string    `json:"file"`
	Name      string    `json:"name"`
	Before    *Function `json:"before"`
	After     *Function `json:"after"`
	DeltaMass float64   `json:"delta_mass"`
	Note      string    `json:"note"`
}

type BucketDelta struct {
	MassAddedOverCC10       float64 `json:"mass_added_over_cc_10"`
	MassRemovedOverCC10     float64 `json:"mass_removed_over_cc_10"`
	ErosionBefore           float64 `json:"erosion_before"`
	ErosionAfter            float64 `json:"erosion_after"`
	CloneShareBefore        float64 `json:"clone_share_before"`
	CloneShareAfter         float64 `json:"clone_share_after"`
	CloneLinesTouchedBefore int     `json:"clone_lines_touched_before"`
	CloneLinesTouchedAfter  int     `json:"clone_lines_touched_after"`
}

type Diff struct {
	Base          string                 `json:"base"`
	Head          string                 `json:"head"`
	Touched       []string               `json:"touched"`
	Buckets       map[Bucket]BucketDelta `json:"buckets"`
	Functions     []FunctionDelta        `json:"functions"`
	ClonesAdded   []ClonePair            `json:"clones_added"`
	ClonesRemoved []ClonePair            `json:"clones_removed"`
	Trend         []TrendPoint           `json:"trend"`
}

type TrendPoint struct {
	Rev     string            `json:"rev"`
	Date    string            `json:"date"`
	Buckets map[Bucket]Totals `json:"buckets"`
}

func NewFunction(file, name string, line, cc, sloc int, nested []Function) Function {
	return Function{
		File: file, Name: name, Line: line, CC: cc, SLOC: sloc,
		Mass: float64(cc) * math.Sqrt(float64(sloc)), Nested: nested,
	}
}

func Summarize(functions []Function) Totals {
	totals := Totals{Functions: len(functions)}
	for _, function := range functions {
		totals.Mass += function.Mass
		if function.CC > ErosionComplexityCutoff {
			totals.MassOverCC10 += function.Mass
		}
	}
	if totals.Mass != 0 {
		totals.Erosion = totals.MassOverCC10 / totals.Mass
	}
	return totals
}
