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
}

type pluginManifest struct {
	Name        string            `json:"name"`
	Version     string            `json:"version"`
	Description string            `json:"description"`
	Entry       string            `json:"entry"`
	Skills      []string          `json:"skills"`
	License     string            `json:"license"`
	Files       map[string]string `json:"files"`
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
