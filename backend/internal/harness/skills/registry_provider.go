package skills

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/luckyisrael/talon/backend/internal/knowledge"
)

// RegistryProvider fetches version-pinned public plugins: the plugin
// directory without vendoring it. Pins are exact versions — a registry move
// never silently changes what a build sees.
//
// Trust posture, same boundary as workspace skills: remote entries are
// listed by name and description only. Bodies reach the model exclusively
// through read_skill, auto-attach and triggers are stripped even if a file
// claims them, and every fetched file is sha256-checked against the signed
// manifest before it is cached or served. A failed fetch degrades to the
// on-disk cache so offline builds keep working; a failed hash fails the
// plugin loudly, never silently.
type RegistryProvider struct {
	// IndexURL is the registry.json location. Empty selects the default
	// public directory.
	IndexURL string
	// Pins maps plugin name to the exact version to serve.
	Pins map[string]string
	// CacheDir roots the offline cache (<name>/<version>/…). Empty disables
	// caching: every Reload refetches.
	CacheDir string
	// Client performs the fetches. Nil builds a guarded default.
	Client *http.Client
}

// DefaultRegistry is the public plugin directory.
const DefaultRegistry = "https://raw.githubusercontent.com/Luckyisrael/talon-plugin-directory/main/registry.json"

const (
	registryTimeout = 15 * time.Second
	maxIndexBytes   = 1 << 20
	maxSkillBytes   = 256 << 10
)

func (p RegistryProvider) Name() string { return "registry" }

type registryIndex struct {
	Registry string          `json:"registry"`
	Version  int             `json:"version"`
	Base     string          `json:"base"`
	Plugins  []registryEntry `json:"plugins"`
}

type registryEntry struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Path        string `json:"path"`
	Description string `json:"description"`
	License     string `json:"license"`
	Repo        string `json:"repo"`
	// Keywords are extra search terms the one-line description does not
	// carry (optional — a directory entry without them still matches on
	// name and description).
	Keywords []string `json:"keywords,omitempty"`
}

type pluginManifest struct {
	Name        string            `json:"name"`
	Version     string            `json:"version"`
	Description string            `json:"description"`
	Entry       string            `json:"entry"`
	Skills      []string          `json:"skills"`
	Tools       []manifestTool    `json:"tools,omitempty"`
	License     string            `json:"license"`
	Files       map[string]string `json:"files"`
}

// manifestTool is a W11 plugin-declared tool: a JSON input schema plus
// direct-exec argv run from the plugin's hash-verified cache directory
// (stdin gets the model's JSON arguments, stdout is the result — W10's
// shell contract applied to tools). Mutating is parsed for loud skipping
// only: read-only registration is the W11 contract, and a manifest claiming
// mutation never gets an execution path.
type manifestTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
	Command     []string        `json:"command"`
	Mutating    bool            `json:"mutating,omitempty"`
}

// PinnedTool is one plugin-declared tool resolved for a workspace (W11):
// the declaration as the signed manifest described it, plus the verified
// cache directory its command runs from.
type PinnedTool struct {
	Plugin      string
	Version     string
	Name        string
	Description string
	InputSchema json.RawMessage
	Command     []string
	Dir         string
}

