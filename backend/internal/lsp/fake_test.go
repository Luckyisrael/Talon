package lsp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestMain doubles as the fake language server: with TALON_LSP_FAKE=1 the
// test binary speaks just enough LSP on stdio to be a deterministic server —
// no network, no node_modules, no real type-checker. Lifecycle tests point
// the client at os.Args[0] through .talon/config.json; behavior is scripted
// by FAKE_* environment variables so every assertion is exact.
func TestMain(m *testing.M) {
	if os.Getenv("TALON_LSP_FAKE") == "1" {
		fakeServerMain()
		return // unreachable; fakeServerMain exits
	}
	os.Exit(m.Run())
}

func fakeServerMain() {
	in := bufio.NewReader(os.Stdin)
	write := func(v any) {
		body, err := json.Marshal(v)
		if err != nil {
			os.Exit(1)
		}
		if _, err := fmt.Fprintf(os.Stdout, "Content-Length: %d\r\n\r\n", len(body)); err != nil {
			os.Exit(0)
		}
		if _, err := os.Stdout.Write(body); err != nil {
			os.Exit(0)
		}
	}

	for {
		length := -1
		for {
			line, err := in.ReadString('\n')
			if err != nil {
				os.Exit(0)
			}
			line = strings.TrimRight(line, "\r\n")
			if line == "" {
				break
			}
			if v, ok := strings.CutPrefix(line, "Content-Length:"); ok {
				length, _ = strconv.Atoi(strings.TrimSpace(v))
			}
		}
		if length < 0 {
			os.Exit(1)
		}
		body := make([]byte, length)
		if _, err := io.ReadFull(in, body); err != nil {
			os.Exit(0)
		}
		var msg struct {
			ID     *int            `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(body, &msg) != nil || msg.Method == "" {
			continue
		}

		// Restart-on-crash scripting: die ONCE on a chosen method, marked by
		// a file so the replacement process serves normally. Unbounded crash
		// loops would prove nothing; a once-crash proves the fleet replaced
		// the dead server with a fresh one.
		if m := os.Getenv("FAKE_CRASH_ONCE_METHOD"); m == msg.Method {
			marker := os.Getenv("FAKE_CRASH_ONCE_FILE")
			if marker == "" {
				os.Exit(3)
			}
			if _, err := os.Stat(marker); os.IsNotExist(err) {
				_ = os.WriteFile(marker, []byte("1"), 0o644)
				os.Exit(3)
			}
		}

		switch msg.Method {
		case "initialize":
			write(map[string]any{"jsonrpc": "2.0", "id": *msg.ID, "result": map[string]any{
				"capabilities": map[string]any{
					"referencesProvider":     true,
					"definitionProvider":     true,
					"documentSymbolProvider": true,
					"textDocumentSync":       1,
				},
			}})
		case "textDocument/didOpen":
			fakePublish(msg.Params, write)
		case "textDocument/references":
			other := os.Getenv("FAKE_OTHER_URI")
			main := fakeDocURI(msg.Params)
			loc := func(uri string, line, col int) map[string]any {
				return map[string]any{"uri": uri, "range": map[string]any{
					"start": map[string]any{"line": line, "character": col},
				}}
			}
			out := []any{loc(main, 0, 16)}
			if other != "" {
				out = append(out, loc(other, 4, 0))
			}
			write(map[string]any{"jsonrpc": "2.0", "id": *msg.ID, "result": out})
		case "textDocument/definition":
			main := fakeDocURI(msg.Params)
			write(map[string]any{"jsonrpc": "2.0", "id": *msg.ID, "result": map[string]any{
				"uri": main, "range": map[string]any{
					"start": map[string]any{"line": 7, "character": 0},
				},
			}})
		case "textDocument/documentSymbol":
			main := fakeDocURI(msg.Params)
			sym := func(name string, kind, line int) map[string]any {
				return map[string]any{
					"name": name, "kind": kind,
					"location": map[string]any{"uri": main, "range": map[string]any{
						"start": map[string]any{"line": line, "character": 0},
					}},
				}
			}
			write(map[string]any{"jsonrpc": "2.0", "id": *msg.ID, "result": []any{
				sym("boot", 12, 0),    // function
				sym("AppRoot", 5, 10), // class
			}})
		case "textDocument/codeAction":
			// W5.4: one organize-imports action that prepends a fixed pair
			// of import lines — it proves the round-trip and the edit
			// application. Sorting REAL imports is the real server's job;
			// the fake pins protocol and bytes, honestly.
			main := fakeDocURI(msg.Params)
			write(map[string]any{"jsonrpc": "2.0", "id": *msg.ID, "result": []any{
				map[string]any{
					"title": "Organize Imports",
					"kind":  "source.organizeImports",
					"edit": map[string]any{"changes": map[string]any{
						main: []any{map[string]any{
							"range": map[string]any{
								"start": map[string]any{"line": 0, "character": 0},
								"end":   map[string]any{"line": 0, "character": 0},
							},
							"newText": "import \"./alpha\";\nimport \"./beta\";\n",
						}},
					}},
				},
			}})
		case "textDocument/didClose", "textDocument/didChange", "initialized":
			// nothing to do
		case "shutdown":
			write(map[string]any{"jsonrpc": "2.0", "id": *msg.ID, "result": nil})
		case "exit":
			os.Exit(0)
		default:
			if msg.ID != nil {
				// Unknown requests get a null answer so the client never hangs.
				write(map[string]any{"jsonrpc": "2.0", "id": *msg.ID, "result": nil})
			}
		}
	}
}

func fakeDocURI(params json.RawMessage) string {
	var p struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
	}
	_ = json.Unmarshal(params, &p)
	return p.TextDocument.URI
}

// fakePublish answers a didOpen with diagnostics iff the file's URI
// contains FAKE_BROKEN_SUBSTRING (default "broken") — the planted type
// error the eval gate names. Everything else opens clean.
func fakePublish(params json.RawMessage, write func(any)) {
	uri := fakeDocURI(params)
	needle := os.Getenv("FAKE_BROKEN_SUBSTRING")
	if needle == "" {
		needle = "broken"
	}
	diags := []any{}
	if strings.Contains(uri, needle) {
		line := 0
		if v := os.Getenv("FAKE_ERROR_LINE"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				line = n - 1 // planted line is 1-based in the env; LSP is 0-based
			}
		}
		msg := os.Getenv("FAKE_ERROR_MSG")
		if msg == "" {
			msg = "Type 'string' is not assignable to type 'number'."
		}
		code := os.Getenv("FAKE_ERROR_CODE")
		if code == "" {
			code = "2322"
		}
		var codeVal any = code
		if n, err := strconv.Atoi(code); err == nil {
			codeVal = n
		}
		diags = append(diags, map[string]any{
			"range":    map[string]any{"start": map[string]any{"line": line, "character": 0}},
			"severity": 1,
			"code":     codeVal,
			"message":  msg,
		})
	}
	write(map[string]any{"jsonrpc": "2.0", "method": "textDocument/publishDiagnostics",
		"params": map[string]any{"uri": uri, "diagnostics": diags}})
}

// fakeRoot builds a workspace whose .talon/config.json points the "lsp"
// command at THIS test binary (the fake server) — the config-override key
// the plan calls for — with a clean file, a planted-error file, and a
// second file references can span into.
func fakeRoot(t *testing.T) string {
	t.Helper()
	t.Setenv("TALON_LSP_FAKE", "1")
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".talon"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := fmt.Sprintf(`{"lsp": %q}`, os.Args[0])
	if err := os.WriteFile(filepath.Join(root, ".talon", "config.json"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	mustMapFile(t, root, "app.ts", "export const port: number = 8080\nexport function boot() {}\n")
	mustMapFile(t, root, "broken.ts", "export const bad: number = 'planted'\n")
	mustMapFile(t, root, "other.ts", "export function helper() {}\nexport const y = 1\nexport const z = 2\nexport const w = 3\nexport const v = 4\n")
	return root
}

func mustMapFile(t *testing.T, root, rel, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, rel), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
