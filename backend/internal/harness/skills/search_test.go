package skills

import (
	"strings"
	"testing"
)

// Search ranks the directory index by query fit, flags what the asking
// workspace already pins, and returns metadata only.
func TestSearchRanksAndFlagsPins(t *testing.T) {
	srv := fixtureRegistry(t)
	p := providerFor(t, srv, nil)

	hits, err := p.Search("connect wallet", map[string]string{"web3-components": "1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("hits = %d, want 1", len(hits))
	}
	h := hits[0]
	if h.Name != "web3-components" || h.Version != "1.0.0" || h.License != "MIT" {
		t.Errorf("hit = %+v, want the fixture plugin with its catalog metadata", h)
	}
	if !h.Pinned {
		t.Error("pins passed by the caller must flag the hit as already enabled")
	}
	if len(h.Matched) == 0 {
		t.Error("matched terms must explain why the entry hit")
	}
	for _, want := range []string{"connect", "wallet"} {
		found := false
		for _, m := range h.Matched {
			if m == want {
				found = true
			}
		}
		if !found {
			t.Errorf("matched = %v, want %q among them", h.Matched, want)
		}
	}
}

// A query nothing fits returns nothing — an honest empty list, not the
// whole directory.
func TestSearchNoMatchReturnsEmpty(t *testing.T) {
	srv := fixtureRegistry(t)
	p := providerFor(t, srv, nil)
	hits, err := p.Search("insurance underwriting", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Errorf("hits = %d, want 0 for a query no entry fits", len(hits))
	}
}

// An empty query lists the directory (names, versions, keywords) — the
// browse mode the tool falls back to.
func TestSearchEmptyQueryListsDirectory(t *testing.T) {
	srv := fixtureRegistry(t)
	p := providerFor(t, srv, nil)
	hits, err := p.Search("", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Name != "web3-components" {
		t.Errorf("hits = %+v, want every plugin in the index", hits)
	}
	if len(hits[0].Matched) != 0 {
		t.Errorf("unfiltered listing must not claim matches, got %v", hits[0].Matched)
	}
	if hits[0].Pinned {
		t.Error("no pins passed, nothing is pinned")
	}
}

// An unreachable index fails the search loudly; the tool surfaces it as
// "unreachable", never as "no plugins exist".
func TestSearchUnreachableIndexFails(t *testing.T) {
	p := RegistryProvider{IndexURL: "http://127.0.0.1:1/registry.json", CacheDir: t.TempDir()}
	if _, err := p.Search("wallet", nil); err == nil {
		t.Fatal("search must fail when the index cannot be reached")
	}
}

// The tokenizer keeps ranking on capability words: stopwords and
// duplicates drop out, exact-name matches outrank description mentions.
func TestSearchTermsAndScoring(t *testing.T) {
	terms := searchTerms("The the Wallet wallet for a payment API")
	if strings.Contains(strings.Join(terms, " "), "the") {
		t.Errorf("stopwords must drop: %v", terms)
	}
	seen := map[string]bool{}
	for _, term := range terms {
		if seen[term] {
			t.Errorf("duplicates must drop: %v", terms)
			break
		}
		seen[term] = true
	}

	exact, _ := scoreRegistryEntry(registryEntry{Name: "payment"}, []string{"payment"})
	sub, _ := scoreRegistryEntry(registryEntry{Name: "payment-api"}, []string{"payment"})
	if exact <= sub {
		t.Errorf("exact name (%d) must outrank substring (%d)", exact, sub)
	}
	kw, matched := scoreRegistryEntry(registryEntry{Name: "x", Keywords: []string{"payments"}}, []string{"payment"})
	if kw == 0 || len(matched) == 0 {
		t.Errorf("keyword stem must hit: score=%d matched=%v", kw, matched)
	}
}