// PinnedTools resolves every pinned plugin's tool declarations for one
// workspace, mirroring LoadForPins' failure posture: an unreachable index
// degrades to cache-verified declarations, one bad plugin skips with a log
// line, and nothing here can fail the session that asked. Every file the
// commands may touch is materialized through the hash check first — the
// cache write path is the supply-chain boundary the plan names (pins +
// hashes), and a mismatch fails the plugin's tools closed, never open.
func (p RegistryProvider) PinnedTools(pins map[string]string) ([]PinnedTool, error) {
	if len(pins) == 0 || p.CacheDir == "" {
		// No pins = no plugins; no cache dir = nowhere to materialize an
		// executable directory. Both are honest absence, not failure.
		return nil, nil
	}
	indexURL := p.IndexURL
	if indexURL == "" {
		indexURL = DefaultRegistry
	}
	index, err := p.fetchIndex(indexURL)
	if err != nil {
		log.Printf("plugins: index unreachable (%v), tool declarations served from cache only", err)
		return p.cachedToolsForPins(pins)
	}
	names := make([]string, 0, len(pins))
	for n := range pins {
		names = append(names, n)
	}
	sort.Strings(names)
	var out []PinnedTool
	for _, name := range names {
		decls, err := p.pluginTools(index, name, pins[name])
		if err != nil {
			log.Printf("plugins: skipping tools for %s: %v", name, err)
			continue
		}
		out = append(out, decls...)
	}
	return out, nil
}

// pluginTools resolves one pin: exact manifest (drift serves cache like
// loadPlugin does), every pinned file verified into the cache, then the
// read-only declarations extracted (mutations and malformed entries skip
// with a log line — one bad declaration never removes the good ones).
func (p RegistryProvider) pluginTools(index *registryIndex, name, version string) ([]PinnedTool, error) {
	var entry *registryEntry
	for i := range index.Plugins {
		if index.Plugins[i].Name == name {
			entry = &index.Plugins[i]
			break
		}
	}
	if entry == nil {
		return nil, fmt.Errorf("not in registry index")
	}
	man, err := p.fetchManifestForPin(index, entry, name, version)
	if err != nil {
		return nil, err
	}
	if len(man.Tools) == 0 {
		return nil, nil
	}
	rels := make([]string, 0, len(man.Files))
	for rel := range man.Files {
		rels = append(rels, rel)
	}
	sort.Strings(rels) // byte-stable order so fetch logs replay identically
	for _, rel := range rels {
		if _, err := p.verifyFile(index, entry, man, rel); err != nil {
			return nil, fmt.Errorf("file %s: %w", rel, err)
		}
	}
	return declsFromManifest(man, filepath.Join(p.CacheDir, name, version)), nil
}

// fetchManifestForPin fetches the pin-exact manifest, falling back to the
// cached copy on registry drift or unreachable fetch (publishing a new
// version must never break pinned builds — loadPlugin's posture, shared).
func (p RegistryProvider) fetchManifestForPin(index *registryIndex, entry *registryEntry, name, version string) (pluginManifest, error) {
	if entry.Version != version {
		if man, ok := p.cachedManifest(name, version); ok {
			log.Printf("plugins: %s pinned %s, registry at %s — serving cached declarations (edit .talon/plugins.json to adopt %s)",
				name, version, entry.Version, entry.Version)
			return man, nil
		}
		return pluginManifest{}, fmt.Errorf("registry has %s, pinned %s (edit .talon/plugins.json to adopt %s)",
			entry.Version, version, entry.Version)
	}
	manURL := joinBase(index.Base, entry.Path, "talon-plugin.json")
	raw, err := p.fetchGET(manURL, maxSkillBytes)
	if err != nil {
		if man, ok := p.cachedManifest(name, version); ok {
			return man, nil
		}
		return pluginManifest{}, fmt.Errorf("manifest unreachable and not cached: %w", err)
	}
	var man pluginManifest
	if err := json.Unmarshal(raw, &man); err != nil {
		return pluginManifest{}, fmt.Errorf("bad manifest: %w", err)
	}
	if man.Name != name || man.Version != version {
		return pluginManifest{}, fmt.Errorf("manifest says %s@%s, pinned %s@%s", man.Name, man.Version, name, version)
	}
	p.writeCache(name, version, "talon-plugin.json", raw)
	return man, nil
}

// cachedManifest reads the previously verified manifest for a pin.
func (p RegistryProvider) cachedManifest(name, version string) (pluginManifest, bool) {
	raw, err := os.ReadFile(p.cachePath(name, version, "talon-plugin.json"))
	if err != nil {
		return pluginManifest{}, false
	}
	var man pluginManifest
	if err := json.Unmarshal(raw, &man); err != nil || man.Name != name || man.Version != version {
		return pluginManifest{}, false
	}
	return man, true
}

