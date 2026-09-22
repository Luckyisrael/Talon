package lsp

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Manager owns the per-workspace process fleet — the device reaper's
// pattern applied to language servers: an LRU cap bounds how many run, an
// idle ticker shuts down the ones nobody uses, restart-on-crash replaces a
// server that died between calls, and every failure is structured
// Unavailable (fail-open-availability — never a block).
type Manager struct {
	idleAfter  time.Duration
	sweepEvery time.Duration
	maxServers int

	mu      sync.Mutex
	servers map[string]*Client // key: root + "|" + language
	lastUse map[string]time.Time

	stop chan struct{}
	once sync.Once
}

// newManager builds a fleet with explicit windows (tests pass short ones)
// and starts its reaper immediately — like the device reaper, Stop halts.
func newManager(idle, sweep time.Duration, max int) *Manager {
	m := &Manager{
		idleAfter:  idle,
		sweepEvery: sweep,
		maxServers: max,
		servers:    map[string]*Client{},
		lastUse:    map[string]time.Time{},
		stop:       make(chan struct{}),
	}
	go m.reapLoop()
	return m
}

var (
	sharedOnce sync.Once
	shared     *Manager
)

// Shared is the process-wide fleet: one reaper, servers keyed by
// workspace+language, lazily created on first use (so a deployment that
// never enables the flag spawns nothing, ever).
func Shared() *Manager {
	sharedOnce.Do(func() {
		shared = newManager(10*time.Minute, time.Minute, 4)
	})
	return shared
}

// Stop halts the reaper and closes every server — no process outlives its
// fleet. Safe to call more than once.
func (m *Manager) Stop() {
	m.once.Do(func() {
		close(m.stop)
		m.mu.Lock()
		clients := make([]*Client, 0, len(m.servers))
		for k, c := range m.servers {
			clients = append(clients, c)
			delete(m.servers, k)
		}
		m.mu.Unlock()
		for _, c := range clients {
			_ = c.Close()
		}
	})
}

func (m *Manager) reapLoop() {
	t := time.NewTicker(m.sweepEvery)
	defer t.Stop()
	for {
		select {
		case <-m.stop:
			return
		case now := <-t.C:
			var stale []*Client
			m.mu.Lock()
			for k, c := range m.servers {
				if now.Sub(m.lastUse[k]) > m.idleAfter {
					stale = append(stale, c)
					delete(m.servers, k)
					delete(m.lastUse, k)
				}
			}
			m.mu.Unlock()
			for _, c := range stale {
				_ = c.Close() // outside the lock: Close waits up to ~2s
			}
		}
	}
}

// Client returns a live server for the language behind path, starting one
// if needed. Resolution order: the workspace's .talon/config.json "lsp"
// override, then builtin detection (go → gopls, ts/js → typescript-
// language-server). A missing binary or an undetected language is
// Unavailable — structured, never a block. A client that crashed since its
// last use is replaced before it is returned, and the LRU cap evicts the
// coldest server when the fleet is full.
func (m *Manager) Client(ctx context.Context, root, path string) (*Client, error) {
	if !Enabled() {
		return nil, Unavailable("TALONSTUDIO_LSP is off")
	}
	lang, argv := resolve(root, path)
	if lang == "" {
		return nil, Unavailable("no language server serves " + path)
	}
	key := root + "|" + lang

	var evict, dead *Client
	m.mu.Lock()
	if c, ok := m.servers[key]; ok {
		if c.Alive() {
			m.lastUse[key] = time.Now()
			m.mu.Unlock()
			return c, nil
		}
		dead = c
		delete(m.servers, key)
		delete(m.lastUse, key)
	}
	evict = m.evictLocked(key)
	m.mu.Unlock()
	if dead != nil {
		_ = dead.Close()
	}
	if evict != nil {
		_ = evict.Close()
	}

	c, err := startClient(ctx, root, argv)
	if err != nil {
		return nil, err
	}

	// Another goroutine may have started the same key while we spawned;
	// adopt theirs and close ours so exactly one process serves the key.
	m.mu.Lock()
	if existing, ok := m.servers[key]; ok && existing.Alive() {
		m.lastUse[key] = time.Now()
		m.mu.Unlock()
		_ = c.Close()
		return existing, nil
	}
	m.servers[key] = c
	m.lastUse[key] = time.Now()
	m.mu.Unlock()
	return c, nil
}

// evictLocked returns the coldest client when the fleet is at its cap
// (never the key being inserted). Caller closes it outside the lock.
func (m *Manager) evictLocked(keep string) *Client {
	if len(m.servers) < m.maxServers {
		return nil
	}
	victimKey := ""
	var victimAt time.Time
	for k := range m.servers {
		if k == keep {
			continue
		}
		at := m.lastUse[k]
		if victimKey == "" || at.Before(victimAt) {
			victimKey, victimAt = k, at
		}
	}
	if victimKey == "" {
		return nil
	}
	c := m.servers[victimKey]
	delete(m.servers, victimKey)
	delete(m.lastUse, victimKey)
	return c
}

// resolve maps a path to (language, argv). The config override replaces the
// builtin command for whatever language the file needs; undetected
// extensions stay undetected (the key refines detection, it does not invent
// it — honest about what a server can actually serve).
func resolve(root, path string) (lang string, argv []string) {
	lang = LangFor(path)
	if lang == "" {
		return "", nil
	}
	if cmd := LoadConfig(root).Command(); len(cmd) > 0 {
		return lang, cmd
	}
	switch lang {
	case "go":
		return lang, []string{"gopls"}
	case "ts":
		return lang, []string{"typescript-language-server", "--stdio"}
	}
	return "", nil
}

// LangFor classifies a path by extension — the fleet's cache key and the
// builtin command's selector. Exported so the lsp_* tools group workspace
// sweeps by the SAME classes the fleet keys on (one law, no drift): ".go"
// → "go", the ts family → "ts", anything else → "" (no server).
func LangFor(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		return "go"
	case ".ts", ".tsx", ".js", ".jsx":
		return "ts"
	}
	return ""
}
