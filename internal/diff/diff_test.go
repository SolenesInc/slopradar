package diff

import (
	"math"
	"reflect"
	"testing"

	"github.com/SolenesInc/slopradar/internal/model"
)

func TestBuildSeparatesMassAndAnnotatesEveryFunctionChange(t *testing.T) {
	base := snapshot("base", []model.Function{
		function("source.go", "back", model.Source, 16, 36),
		function("source.go", "removed", model.Source, 12, 25),
		function("source.go", "crossed", model.Source, 10, 16),
		function("source.go", "unchanged", model.Source, 20, 9),
	}, model.Totals{Erosion: 0.25, CloneShare: 0.1}, model.Totals{Erosion: 0.5, CloneShare: 0.2})
	head := snapshot("head", []model.Function{
		function("source.go", "back", model.Source, 8, 25),
		function("source.go", "crossed", model.Source, 12, 25),
		function("source.go", "new high", model.Source, 11, 36),
		function("source.go", "new low", model.Source, 2, 9),
		function("source.go", "unchanged", model.Source, 20, 9),
		function("source_test.go", "test high", model.Tests, 20, 4),
	}, model.Totals{Erosion: 0.3, CloneShare: 0.15}, model.Totals{Erosion: 0.6, CloneShare: 0.25})

	got := Build(base, head, []string{"source_test.go", "source.go"})
	source := got.Buckets[model.Source]
	if source.MassAddedOverCC10 != 86 || source.MassRemovedOverCC10 != 116 {
		t.Fatalf("source mass delta = %#v", source)
	}
	if got.Buckets[model.Tests].MassAddedOverCC10 != 40 {
		t.Fatalf("test mass delta = %#v", got.Buckets[model.Tests])
	}
	if source.ErosionBefore != 0.25 || source.ErosionAfter != 0.3 || source.CloneShareBefore != 0.1 || source.CloneShareAfter != 0.15 {
		t.Fatalf("source ratios = %#v", source)
	}
	gotNotes := map[string]string{}
	for _, delta := range got.Functions {
		gotNotes[delta.Name] = delta.Note
	}
	wantNotes := map[string]string{
		"back": "back under CC 10", "removed": "removed", "crossed": "crossed CC 10",
		"new high": "new", "new low": "new", "test high": "new",
	}
	if !reflect.DeepEqual(gotNotes, wantNotes) {
		t.Fatalf("notes = %#v, want %#v", gotNotes, wantNotes)
	}
	if got.Functions[0].Name != "new high" || got.Functions[1].Name != "removed" || got.Functions[2].Name != "back" {
		t.Fatalf("function order = %#v", got.Functions)
	}
}

func TestBuildMatchesRepeatedNamesByLineOrder(t *testing.T) {
	base := snapshot("base", []model.Function{
		functionAt("same.go", "(anonymous)", model.Source, 10, 2, 4),
		functionAt("same.go", "(anonymous)", model.Source, 30, 3, 4),
	}, model.Totals{}, model.Totals{})
	head := snapshot("head", []model.Function{
		functionAt("same.go", "(anonymous)", model.Source, 12, 4, 4),
		functionAt("same.go", "(anonymous)", model.Source, 32, 5, 4),
	}, model.Totals{}, model.Totals{})
	got := Build(base, head, []string{"same.go"})
	if len(got.Functions) != 2 || got.Functions[0].Before.Line != 10 || got.Functions[0].After.Line != 12 || got.Functions[1].Before.Line != 30 || got.Functions[1].After.Line != 32 {
		t.Fatalf("deltas = %#v", got.Functions)
	}
}