// verifyFile ends in verified bytes by either leg: a fresh fetch that
// hash-checks (then warms the cache), or a cached copy that hash-checks
// when the registry is unreachable. A hash failure on the leg that answers
// fails the file — never serves it.
func (p RegistryProvider) verifyFile(index *registryIndex, entry *registryEntry, man pluginManifest, rel string) ([]byte, error) {
	want, ok := man.Files[rel]
	if !ok || want == "" {
		return nil, fmt.Errorf("not hash-pinned in the manifest")
	}
	data, err := p.fetchGET(joinBase(index.Base, entry.Path, rel), maxSkillBytes)
	if err == nil {
		if got := hashHex(data); got != strings.ToLower(want) {
			return nil, fmt.Errorf("hash mismatch: manifest says %.12s, fetched %.12s", want, got)
		}
		p.writeCache(man.Name, man.Version, rel, data)
		return data, nil
	}
	cached, cerr := os.ReadFile(p.cachePath(man.Name, man.Version, rel))
	if cerr != nil {
		return nil, fmt.Errorf("unreachable and not cached: %v", err)
	}
	if got := hashHex(cached); got != strings.ToLower(want) {
		return nil, fmt.Errorf("cached hash mismatch: manifest says %.12s, have %.12s", want, got)
	}
	return cached, nil
}

// cachedToolsForPins serves declarations entirely from verified cache when
// the index cannot be reached: same extraction, same filters, no network.
func (p RegistryProvider) cachedToolsForPins(pins map[string]string) ([]PinnedTool, error) {
	names := make([]string, 0, len(pins))
	for n := range pins {
		names = append(names, n)
	}
	sort.Strings(names)
	var out []PinnedTool
	for _, name := range names {
		version := pins[name]
		man, ok := p.cachedManifest(name, version)
		if !ok {
			log.Printf("plugins: %s@%s has no cached manifest — its tools are absent this session", name, version)
			continue
		}
		if len(man.Tools) == 0 {
			continue
		}
		rels := make([]string, 0, len(man.Files))
		for rel := range man.Files {
			rels = append(rels, rel)
		}
		sort.Strings(rels)
		okAll := true
		for _, rel := range rels {
			want := man.Files[rel]
			cached, err := os.ReadFile(p.cachePath(name, version, rel))
			if err != nil || want == "" || hashHex(cached) != strings.ToLower(want) {
				log.Printf("plugins: %s file %s not verified in cache — its tools are absent this session", name, rel)
				okAll = false
				break
			}
		}
		if !okAll {
			continue
		}
		out = append(out, declsFromManifest(man, filepath.Join(p.CacheDir, name, version))...)
	}
	return out, nil
}

// declsFromManifest applies the read-only contract's filters once:
// mutations skip (loudly), malformed names and empty commands skip, valid
// declarations pass with schema intact (the tools layer object-guards it).
func declsFromManifest(man pluginManifest, dir string) []PinnedTool {
	var out []PinnedTool
	for _, mt := range man.Tools {
		if mt.Mutating {
			log.Printf("plugins: %s declares mutating tool %q — skipped (plugin tools register read-only in W11)", man.Name, mt.Name)
			continue
		}
		if !validToolName(mt.Name) {
			log.Printf("plugins: %s tool name %q invalid — skipped", man.Name, mt.Name)
			continue
		}
		if len(mt.Command) == 0 || strings.TrimSpace(mt.Command[0]) == "" {
			log.Printf("plugins: %s tool %q has no command — skipped", man.Name, mt.Name)
			continue
		}
		out = append(out, PinnedTool{
			Plugin: man.Name, Version: man.Version, Name: mt.Name,
			Description: mt.Description, InputSchema: mt.InputSchema,
			Command: mt.Command, Dir: dir,
		})
	}
	return out
}

