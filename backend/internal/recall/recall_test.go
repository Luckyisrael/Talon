package recall

import (
	"reflect"
	"testing"
)

func TestFuseSingleListIsStable(t *testing.T) {
	got := Fuse([]string{"a", "b", "c"})
	want := []string{"a", "b", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Fuse(single) = %v, want %v", got, want)
	}
}

// TestFuseOverlapBoosts: an ID in both lists outranks IDs seen once — the
// whole reason fusion beats either leg alone.
func TestFuseOverlapBoosts(t *testing.T) {
	// "hit" is last in the lexical list but first in the vector list; a
	// single-list read would bury it behind everything else.
	lex := []string{"lex1", "lex2", "lex3", "hit"}
	vec := []string{"hit", "vec1"}
	got := Fuse(lex, vec)
	if len(got) != 5 {
		t.Fatalf("got %d ids, want 5: %v", len(got), got)
	}
	if got[0] != "hit" {
		t.Errorf("both-lists candidate = %q first, want it at position 0 (%v)", got[0], got)
	}
	// Remaining order: higher-scoring singles before lower ones; lex1 (rank0
	// of a longer-scoring list) and vec1 (rank1) both beat lex3 (rank2)...
	// just pin the invariant: nothing seen once outranks "hit", and the
	// tail keeps per-list relative order.
	pos := map[string]int{}
	for i, id := range got {
		pos[id] = i
	}
	if pos["lex1"] > pos["lex3"] || pos["vec1"] > pos["lex3"] {
		// lex1: 1/61 + nothing; vec1: 1/62; lex3: 1/63 — both beat lex3.
		t.Errorf("relative order wrong: %v", got)
	}
}

// TestFuseTieKeepsFirstList: equal scores resolve toward the earlier list
// (callers pass lexical before vector, so ties favor lexical evidence).
func TestFuseTieKeepsFirstList(t *testing.T) {
	got := Fuse([]string{"a", "b"}, []string{"b", "a"})
	// a and b each appear at rank0 once and rank1 once — exact tie — so
	// first-seen order (a then b) survives the stable sort.
	want := []string{"a", "b"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Fuse(tie) = %v, want %v", got, want)
	}
}

// TestFuseDeduplicatesWithinList: a repeated ID counts once, at its first
// rank — a list that repeats itself must not inflate a candidate.
func TestFuseDeduplicatesWithinList(t *testing.T) {
	got := Fuse([]string{"dup", "other", "dup"})
	want := []string{"dup", "other"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Fuse(dup) = %v, want %v", got, want)
	}
}

// TestFuseEmptyLegs: no vector results must not perturb the lexical list —
// the flag-off / provider-off degenerate case both tools rely on.
func TestFuseEmptyLegs(t *testing.T) {
	if got := Fuse([]string{"x", "y"}, nil); !reflect.DeepEqual(got, []string{"x", "y"}) {
		t.Errorf("Fuse(lex, nil) = %v", got)
	}
	if got := Fuse(nil, []string{"x", "y"}); !reflect.DeepEqual(got, []string{"x", "y"}) {
		t.Errorf("Fuse(nil, vec) = %v", got)
	}
	if got := Fuse(); len(got) != 0 {
		t.Errorf("Fuse() = %v, want empty", got)
	}
}

// TestFuseSkipsEmptyIDs: a "" id (a caller's missing field) never joins —
// fusing would fabricate a phantom candidate shared by unrelated rows.
func TestFuseSkipsEmptyIDs(t *testing.T) {
	got := Fuse([]string{"", "keep"}, []string{"", "also"})
	want := []string{"keep", "also"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Fuse(empty-id) = %v, want %v", got, want)
	}
}
