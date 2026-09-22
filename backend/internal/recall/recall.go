// Package recall is W4.5's fusion layer: one deterministic way to merge
// ranked lists — FTS phrase hits, bm25 chunks, cosine vectors — into a
// single ordering. Scope item 5 says read_memory's FTS ∪ vector uses the
// "same fusion" as codebase_search, so both legs of both tools meet here.
//
// The method is reciprocal rank fusion (Cormack et al.): score = Σ 1/(K+rank)
// across the lists a candidate appears in. It needs no score normalization —
// bm25, cosine, and recency orderings have numbers that mean nothing to
// each other, and RRF deliberately never looks at them.
package recall

import "sort"

// K is the RRF dampening constant — the value the literature converged on,
// and the one every fusion test's expectations are built on.
const K = 60

// Fuse merges ranked ID lists into one ordering. A candidate seen in
// several lists outranks one seen once — that is the entire point: an entry
// the phrase matcher missed but the vector found still surfaces, and one
// both legs agree on rises. Ties keep first-seen order, so callers pass
// lists strongest-first (lexical before vector): a tie resolves toward
// lexical evidence. A duplicate ID within one list counts once, at the rank
// of its first sighting. Empty and nil lists are legal (single-leg calls
// degenerate to a stable copy of that leg).
func Fuse(ranked ...[]string) []string {
	scores := map[string]float64{}
	var order []string
	for _, list := range ranked {
		inList := map[string]bool{}
		for rank, id := range list {
			if id == "" || inList[id] {
				continue
			}
			inList[id] = true
			if _, ok := scores[id]; !ok {
				order = append(order, id)
			}
			scores[id] += 1 / float64(K+rank+1)
		}
	}
	// Stable sort: equal scores preserve first-seen order (list order, then
	// position within the list) — the tie-break is the caller's leg order.
	sort.SliceStable(order, func(i, j int) bool {
		return scores[order[i]] > scores[order[j]]
	})
	return order
}