// validToolName bounds declared tool names the same way the mcp package
// bounds server names: ASCII alphanumerics with -_ after the first byte, so
// a name can never smuggle structure into the plugin__ namespace.
func validToolName(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
			continue
		case (c == '_' || c == '-') && i > 0:
			continue
		}
		return false
	}
	return true
}

// ParsePluginPins parses TALON_PLUGINS="name@version,name@version". Bad
// segments are skipped with a log line, never fatal.
func ParsePluginPins(raw string) map[string]string {
	out := map[string]string{}
	for _, seg := range strings.Split(raw, ",") {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		name, version, ok := strings.Cut(seg, "@")
		name, version = strings.TrimSpace(name), strings.TrimSpace(version)
		if !ok || name == "" || version == "" {
			log.Printf("plugins: ignoring malformed pin %q (want name@version)", seg)
			continue
		}
		out[name] = version
	}
	return out
}

func (p RegistryProvider) client() *http.Client {
	if p.Client != nil {
		return p.Client
	}
	return &http.Client{
		Timeout: registryTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) == 0 {
				return nil
			}
			// Redirects stay on the origin host: a registry that bounces to
			// a third party is indistinguishable from a hijack.
			if req.URL.Host != via[0].URL.Host {
				return fmt.Errorf("plugins: refusing cross-host redirect to %s", req.URL.Host)
			}
			if len(via) >= 5 {
				return fmt.Errorf("plugins: too many redirects")
			}
			return nil
		},
	}
}

// fetchGET reads a URL with a byte cap. http(s) only, except loopback which
// exists so tests can serve fixtures without TLS.
func (p RegistryProvider) fetchGET(rawURL string, maxBytes int64) ([]byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "https" && !isLoopback(u.Hostname()) {
		return nil, fmt.Errorf("plugins: refusing non-https URL %s", rawURL)
	}
	req, err := http.NewRequest("GET", rawURL, nil)
	if err != nil {
		return nil, err
	}
	res, err := p.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("plugins: GET %s: HTTP %d", rawURL, res.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("plugins: %s exceeds %d bytes", rawURL, maxBytes)
	}
	return data, nil
}

