// Package mcp implements W11's MCP client seam: servers declared in the
// workspace's .talon/config.json whose tools join the one tool registry
// under the mcp__ namespace. There is no second execution path — an MCP
// tool is a tools.Tool like any other, so policy, guardrails, validateArgs,
// permission mode, and the sandbox ring all apply to it by construction.
//
// Trust posture (the plan's risk line): supply chain is bounded by
// per-server allow/deny policy, read-only by default (mutation needs an
// explicit workspace allow AND a write-capable sandbox posture), and
// namespacing so an MCP tool can never shadow a native one.
package mcp

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Enabled reads the W11 flag: truthy (1|true|on|v1) lets servers load from
// the workspace config; anything else — including unset — reads no config
// at all and is byte-identical to a build without MCP.
func Enabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("TALONSTUDIO_MCP"))) {
	case "1", "true", "on", "v1":
		return true
	}
	return false
}

// ServerConfig is one declared MCP server as it appears in
// .talon/config.json's "mcp.servers" array — the same file W6
// (verify_gates), W9 (permission_mode), and W10 (hooks) read.
//
// Policy: Allow (when non-empty) is an exact-name include list; Deny always
// wins over Allow. Both are evaluated per remote tool name — denied tools
// stay advertised (H3's catalog-stability law: refusals happen at policy
// time, not by hiding tools) and refuse at call time with the reason.
// Mutate is the explicit workspace-side allow that promotes a tool out of
// the read-only default; anything not listed there runs read-only.
type ServerConfig struct {
	Name      string            `json:"name"`
	Transport string            `json:"transport,omitempty"` // "stdio" | "http"; empty derives
	Command   []string          `json:"command,omitempty"`   // stdio: argv to spawn
	Env       map[string]string `json:"env,omitempty"`       // stdio: env additions
	URL       string            `json:"url,omitempty"`       // http: endpoint
	Headers   map[string]string `json:"headers,omitempty"`   // http: extra headers, ${ENV} expanded
	Allow     []string          `json:"allow,omitempty"`     // exact tool names; empty = all allowed
	Deny      []string          `json:"deny,omitempty"`      // exact tool names; always refused
	Mutate    []string          `json:"mutate,omitempty"`    // exact tool names allowed to mutate
}

// fileConfig is the W11 view of .talon/config.json.
type fileConfig struct {
	MCP struct {
		Servers []ServerConfig `json:"servers"`
	} `json:"mcp"`
}

// nameShape bounds names so a server or tool name can never shadow a native
// tool or smuggle path/JSON structure into the mcp__ namespace.
var nameShape = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

// ValidName reports whether a remote tool's name fits the namespace —
// registration drops the ones that don't (api/mcpwiring), so a hostile or
// sloppy name never reaches the catalog.
func ValidName(name string) bool { return nameShape.MatchString(name) }

// Load reads workspacePath/.talon/config.json's "mcp" object and returns
// the servers that validate, skipping — with a log line naming the entry —
// any that do not. MCP is additive capability, not enforcement: one bad
// server degrades to that server missing, never to the session failing and
// never to a partially-parsed server being dialled. Flag off, empty
// workspace, missing file, or no "mcp" key: nil, no I/O beyond the flag.
func Load(workspacePath string) []ServerConfig {
	if !Enabled() || workspacePath == "" {
		return nil
	}
	raw, err := os.ReadFile(filepath.Join(workspacePath, ".talon", "config.json"))
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("mcp: cannot read project config, MCP disabled: %v", err)
		}
		return nil
	}
	var cfg fileConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		// Same file the other readers parse: invalid JSON is invalid for
		// everyone, and each reader logs its own view of it.
		log.Printf("mcp: invalid project config JSON, MCP disabled: %v", err)
		return nil
	}
	var out []ServerConfig
	seen := map[string]bool{}
	for i, sc := range cfg.MCP.Servers {
		if err := validateServer(sc, seen); err != nil {
			log.Printf("mcp: server[%d] invalid, skipped: %v", i, err)
			continue
		}
		sc.Headers = expandVars(sc.Headers)
		sc.Env = expandVars(sc.Env)
		// Normalize the derived transport so a loaded config round-trips
		// self-describing (Dial would derive the same value).
		if sc.Transport == "" {
			if len(sc.Command) > 0 {
				sc.Transport = "stdio"
			} else {
				sc.Transport = "http"
			}
		}
		seen[sc.Name] = true
		out = append(out, sc)
	}
	return out
}

// validateServer enforces the shape Dial depends on and rejects duplicate
// names (a duplicate would make the mcp__ namespace ambiguous).
func validateServer(sc ServerConfig, seen map[string]bool) error {
	if !nameShape.MatchString(sc.Name) {
		return fmt.Errorf("name %q must match %s", sc.Name, nameShape)
	}
	if seen[sc.Name] {
		return fmt.Errorf("duplicate server name %q", sc.Name)
	}
	switch sc.Transport {
	case "", "stdio", "http":
	default:
		return fmt.Errorf("unknown transport %q (want stdio or http)", sc.Transport)
	}
	transport := sc.Transport
	if transport == "" {
		if len(sc.Command) > 0 {
			transport = "stdio"
		} else if sc.URL != "" {
			transport = "http"
		} else {
			return fmt.Errorf("server %q declares neither command nor url", sc.Name)
		}
	}
	switch transport {
	case "stdio":
		if len(sc.Command) == 0 || strings.TrimSpace(sc.Command[0]) == "" {
			return fmt.Errorf("stdio server %q needs a command argv", sc.Name)
		}
	case "http":
		if sc.URL == "" {
			return fmt.Errorf("http server %q needs a url", sc.Name)
		}
		if !strings.HasPrefix(sc.URL, "http://") && !strings.HasPrefix(sc.URL, "https://") {
			return fmt.Errorf("http server %q has a non-http(s) url", sc.Name)
		}
	}
	return nil
}

// expandVars resolves ${NAME} references against the process environment so
// tokens live in the environment, not in the committed config. An unset
// variable expands to empty — the header/env is present but inert, and the
// server's own auth failure reports it honestly at call time.
func expandVars(m map[string]string) map[string]string {
	if len(m) == 0 {
		return m
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = os.Expand(v, func(name string) string { return os.Getenv(name) })
	}
	return out
}