func TestBuildKeepsCloneIdentityAcrossLineShiftsAndUnionsTouchedLines(t *testing.T) {
	base := snapshot("base", nil, model.Totals{}, model.Totals{})
	base.Clones = []model.ClonePair{
		clone("removed", "source_test.go", 10, 14, "other_test.go", 20, 24),
		clone("stable", "source.go", 1, 5, "other.go", 10, 14),
		clone("untouched", "one.go", 1, 5, "two.go", 1, 5),
	}
	head := snapshot("head", nil, model.Totals{}, model.Totals{})
	head.Clones = []model.ClonePair{
		clone("added", "source.go", 20, 25, "third.go", 1, 6),
		clone("stable", "source.go", 3, 7, "other.go", 12, 16),
	}
	got := Build(base, head, []string{"source.go", "source_test.go"})
	if len(got.ClonesAdded) != 1 || got.ClonesAdded[0].ID != "added" || len(got.ClonesRemoved) != 1 || got.ClonesRemoved[0].ID != "removed" {
		t.Fatalf("clone changes = added %#v, removed %#v", got.ClonesAdded, got.ClonesRemoved)
	}
	if got.Buckets[model.Source].CloneLinesTouchedBefore != 5 || got.Buckets[model.Source].CloneLinesTouchedAfter != 11 {
		t.Fatalf("source clone lines = %#v", got.Buckets[model.Source])
	}
	if got.Buckets[model.Tests].CloneLinesTouchedBefore != 5 || got.Buckets[model.Tests].CloneLinesTouchedAfter != 0 {
		t.Fatalf("test clone lines = %#v", got.Buckets[model.Tests])
	}
}

func TestBuildPreservesRepeatedCloneIdentityMultiplicity(t *testing.T) {
	base := snapshot("base", nil, model.Totals{}, model.Totals{})
	base.Clones = []model.ClonePair{
		clone("repeated", "source.go", 1, 5, "other.go", 1, 5),
		clone("repeated", "source.go", 20, 24, "other.go", 20, 24),
	}
	head := snapshot("head", nil, model.Totals{}, model.Totals{})
	head.Clones = []model.ClonePair{
		clone("repeated", "source.go", 2, 6, "other.go", 2, 6),
		clone("repeated", "source.go", 21, 25, "other.go", 21, 25),
		clone("repeated", "source.go", 40, 44, "other.go", 40, 44),
	}
	added := Build(base, head, []string{"source.go"})
	if len(added.ClonesAdded) != 1 || added.ClonesAdded[0].A.Start != 40 || len(added.ClonesRemoved) != 0 {
		t.Fatalf("added repeated occurrence = %#v, removed = %#v", added.ClonesAdded, added.ClonesRemoved)
	}
	removed := Build(head, base, []string{"source.go"})
	if len(removed.ClonesRemoved) != 1 || removed.ClonesRemoved[0].A.Start != 40 || len(removed.ClonesAdded) != 0 {
		t.Fatalf("removed repeated occurrence = %#v, added = %#v", removed.ClonesRemoved, removed.ClonesAdded)
	}
}

func snapshot(rev string, functions []model.Function, source, tests model.Totals) model.Snapshot {
	if functions == nil {
		functions = []model.Function{}
	}
	return model.Snapshot{Rev: rev, Functions: functions, Clones: []model.ClonePair{}, Buckets: map[model.Bucket]model.Totals{model.Source: source, model.Tests: tests}}
}

func function(file, name string, bucket model.Bucket, cc, sloc int) model.Function {
	return functionAt(file, name, bucket, 1, cc, sloc)
}

func functionAt(file, name string, bucket model.Bucket, line, cc, sloc int) model.Function {
	function := model.NewFunction(file, name, line, cc, sloc, nil)
	function.Bucket = bucket
	return function
}

func clone(id, aFile string, aStart, aEnd int, bFile string, bStart, bEnd int) model.ClonePair {
	return model.ClonePair{ID: id, A: model.Range{File: aFile, Start: aStart, End: aEnd}, B: model.Range{File: bFile, Start: bStart, End: bEnd}, Tokens: 50, Lines: int(math.Min(float64(aEnd-aStart+1), float64(bEnd-bStart+1)))}
}