func isLoopback(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

func hashHex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// Load resolves every pinned plugin to verified skill bodies.
func (p RegistryProvider) Load() ([]knowledge.Skill, error) {
	return p.LoadForPins(p.Pins)
}

// LoadForPins resolves an explicit pin set: the workspace override path.
// An empty set resolves nothing — absence is normal, not an error.
func (p RegistryProvider) LoadForPins(pins map[string]string) ([]knowledge.Skill, error) {
	if len(pins) == 0 {
		return nil, nil
	}
	indexURL := p.IndexURL
	if indexURL == "" {
		indexURL = DefaultRegistry
	}
	index, err := p.fetchIndex(indexURL)
	if err != nil {
		// The index is unreachable: serve pinned skills from cache rather
		// than dropping every plugin at once.
		log.Printf("plugins: index unreachable (%v), serving cache", err)
		return p.loadCachedForPins(pins)
	}
	names := make([]string, 0, len(pins))
	for n := range pins {
		names = append(names, n)
	}
	sort.Strings(names)
	var out []knowledge.Skill
	for _, name := range names {
		skills, err := p.loadPlugin(index, name, pins[name])
		if err != nil {
			log.Printf("plugins: skipping %s: %v", name, err)
			continue
		}
		out = append(out, skills...)
	}
	return out, nil
}

func (p RegistryProvider) fetchIndex(indexURL string) (*registryIndex, error) {
	data, err := p.fetchGET(indexURL, maxIndexBytes)
	if err != nil {
		return nil, err
	}
	var index registryIndex
	if err := json.Unmarshal(data, &index); err != nil {
		return nil, fmt.Errorf("plugins: bad registry index: %w", err)
	}
	if strings.TrimSpace(index.Base) == "" {
		return nil, fmt.Errorf("plugins: registry index has no base")
	}
	return &index, nil
}

func joinBase(base string, parts ...string) string {
	base = strings.TrimSuffix(base, "/")
	for _, part := range parts {
		base += "/" + strings.Trim(part, "/")
	}
	return base
}

func (p RegistryProvider) loadPlugin(index *registryIndex, name, version string) ([]knowledge.Skill, error) {
	var entry *registryEntry
	for i := range index.Plugins {
		if index.Plugins[i].Name == name {
			entry = &index.Plugins[i]
			break
		}
	}
	if entry == nil {
		return nil, fmt.Errorf("not in registry index")
	}
	if entry.Version != version {
		// The registry moved past the pin: serve the cached pinned bytes
		// when they exist so publishing never breaks pinned builds; fresh
		// machines without cache fail closed with an actionable message.
		if cached, cerr := p.loadCachedPlugin(name, version, ""); cerr == nil {
			log.Printf("plugins: %s pinned %s, registry at %s — serving cache (edit .talon/plugins.json to adopt %s)",
				name, version, entry.Version, entry.Version)
			return cached, nil
		}
		return nil, fmt.Errorf("registry has %s, pinned %s (edit .talon/plugins.json to adopt %s)",
			entry.Version, version, entry.Version)
	}
	manURL := joinBase(index.Base, entry.Path, "talon-plugin.json")
	raw, err := p.fetchGET(manURL, maxSkillBytes)
	if err != nil {
		return p.loadCachedPlugin(name, version, fmt.Sprintf("manifest unreachable: %v", err))
	}
	var man pluginManifest
	if err := json.Unmarshal(raw, &man); err != nil {
		return nil, fmt.Errorf("bad manifest: %w", err)
	}
	if man.Name != name || man.Version != version {
		return nil, fmt.Errorf("manifest says %s@%s, pinned %s@%s", man.Name, man.Version, name, version)
	}
	if len(man.Skills) == 0 {
		return nil, fmt.Errorf("manifest lists no skills")
	}
	var out []knowledge.Skill
	for _, rel := range man.Skills {
		body, err := p.fetchVerified(index, entry, man, rel)
		if err != nil {
			return nil, err
		}
		s, err := parseWorkspaceSkill(string(body))
		if err != nil {
			return nil, fmt.Errorf("skill %s: %w", rel, err)
		}
		// Remote skills never auto-attach and never bring triggers, even
		// when the file claims them: listing only, bodies via read_skill.
		s.AutoAttach = false
		s.Triggers = nil
		p.writeCache(name, version, rel, body)
		out = append(out, *s)
	}
	p.writeCache(name, version, "talon-plugin.json", raw)
	return out, nil
}

func (p RegistryProvider) fetchVerified(index *registryIndex, entry *registryEntry, man pluginManifest, rel string) ([]byte, error) {
	want, ok := man.Files[rel]
	if !ok || want == "" {
		return nil, fmt.Errorf("skill %s is not hash-pinned in the manifest", rel)
	}
	data, err := p.fetchGET(joinBase(index.Base, entry.Path, rel), maxSkillBytes)
	if err != nil {
		return nil, err
	}
	if got := hashHex(data); got != strings.ToLower(want) {
		return nil, fmt.Errorf("skill %s hash mismatch: manifest says %.12s, fetched %.12s", rel, want, got)
	}
	return data, nil
}

func (p RegistryProvider) cachePath(name, version, rel string) string {
	return filepath.Join(p.CacheDir, name, version, filepath.FromSlash(rel))
}

func (p RegistryProvider) writeCache(name, version, rel string, data []byte) {
	if p.CacheDir == "" {
		return
	}
	path := p.cachePath(name, version, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o644)
}

// loadCached serves pinned plugins from a previous verified fetch: the
// files on disk already passed hash checks when written.
func (p RegistryProvider) loadCached() ([]knowledge.Skill, error) {
	return p.loadCachedForPins(p.Pins)
}

func (p RegistryProvider) loadCachedForPins(pins map[string]string) ([]knowledge.Skill, error) {
	if p.CacheDir == "" {
		return nil, nil
	}
	names := make([]string, 0, len(pins))
	for n := range pins {
		names = append(names, n)
	}
	sort.Strings(names)
	var out []knowledge.Skill
	for _, name := range names {
		skills, err := p.loadCachedPlugin(name, pins[name], "")
		if err != nil {
			log.Printf("plugins: skipping cached %s: %v", name, err)
			continue
		}
		out = append(out, skills...)
	}
	return out, nil
}

func (p RegistryProvider) loadCachedPlugin(name, version, why string) ([]knowledge.Skill, error) {
	if p.CacheDir == "" {
		if why != "" {
			return nil, fmt.Errorf("%s (no cache configured)", why)
		}
		return nil, fmt.Errorf("no cache configured")
	}
	raw, err := os.ReadFile(p.cachePath(name, version, "talon-plugin.json"))
	if err != nil {
		if why != "" {
			return nil, fmt.Errorf("%s (nothing cached)", why)
		}
		return nil, fmt.Errorf("nothing cached")
	}
	var man pluginManifest
	if err := json.Unmarshal(raw, &man); err != nil {
		return nil, fmt.Errorf("cached manifest unreadable: %w", err)
	}
	if man.Name != name || man.Version != version {
		return nil, fmt.Errorf("cached manifest is %s@%s, pinned %s@%s", man.Name, man.Version, name, version)
	}
	var out []knowledge.Skill
	for _, rel := range man.Skills {
		body, err := os.ReadFile(p.cachePath(name, version, rel))
		if err != nil {
			return nil, fmt.Errorf("cached skill %s unreadable: %w", rel, err)
		}
		if want, ok := man.Files[rel]; ok && want != "" {
			if got := hashHex(body); got != strings.ToLower(want) {
				return nil, fmt.Errorf("cached skill %s failed its hash", rel)
			}
		}
		s, err := parseWorkspaceSkill(string(body))
		if err != nil {
			return nil, fmt.Errorf("cached skill %s: %w", rel, err)
		}
		s.AutoAttach = false
		s.Triggers = nil
		out = append(out, *s)
	}
	return out, nil
}

// SearchHit is one directory match: catalog metadata only (name, version,
// license, why it matched). Skill bodies never ride search results — they
// reach the model only through read_skill after a pin exists.
type SearchHit struct {
	Name        string
	Version     string
	Description string
	License     string
	Keywords    []string
	// Matched lists the query terms this entry scored on — the honest
	// "why this one" the model can repeat to the user.
	Matched []string
	// Pinned is true when the asking workspace already pins this plugin,
	// so search output never tells the model to enable what is enabled.
	Pinned bool
}

// searchStopwords are filler a capability query usually carries; dropping
// them keeps ranking on the words that actually name the job.
var searchStopwords = map[string]bool{
	"a": true, "an": true, "and": true, "are": true, "can": true,
	"do": true, "does": true, "for": true, "from": true, "how": true,
	"i": true, "in": true, "is": true, "it": true, "me": true,
	"my": true, "need": true, "needs": true, "of": true, "on": true,
	"or": true, "please": true, "that": true, "the": true, "this": true,
	"to": true, "use": true, "using": true, "want": true, "wants": true,
	"with": true, "plugin": true, "plugins": true, "talon": true,
}

// searchTerms lowercases and tokenizes a query, dropping stopwords,
// one-character tokens, and duplicates. An empty result means "list
// everything" — a blank query is the directory index, not a refusal.
func searchTerms(query string) []string {
	fields := strings.FieldsFunc(strings.ToLower(query), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	})
	out := make([]string, 0, len(fields))
	seen := map[string]bool{}
	for _, f := range fields {
		if len(f) < 2 || searchStopwords[f] || seen[f] {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	return out
}

// scoreRegistryEntry ranks one directory entry against the query terms:
// exact name beats name substring, which beats a keyword hit, which beats
// a description mention. It returns the score and the terms that scored
// (empty score = no fit; empty terms = unfiltered listing).
func scoreRegistryEntry(e registryEntry, terms []string) (int, []string) {
	if len(terms) == 0 {
		return 0, nil
	}
	name := strings.ToLower(e.Name)
	desc := strings.ToLower(e.Description)
	keywords := make([]string, 0, len(e.Keywords))
	for _, k := range e.Keywords {
		keywords = append(keywords, strings.ToLower(k))
	}
	score := 0
	var matched []string
	for _, t := range terms {
		switch {
		case name == t:
			score += 100
			matched = append(matched, t)
		case strings.Contains(name, t):
			score += 50
			matched = append(matched, t)
		default:
			hit := false
			for _, k := range keywords {
				if k == t || strings.Contains(k, t) || strings.Contains(t, k) {
					score += 40
					hit = true
					break
				}
			}
			if !hit && strings.Contains(desc, t) {
				score += 20
				hit = true
			}
			if hit {
				matched = append(matched, t)
			}
		}
	}
	return score, matched
}

// Search resolves the directory index and ranks it against a plain-words
// capability query. This is the discovery half of the plugin system: the
// model asks what the request needs and gets candidates to choose ONE
// from — no bodies, no bulk loading, nothing enabled by the search itself.
// The pins argument only annotates hits as already-enabled; a nil map
// means "nothing pinned". An unreachable index returns an error the tool
// surfaces honestly; it never falls back to guesses.
func (p RegistryProvider) Search(query string, pins map[string]string) ([]SearchHit, error) {
	indexURL := p.IndexURL
	if indexURL == "" {
		indexURL = DefaultRegistry
	}
	index, err := p.fetchIndex(indexURL)
	if err != nil {
		return nil, err
	}
	terms := searchTerms(query)
	type ranked struct {
		hit   SearchHit
		score int
	}
	var hits []ranked
	for _, e := range index.Plugins {
		score, matched := scoreRegistryEntry(e, terms)
		if len(terms) > 0 && score == 0 {
			continue
		}
		hits = append(hits, ranked{
			hit: SearchHit{
				Name:        e.Name,
				Version:     e.Version,
				Description: e.Description,
				License:     e.License,
				Keywords:    e.Keywords,
				Matched:     matched,
				Pinned:      strings.TrimSpace(pins[e.Name]) != "",
			},
			score: score,
		})
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		return hits[i].hit.Name < hits[j].hit.Name
	})
	const maxHits = 6
	if len(hits) > maxHits {
		hits = hits[:maxHits]
	}
	out := make([]SearchHit, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.hit)
	}
	return out, nil
}

// LatestVersions reports the registry's current version per plugin name.
// Network or parse failures return an error; callers treat that as
// "no update information", never as a reason to change pins.
func (p RegistryProvider) LatestVersions() (map[string]string, error) {
	indexURL := p.IndexURL
	if indexURL == "" {
		indexURL = DefaultRegistry
	}
	index, err := p.fetchIndex(indexURL)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(index.Plugins))
	for _, e := range index.Plugins {
		out[e.Name] = e.Version
	}
	return out, nil
}

// FetchPinnedFile returns one hash-verified non-skill file from a pinned
// plugin (the references/ starting points skills point at). The plugin must
// be in pins at the exact version being served; the relative path must live
// under references/ with no escapes. Verification and caching match skill
// bodies: fetch, compare against the signed manifest, cache on success,
// serve verified cache when the network fails.
func (p RegistryProvider) FetchPinnedFile(pins map[string]string, plugin, rel string) ([]byte, error) {
	rel = filepath.ToSlash(filepath.Clean("/" + strings.TrimPrefix(strings.TrimSpace(rel), "/")))
	rel = strings.TrimPrefix(rel, "/")
	if rel == "" || rel == "talon-plugin.json" || !strings.HasPrefix(rel, "references/") ||
		strings.Contains(rel, "..") {
		return nil, fmt.Errorf("plugins: %q is not a fetchable reference path (want references/…)", rel)
	}
	version, ok := pins[plugin]
	if !ok || version == "" {
		return nil, fmt.Errorf("plugins: %s is not pinned for this project", plugin)
	}
	indexURL := p.IndexURL
	if indexURL == "" {
		indexURL = DefaultRegistry
	}
	index, err := p.fetchIndex(indexURL)
	if err != nil {
		// Offline: verified cache only.
		return p.loadCachedFile(plugin, version, rel)
	}
	var entry *registryEntry
	for i := range index.Plugins {
		if index.Plugins[i].Name == plugin {
			entry = &index.Plugins[i]
			break
		}
	}
	if entry == nil {
		return nil, fmt.Errorf("plugins: %s is not in the registry index", plugin)
	}
	if entry.Version != version {
		// Same discipline as skills: the pin wins, cache covers the gap.
		if cached, cerr := p.loadCachedFile(plugin, version, rel); cerr == nil {
			return cached, nil
		}
		return nil, fmt.Errorf("plugins: registry has %s %s, pinned %s (edit .talon/plugins.json to adopt %s)",
			plugin, entry.Version, version, entry.Version)
	}
	manURL := joinBase(index.Base, entry.Path, "talon-plugin.json")
	raw, err := p.fetchGET(manURL, maxSkillBytes)
	if err != nil {
		return p.loadCachedFile(plugin, version, rel)
	}
	var man pluginManifest
	if err := json.Unmarshal(raw, &man); err != nil {
		return nil, fmt.Errorf("plugins: bad manifest: %w", err)
	}
	if man.Name != plugin || man.Version != version {
		return nil, fmt.Errorf("plugins: manifest says %s@%s, pinned %s@%s",
			man.Name, man.Version, plugin, version)
	}
	want, ok := man.Files[rel]
	if !ok || want == "" {
		return nil, fmt.Errorf("plugins: %s is not hash-pinned in the manifest", rel)
	}
	data, err := p.fetchGET(joinBase(index.Base, entry.Path, rel), maxSkillBytes)
	if err != nil {
		return p.loadCachedFile(plugin, version, rel)
	}
	if got := hashHex(data); got != strings.ToLower(want) {
		return nil, fmt.Errorf("plugins: %s hash mismatch: manifest says %.12s, fetched %.12s", rel, want, got)
	}
	p.writeCache(plugin, version, rel, data)
	return data, nil
}

func (p RegistryProvider) loadCachedFile(plugin, version, rel string) ([]byte, error) {
	if p.CacheDir == "" {
		return nil, fmt.Errorf("plugins: %s unreachable and no cache configured", rel)
	}
	raw, err := os.ReadFile(p.cachePath(plugin, version, "talon-plugin.json"))
	if err != nil {
		return nil, fmt.Errorf("plugins: %s unreachable (nothing cached)", rel)
	}
	var man pluginManifest
	if err := json.Unmarshal(raw, &man); err != nil {
		return nil, fmt.Errorf("plugins: cached manifest unreadable: %w", err)
	}
	data, err := os.ReadFile(p.cachePath(plugin, version, rel))
	if err != nil {
		return nil, fmt.Errorf("plugins: cached %s unreadable: %w", rel, err)
	}
	if want, ok := man.Files[rel]; ok && want != "" {
		if got := hashHex(data); got != strings.ToLower(want) {
			return nil, fmt.Errorf("plugins: cached %s failed its hash", rel)
		}
	}
	return data, nil
}
