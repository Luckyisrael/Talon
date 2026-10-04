package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type ToolUseBlock struct {
	Type  string          `json:"type"`
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

type ToolResultBlock struct {
	Type      string `json:"type"`
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content"`
	IsError   bool   `json:"is_error,omitempty"`
}

type AgenticResponse struct {
	Content    []ContentBlock `json:"content"`
	StopReason string         `json:"stop_reason"`
	Usage      struct {
		InputTokens              int `json:"input_tokens"`
		OutputTokens             int `json:"output_tokens"`
		CacheReadInputTokens     int `json:"cache_read_input_tokens"`
		CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	} `json:"usage"`
}

type ContentBlock struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	Thinking string          `json:"thinking,omitempty"`
	ID       string          `json:"id,omitempty"`
	Name     string          `json:"name,omitempty"`
	Input    json.RawMessage `json:"input,omitempty"`
}

// AgenticChatResult is one assistant turn. Blocks is the authoritative form —
// it round-trips to storage and back into the next request. Text and ToolUses
// are conveniences derived from it.
type AgenticChatResult struct {
	Text       string
	ToolUses   []ToolUseBlock
	Blocks     []Block
	StopReason string
	Usage      Usage
}

// maxOutputTokens caps a single response. Large enough for several complete
// component files in one turn — and for a thinking model's reasoning plus the
// plan envelope it is reasoning about: Run-7's plan was cut mid-array at
// 17,081 chars under the old 16384 ceiling, and one build round alone emitted
// out=10628. The ceiling is a limit, not a target (FM-PLANa).
const maxOutputTokens = 32768

// StreamSink receives the model's words as they are produced, mid-call. When
// an orchestrator passes one, the round's text and reasoning reach the client
// as they arrive instead of all at once when the call returns — without it a
// long model call shows the user nothing but a spinner, because the provider
// has the answer and the wire is silent.
type StreamSink func(text string, thinking bool)

// logEstInput emits the per-call input estimate every agentic path logs —
// request bodies that exceed a provider's per-minute input budget are
// rejected outright, so the estimate is the diagnostic that says what to
// trim. ~4 chars/token is coarse but consistent. The format is a contract
// with e2e/scorecard.py (which parses it for S9/S10), so the label is a
// parameter but the line's shape is not: one emitter, both protocols
// (FM-TELf), because a diagnostic that exists only on the openai path
// voids the scorecard's rows for every other provider.
func logEstInput(label, model string, body []byte, system string, histMsgs, toolDefs int) {
	log.Printf("%s model=%s est_input_tok=%d (system=%d hist_msgs=%d tools=%d body_bytes=%d)",
		label, model, len(body)/4, len(system)/4, histMsgs, toolDefs, len(body))
}

// ChatWithTools performs one agentic round: send the conversation plus tool
// definitions, get back text and/or tool calls. It does not execute tools —
// the caller owns the loop. A non-nil sink receives text and reasoning deltas
// as the provider emits them; a nil sink keeps the buffered behaviour.
func (c *Client) ChatWithTools(ctx context.Context, system string, history []Message, tools []ToolDefinition, sink StreamSink) (*AgenticChatResult, error) {
	if c.protocol == ProtocolOpenAI {
		return c.chatWithToolsOpenAI(ctx, system, history, tools, sink)
	}
	// FM-ANTa: the sink crosses this boundary too. Dropping it here meant
	// every anthropic build round was buffered while the openai one
	// streamed, and the caller could not tell which wire it got.
	return c.chatWithToolsAnthropic(ctx, system, history, tools, sink)
}

// doWithRetry issues the request, retrying transient failures with exponential
// backoff plus jitter and honouring Retry-After when the server sends it.
func (c *Client) doWithRetry(ctx context.Context, url string, body []byte, setHeaders func(*http.Request), label string) ([]byte, error) {
	const maxRetries = 4
	var lastErr error
	// rateLimitWaited marks a round that already waited out a rate window
	// below, so the loop-top ladder does not stack its own seconds on top of
	// a minute (FM-429a). Reset every round: a plain transient still ladders
	// exactly as before.
	rateLimitWaited := false
	// FM-AUTHa: a first identity rejection (401/403) waits authRetryDelay
	// and retries once below; rateLimitWaited suppresses the loop-top
	// ladder so the two waits never stack.
	authRetried := false

	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 && !rateLimitWaited {
			delay := time.Duration(1<<uint(attempt-1)) * 2 * time.Second
			// Jitter keeps concurrent builds from retrying in lockstep.
			delay += time.Duration(rand.Int63n(int64(time.Second)))
			log.Printf("%s: retry %d/%d in %v", label, attempt+1, maxRetries, delay)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}
		rateLimitWaited = false

		req, err := c.newRequest(ctx, url, body)
		if err != nil {
			return nil, err
		}
		setHeaders(req)

		resp, err := c.httpClient.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			lastErr = err
			continue
		}

		respBody, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			continue
		}

		switch {
		case resp.StatusCode == http.StatusOK:
			return respBody, nil
		case resp.StatusCode == 429 || resp.StatusCode >= 500:
			// Quota exhaustion is not throttling: the budget refills on the
			// provider's clock, so retrying (even with Retry-After) only
			// burns requests. Fail fast with the reset time when given.
			if resp.StatusCode == 429 && isQuotaExhausted(string(respBody)) {
				err := quotaError(resp.Header.Get("X-RateLimit-Reset"))
				c.noteFailure(err)
				return nil, err
			}
			lastErr = fmt.Errorf("API error (status %d): %s", resp.StatusCode, truncate(string(respBody), 500))
			if resp.StatusCode == 429 {
				// FM-429a(iv): a 429 waits the gateway's own Retry-After,
				// else the rate window — never the exponential ladder, whose
				// 2s/4s/8s futility-spends itself ~46s before a per-minute
				// window resets. Reported as well: a window-long wait with no
				// report is exactly the silent stall the hook exists to kill.
				delay := c.rateLimitDelay(resp.Header.Get("Retry-After"))
				log.Printf("%s: rate limited (attempt %d), waiting %v", label, attempt+1, delay)
				c.reportRetry(attempt+1, maxRetries, "rate limited (HTTP 429)", delay)
				if err := waitBackoff(ctx, delay); err != nil {
					return nil, err
				}
				rateLimitWaited = true
				continue
			}
			// 5xx keeps today's behaviour untouched: its Retry-After is the
			// provider's guess at its own recovery, and FM-429a is about the
			// rate window only — a window-sized backoff would stall a 502
			// recovery, which is the one thing the law forbids here.
			if ra := resp.Header.Get("Retry-After"); ra != "" {
				if secs, convErr := strconv.Atoi(ra); convErr == nil && secs > 0 && secs <= 60 {
					select {
					case <-ctx.Done():
						return nil, ctx.Err()
					case <-time.After(time.Duration(secs) * time.Second):
					}
				}
			}
			continue
		default:
			// 4xx other than 429 will not succeed on retry — except a
			// first identity rejection, which gets one backoff retry
			// (FM-AUTHa): a gateway that authenticates on client identity
			// can flap mid-run, and one flap must not kill a healthy turn.
			// A second refusal returns this same status-bearing error.
			err := fmt.Errorf("API error (status %d): %s", resp.StatusCode, truncate(string(respBody), 1000))
			if !authRetried && (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden) && attempt < maxRetries-1 {
				authRetried = true
				lastErr = err
				delay := c.authRetryDelay()
				log.Printf("%s: HTTP %d — identity rejected, one retry in %v", label, resp.StatusCode, delay)
				c.reportRetry(attempt+1, maxRetries, fmt.Sprintf("unauthorized (HTTP %d)", resp.StatusCode), delay)
				if werr := waitBackoff(ctx, delay); werr != nil {
					return nil, werr
				}
				rateLimitWaited = true
				continue
			}
			return nil, err
		}
	}

	return nil, fmt.Errorf("%s failed after %d attempts: %w", label, maxRetries, lastErr)
}

// transientEnvelopeRe matches provider error text that clears on its own:
// upstream overloads, timeouts, and OpenRouter's "Provider returned error"
// envelope, which arrives as HTTP 200 with an error body instead of a 5xx.
var transientEnvelopeRe = regexp.MustCompile(`(?i)(provider.{0,20}error|overloaded|temporar\w+|try again|rate.?limit|time\w*\s*out|capacity|upstream.{0,20}error|no endpoints? found)`)

// envelopeError extracts an in-body error envelope of the form
// {"error": {"message": "...", "code": 529}}. It returns the message, the
// numeric code (0 when absent or non-numeric), and whether an envelope was
// present at all.
func envelopeError(body []byte) (string, int, bool) {
	var env struct {
		Error *struct {
			Message string          `json:"message"`
			Code    json.RawMessage `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err != nil || env.Error == nil {
		return "", 0, false
	}
	code := 0
	if len(env.Error.Code) > 0 {
		if n, err := strconv.Atoi(strings.Trim(string(env.Error.Code), `"`)); err == nil {
			code = n
		}
	}
	return env.Error.Message, code, true
}

// doWithEnvelopeRetry is doWithRetry plus one more layer: some gateways
// (notably OpenRouter) report an upstream failure as HTTP 200 carrying an
// error envelope, which the status-code retry above waves through. A transient
// envelope is retried with backoff; a permanent one (bad key, bad model,
// rejected request) fails fast with the code attached so the message names the
// real problem instead of "Provider returned error".
func (c *Client) doWithEnvelopeRetry(ctx context.Context, url string, body []byte, setHeaders func(*http.Request), label string) ([]byte, error) {
	const maxEnvelopeRetries = 3
	for attempt := 0; ; attempt++ {
		respBody, err := c.doWithRetry(ctx, url, body, setHeaders, label)
		if err != nil {
			c.noteFailure(err)
			return nil, err
		}
		msg, code, ok := envelopeError(respBody)
		if !ok {
			return respBody, nil
		}
		if isQuotaExhausted(msg) {
			err := quotaError("")
			c.noteFailure(err)
			return nil, err
		}
		transient := (code == 429 || (code >= 500 && code < 600)) || transientEnvelopeRe.MatchString(msg)
		if !transient {
			err := fmt.Errorf("%s: provider refused the request (code %d): %s", label, code, truncate(msg, 500))
			c.noteFailure(err)
			return nil, err
		}
		if attempt+1 >= maxEnvelopeRetries {
			err := fmt.Errorf("%s: provider errored %d times, last: %s", label, maxEnvelopeRetries, truncate(msg, 300))
			c.noteFailure(err)
			return nil, err
		}
		delay := time.Duration(1<<uint(attempt)) * 2 * time.Second
		delay += time.Duration(rand.Int63n(int64(time.Second)))
		log.Printf("%s: transient provider envelope, retry %d/%d in %v: %s",
			label, attempt+1, maxEnvelopeRetries, delay, truncate(msg, 200))
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func mustMarshalJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

// toolStripInputTokens is the est-input size above which non-core tools are
// dropped to shrink the payload. Both wire paths apply it (FM-L30a): the
// valve used to live in chatWithToolsOpenAI alone, so the anthropic route
// every behavior run uses had no pathological-body safety at all.
// Zero-context: 6500 was a Groq-scoped P1 constant applied to every route, and
// it fired on every request because the system prompt alone is ~8.5K tokens —
// the filter meant as a size valve became a permanent capability amputation.
var toolStripInputTokens = 100_000

var coreToolSet = map[string]bool{
	"read_file": true, "write_file": true, "edit_file": true, "multi_edit": true,
	"delete_file": true,
	"list_files":  true, "grep_codebase": true, "search_files": true, "find_files": true,
	"explore": true, "project_map": true, "codebase_search": true, "codebase_graph": true,
	"inspect_package": true, "read_skill": true,
	"run_typecheck": true, "run_lint": true, "run_checks": true, "run_prettier": true,
	"ask_user": true, "mark_task_done": true,
	// S9 tool contract (ENGINEERING_STANDARDS.md §3): SystemPromptBuild names
	// these by name — the git-native workflow, run_shell probing, install_package,
	// and the bundle/gradle rungs — so the filter must never strip them: a tool
	// the prompt promises and the model never receives is a lie the model plans
	// around (Run 1: "I have no shell"). delete_file joins on Run-1 evidence:
	// plan prune entries need it and it was stripped, so demos could not be
	// removed. Guarded by TestPromptPromisedToolsSurviveCoreFilter.
	"git_status": true, "git_diff": true, "git_log": true,
	"git_commit": true, "git_branch": true,
	"run_shell": true, "install_package": true,
	"run_bundle": true, "run_gradle": true,
	// W5: the language-server senses — read-only and instant, the same
	// contract as read_file/grep. Registered only under TALONSTUDIO_LSP;
	// listed here so agents that have them are allowed to call them.
	// organize_imports writes like edit_file, which is listed above.
	"lsp_diagnostics": true, "lsp_references": true, "lsp_definition": true, "lsp_symbol": true,
	"lsp_organize_imports": true,
}

func filterToCoreTools(tools []map[string]any) []map[string]any {
	var out []map[string]any
	for _, t := range tools {
		if fn, ok := t["function"].(map[string]any); ok {
			if name, ok := fn["name"].(string); ok && coreToolSet[name] {
				out = append(out, t)
			}
		}
	}
	if len(out) < 8 {
		return tools
	}
	return out
}

// filterToCoreToolDefs is filterToCoreTools on the anthropic wire's
// native shape — same threshold, same core set, same <8 keep-original
// guard (one tools-layer law, both wires — FM-L30a). The valve bites
// only bodies past toolStripInputTokens, so an honest request never
// reaches this function.
func filterToCoreToolDefs(tools []ToolDefinition) []ToolDefinition {
	var out []ToolDefinition
	for _, t := range tools {
		if coreToolSet[t.Name] {
			out = append(out, t)
		}
	}
	if len(out) < 8 {
		return tools
	}
	return out
}

// ── OpenAI-compatible path ────────────────────────────

func (c *Client) chatWithToolsOpenAI(ctx context.Context, system string, history []Message, tools []ToolDefinition, sink StreamSink) (*AgenticChatResult, error) {
	messages := toOpenAI(system, history)

	oaiTools := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		// FM-L30a: the description travels whole — a description is a
		// contract with the model. P1 compressed it to its first
		// ≥160-char sentence for a Groq context limit (`7b2f5a4`);
		// the threshold half of that fix was later cured with the
		// valve below, this half was an unconditional capability
		// amputation: run_shell lost its approval flow and ask_user
		// its never-ask-for-secrets rule before the model saw them.
		// One tools-layer law, both wires.
		oaiTools = append(oaiTools, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        t.Name,
				"description": t.Description,
				"parameters":  t.InputSchema,
			},
		})
	}
	// Size valve, not capability valve: drop non-core tools only when the
	// request is genuinely oversized. The original P1 threshold (6500) was
	// tuned for Groq's input budget but applied globally — and the system
	// prompt alone is ~8.5K tokens, so every request stripped its tools and
	// every tool the prompt promised went missing (S9 / ENGINEERING_STANDARDS
	// §3, Run 1). coreToolSet now carries each prompt-named tool, so even a
	// strip that fires keeps the contract; the threshold exists only to shrink
	// payload on a small-context route. 100K is the safety valve against
	// pathological bodies — no route in this deployment declares a budget
	// smaller than it, and an operator adding one sets it alongside model_tier.
	if len(tools) > 12 {
		est := (len(system) + len(mustMarshalJSON(messages)) + len(mustMarshalJSON(oaiTools))) / 4
		if est > toolStripInputTokens {
			oaiTools = filterToCoreTools(oaiTools)
		}
	}

	reqBody := map[string]any{
		"model":      c.model,
		"messages":   messages,
		"max_tokens": maxOutputTokens,
	}
	// A sink means someone is watching: ask the provider to stream so the
	// model's words can be forwarded as they are written instead of all at
	// once when the whole response lands.
	if sink != nil {
		reqBody["stream"] = true
	}
	if len(oaiTools) > 0 {
		reqBody["tools"] = oaiTools
		reqBody["tool_choice"] = "auto"
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}
	// Estimated input size, logged on every call — the reasoning lives with
	// the emitter (logEstInput); this call's only job is the label.
	logEstInput("ChatWithTools(OpenAI):", c.model, body, system, len(history), len(oaiTools))
	// L13 (FM-TELa), corrected by FM-TELe: the per-layer split of the
	// number above — measured from the marshaled body itself, the only
	// object that is the wire truth. system/tools/payloads/results/history
	// + a named envelope sum to body exactly with envelope >= 0; reading
	// the body (not the transcript) is what keeps serializer-dropped blocks
	// from being counted — Run-4 caught thinking bytes in `history` with
	// envelope=-26156. payloads is RESEARCH P0.1's leak bucket (FM-TELb).
	led := AttributeRequestBody(body)
	log.Printf("request-ledger: body=%d system=%d tools=%d payloads=%d results=%d history=%d envelope=%d",
		led.Body, led.System, led.Tools, led.Payloads, led.Results, led.History, led.Envelope)

	// The streaming branch takes its own request path: openStream hands back a
	// live body to read chunk by chunk, whereas doWithEnvelopeRetry buffers the
	// whole response before returning. When a sink is watching, read the SSE
	// chunks as they arrive, forwarding text and reasoning to the sink and
	// accumulating the same fields the buffered path returns — the caller's
	// result contract is unchanged, only its latency is.
	// M2: the read runs under an attempt loop. A provider that drops the
	// connection mid-response, or that closes cleanly without
	// finish_reason or [DONE], gets the SAME request resent
	// byte-identically, up to c.streamRestarts times (hand-built clients
	// with 0 keep the single attempt they have today). Each attempt
	// accumulates into fresh sink state, so text from a killed attempt
	// can never reach the result — the sink may already have forwarded it
	// (accepted cosmetic duplication), but the returned text is the
	// completing attempt's alone. openAIStreamAttempt performs one
	// attempt and classifies how it ended.
	var sinkText, sinkReasoning strings.Builder
	if sink != nil {
		for attempt := 0; ; attempt++ {
			sinkText.Reset()
			sinkReasoning.Reset()

			result, scanErr, complete, fatal := c.openAIStreamAttempt(ctx, body, sink, &sinkText, &sinkReasoning)
			if fatal != nil {
				return nil, fatal
			}
			if scanErr == nil && complete {
				return result, nil
			}
			if attempt >= c.streamRestarts {
				// Out of budget. A read error stays terminal with the
				// exact wording clients match on; a clean-but-truncated
				// close is accepted as-is — what this branch has always
				// done when no restart budget existed.
				if scanErr != nil {
					return nil, fmt.Errorf("the AI stream ended early: %w", scanErr)
				}
				log.Printf("ChatWithTools(OpenAI stream): no finish after %d attempt(s); accepting partial (%d chars)",
					attempt+1, len(result.Text))
				return result, nil
			}
			if ctx.Err() != nil {
				// A deliberate cancel (client hang-up) is not a restartable
				// failure: logging "restarting byte-identically" promises a
				// retry the dead context kills on arrival (Run 1's ghost
				// attempt after the stop button). Report the cancel itself.
				return nil, ctx.Err()
			}
			log.Printf("ChatWithTools(OpenAI stream): attempt %d/%d ended early (complete=%v err=%v); restarting byte-identically",
				attempt+1, c.streamRestarts+1, complete, scanErr)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(streamRestartBackoff):
			}
		}
	}

	respBody, err := c.doWithEnvelopeRetry(ctx, c.baseURL+"/chat/completions", body, func(req *http.Request) {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
		req.Header.Set("HTTP-Referer", "https://talon.app")
		req.Header.Set("X-Title", "Talon")
	}, "ChatWithTools(OpenAI)")
	return c.parseBufferedOpenAITools(ctx, respBody)
}

// openAIStreamAttempt performs ONE streaming attempt of the sink branch
// (M2) and classifies how it ended:
//
//	fatal  != nil          terminal: openStream exhausted its own
//	                        retries, the provider sent an error frame,
//	                        or a buffered body failed to parse. Return
//	                        as-is, never restarted.
//	scanErr != nil         a mid-stream read failure — restart
//	                        candidate; at budget end it becomes the
//	                        terminal "the AI stream ended early".
//	complete               finish_reason or [DONE] was seen: done.
//	!complete && scanErr   a clean-but-truncated close — restart
//	    == nil             candidate; at budget end the partial is
//	                        accepted, as it always was.
//
// result is assembled from THIS attempt's sink state alone (nil only
// when the attempt never reached reading — the fatal paths), so a killed
// attempt's text can never leak into what the turn returns. The stream
// body is closed before this returns, so a restart never inherits one.
func (c *Client) openAIStreamAttempt(
	ctx context.Context,
	body []byte,
	sink StreamSink,
	sinkText, sinkReasoning *strings.Builder,
) (result *AgenticChatResult, scanErr error, complete bool, fatal error) {
	stream, err := c.openStream(ctx, c.baseURL+"/chat/completions", body, func(req *http.Request) {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
		req.Header.Set("HTTP-Referer", "https://talon.app")
		req.Header.Set("X-Title", "Talon")
	})
	if err != nil {
		return nil, nil, false, err
	}
	defer stream.Close()

	// Not every provider honours the stream flag — some return one buffered
	// JSON object no matter what was asked. Peek the head of the body: an
	// SSE stream begins with "data:", and anything else means the response
	// is a single object that must be parsed whole. Serving both shapes is
	// what keeps this branch safe against a provider that ignores the flag.
	buf := bufio.NewReaderSize(stream, 256*1024)
	head, _ := buf.Peek(64)
	if !bytes.HasPrefix(bytes.TrimLeft(head, " \t\r\n"), []byte("data:")) {
		bodyBytes, err := io.ReadAll(buf)
		if err != nil {
			// A buffered body cut short is a read failure like any other:
			// it joins the restart decision instead of dying outright.
			return nil, err, false, nil
		}
		parsed, perr := c.parseBufferedOpenAITools(ctx, bodyBytes)
		if perr != nil {
			return nil, nil, false, perr
		}
		return parsed, nil, true, nil
	}
	scanner := newSSEScanner(buf)
	var toolCalls []struct {
		ID       string `json:"id"`
		Function struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"function"`
		ExtraContent struct {
			Google struct {
				ThoughtSignature string `json:"thought_signature"`
			} `json:"google"`
		} `json:"extra_content"`
	}
	var finishReason string
	var sawDone bool
	var usage struct {
		PromptTokens        int `json:"prompt_tokens"`
		CompletionTokens    int `json:"completion_tokens"`
		PromptTokensDetails struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
	}
	for scanner.Next() {
		event := scanner.Event()
		if len(event.Data) == 0 {
			continue
		}
		if bytes.Equal(bytes.TrimSpace(event.Data), []byte("[DONE]")) {
			sawDone = true
			break
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content          string `json:"content"`
					Reasoning        string `json:"reasoning"`
					ReasoningContent string `json:"reasoning_content"`
					ToolCalls        []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
			Usage *struct {
				PromptTokens        int `json:"prompt_tokens"`
				CompletionTokens    int `json:"completion_tokens"`
				PromptTokensDetails struct {
					CachedTokens int `json:"cached_tokens"`
				} `json:"prompt_tokens_details"`
			} `json:"usage"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(event.Data, &chunk); err != nil {
			continue // keep-alives and provider-specific frames are not fatal
		}
		if chunk.Error != nil {
			return nil, nil, false, fmt.Errorf("provider error: %s", chunk.Error.Message)
		}
		if chunk.Usage != nil {
			usage = *chunk.Usage
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		choice := chunk.Choices[0]

		reasoning := choice.Delta.Reasoning
		if reasoning == "" {
			reasoning = choice.Delta.ReasoningContent
		}
		if reasoning != "" {
			sinkReasoning.WriteString(reasoning)
			sink(reasoning, true)
		}
		if choice.Delta.Content != "" {
			sinkText.WriteString(choice.Delta.Content)
			sink(choice.Delta.Content, false)
		}
		// Tool calls arrive incrementally: the name and id land on the first
		// fragment for an index, arguments arrive as successive fragments
		// that must be concatenated in order.
		for _, tc := range choice.Delta.ToolCalls {
			for tc.Index >= len(toolCalls) {
				toolCalls = append(toolCalls, struct {
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
					ExtraContent struct {
						Google struct {
							ThoughtSignature string `json:"thought_signature"`
						} `json:"google"`
					} `json:"extra_content"`
				}{})
			}
			if tc.ID != "" {
				toolCalls[tc.Index].ID = tc.ID
			}
			if tc.Function.Name != "" {
				toolCalls[tc.Index].Function.Name = tc.Function.Name
			}
			toolCalls[tc.Index].Function.Arguments += tc.Function.Arguments
			// L8: argument fragments are progress the sink cannot represent
			// (its contract is text/thinking) — report them as they land, so
			// the wire is not silent for the minutes a large write_file input
			// generates (Run 2's 191 s top gap).
			if tc.Function.Arguments != "" && c.toolArgs != nil {
				c.toolArgs(toolCalls[tc.Index].Function.Name, tc.Function.Arguments)
			}
		}
		if choice.FinishReason != "" {
			finishReason = choice.FinishReason
		}
	}
	if err := scanner.Err(); err != nil {
		scanErr = err
	}
	complete = sawDone || finishReason != ""

	result = &AgenticChatResult{
		Text: sinkText.String(),
		Usage: Usage{
			InputTokens:     usage.PromptTokens,
			OutputTokens:    usage.CompletionTokens,
			CacheReadTokens: usage.PromptTokensDetails.CachedTokens,
		},
	}
	if sinkReasoning.Len() > 0 {
		result.Blocks = append(result.Blocks, Block{Type: "thinking", Text: sinkReasoning.String()})
	}
	if result.Text != "" {
		result.Blocks = append(result.Blocks, Block{Type: "text", Text: result.Text})
	}
	for _, tc := range toolCalls {
		args := tc.Function.Arguments
		if args == "" {
			args = "{}"
		}
		id := tc.ID
		if id == "" {
			id = fmt.Sprintf("call_%s_%d", tc.Function.Name, len(result.ToolUses))
		}
		result.ToolUses = append(result.ToolUses, ToolUseBlock{
			Type: "tool_use", ID: id, Name: tc.Function.Name, Input: json.RawMessage(args),
		})
		result.Blocks = append(result.Blocks, Block{
			Type: "tool_use", ID: id, Name: tc.Function.Name, Input: json.RawMessage(args),
		})
	}
	switch {
	case len(result.ToolUses) > 0:
		result.StopReason = "tool_use"
	case finishReason == "length":
		result.StopReason = "max_tokens"
	default:
		result.StopReason = "end_turn"
	}
	log.Printf("ChatWithTools(OpenAI stream): stop=%s text=%d tools=%d in=%d out=%d complete=%v",
		result.StopReason, len(result.Text), len(result.ToolUses),
		result.Usage.InputTokens, result.Usage.OutputTokens, complete)
	return result, scanErr, complete, nil
}

// parseBufferedOpenAITools turns one whole Chat Completions object into an
// AgenticChatResult. Shared by the buffered path and by the streaming path when
// a provider ignores the stream flag and answers with a single object anyway.
func (c *Client) parseBufferedOpenAITools(_ context.Context, respBody []byte) (*AgenticChatResult, error) {
	var oaiResp struct {
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Content   string `json:"content"`
				Reasoning string `json:"reasoning"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
					// Gemini echoes a thought_signature under extra_content.google
					// on EACH tool call it returns. Per Google's docs the signature
					// travels positionally with the part that issued it - parallel
					// calls only carry one on the first part - and must be echoed
					// back exactly as received on the same part.
					ExtraContent struct {
						Google struct {
							ThoughtSignature string `json:"thought_signature"`
						} `json:"extra_content"`
					} `json:"extra_content"`
				} `json:"tool_calls"`
				// Gemini echoes a thought_signature under extra_content.google that
				// must be round-tripped on the assistant turn it came from.
				ExtraContent struct {
					Google struct {
						ThoughtSignature string `json:"thought_signature"`
					} `json:"extra_content"`
				} `json:"extra_content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens        int `json:"prompt_tokens"`
			CompletionTokens    int `json:"completion_tokens"`
			PromptTokensDetails struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
		} `json:"usage"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}

	if err := json.Unmarshal(respBody, &oaiResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w (body: %s)", err, truncate(string(respBody), 300))
	}
	if oaiResp.Error.Message != "" {
		return nil, fmt.Errorf("provider error: %s", oaiResp.Error.Message)
	}
	if len(oaiResp.Choices) == 0 {
		return nil, fmt.Errorf("no choices in response: %s", truncate(string(respBody), 300))
	}

	choice := oaiResp.Choices[0]
	result := &AgenticChatResult{
		Text: choice.Message.Content,
		Usage: Usage{
			InputTokens:     oaiResp.Usage.PromptTokens,
			OutputTokens:    oaiResp.Usage.CompletionTokens,
			CacheReadTokens: oaiResp.Usage.PromptTokensDetails.CachedTokens,
		},
	}

	// The model's private reasoning comes first - it is what drove the text and
	// the tool calls that follow, so the user reads the decision before the act.
	// Plan mode streams this live; build mode used to vanish it entirely, so a
	// user watching a build saw tool rows stacking up with no idea what the model
	// was deciding.
	if choice.Message.Reasoning != "" {
		result.Blocks = append(result.Blocks, Block{Type: "thinking", Text: choice.Message.Reasoning})
	}
	if choice.Message.Content != "" {
		result.Blocks = append(result.Blocks, Block{Type: "text", Text: choice.Message.Content})
	}
	for _, tc := range choice.Message.ToolCalls {
		args := tc.Function.Arguments
		if args == "" {
			args = "{}"
		}
		// Some providers emit tool_calls with no id; synthesise one so the
		// tool_result we send back can be matched.
		id := tc.ID
		if id == "" {
			id = fmt.Sprintf("call_%s_%d", tc.Function.Name, len(result.ToolUses))
		}
		// Gemini returns the signature on each tool call part; some responses
		// put it once on the whole message instead. Prefer the per-call value -
		// parallel calls only sign the first part, and that positional match is
		// what the API validates on the next request.
		sig := tc.ExtraContent.Google.ThoughtSignature
		if sig == "" {
			sig = choice.Message.ExtraContent.Google.ThoughtSignature
		}
		result.ToolUses = append(result.ToolUses, ToolUseBlock{
			Type: "tool_use", ID: id, Name: tc.Function.Name, Input: json.RawMessage(args),
		})
		result.Blocks = append(result.Blocks, Block{
			Type: "tool_use", ID: id, Name: tc.Function.Name, Input: json.RawMessage(args),
			// Gemini requires its thought_signature echoed back on the same
			// part when we replay this tool call on the next request.
			ThoughtSignature: sig,
		})
	}

	// Normalise finish_reason onto the Anthropic vocabulary so the caller's
	// loop has one set of stop conditions to reason about.
	switch {
	case len(result.ToolUses) > 0:
		result.StopReason = "tool_use"
	case choice.FinishReason == "length":
		result.StopReason = "max_tokens"
	default:
		result.StopReason = "end_turn"
	}

	log.Printf("ChatWithTools(OpenAI): stop=%s text=%d tools=%d in=%d out=%d",
		result.StopReason, len(result.Text), len(result.ToolUses),
		result.Usage.InputTokens, result.Usage.OutputTokens)

	return result, nil
}

// ── Anthropic path ────────────────────────────────────

func (c *Client) chatWithToolsAnthropic(ctx context.Context, system string, history []Message, tools []ToolDefinition, sink StreamSink) (*AgenticChatResult, error) {
	// Cache the system prompt and tool definitions (both stable across a
	// build) plus the conversation prefix up to the last complete turn.
	breakpoint := len(history) - 2
	if breakpoint < 0 {
		breakpoint = -1
	}

	reqBody := map[string]any{
		"model":      c.model,
		"max_tokens": maxOutputTokens,
		"system": []map[string]any{{
			"type":          "text",
			"text":          system,
			"cache_control": map[string]string{"type": "ephemeral"},
		}},
		"messages": toAnthropic(history, breakpoint),
	}
	// FM-L30a: one tools-layer law, both wires — the same size valve the
	// openai path has always applied, same threshold, same core set,
	// same guards. Before this, the wire every behavior run uses sent
	// every tool on any body: a pathological one (RESEARCH measured
	// 294KB) had no safety here at all. The est mirrors the openai
	// formula exactly — system + marshaled messages + marshaled tools,
	// /4 — so both wires trip at the same body.
	wireTools := tools
	if len(wireTools) > 12 {
		est := (len(system) + len(mustMarshalJSON(reqBody["messages"])) + len(mustMarshalJSON(wireTools))) / 4
		if est > toolStripInputTokens {
			wireTools = filterToCoreToolDefs(wireTools)
		}
	}
	if len(wireTools) > 0 {
		reqBody["tools"] = wireTools
	}
	if sink != nil {
		// FM-ANTa: a sink means the caller is watching the wire, so the
		// round streams and each delta is forwarded as it arrives. A nil
		// sink keeps today's buffered shape — buffering on request is
		// correct; buffering unconditionally was the defect (Run-8's
		// S12 measured it as a 133s hole in the wire).
		reqBody["stream"] = true
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}
	// L13 (FM-TELa), corrected by FM-TELe: same per-layer ledger as the
	// OpenAI path — one body-reading attributer for both routes, so a
	// serializer drop (thinking replays here, but plan blocks never do)
	// can never be counted as sent.
	led := AttributeRequestBody(body)
	log.Printf("request-ledger: body=%d system=%d tools=%d payloads=%d results=%d history=%d envelope=%d",
		led.Body, led.System, led.Tools, led.Payloads, led.Results, led.History, led.Envelope)
	// FM-TELf: the same estimate line the OpenAI path has always logged,
	// emitted here too — this is the path Run-7's scorecard found missing
	// (60 ledger lines, zero est_input_tok), which read as `?` on S9/S10
	// and blocked the run under §5.
	logEstInput("ChatWithTools(Anthropic):", c.model, body, system, len(history), len(wireTools))

	setHeaders := func(req *http.Request) {
		req.Header.Set("x-api-key", c.apiKey)
		req.Header.Set("anthropic-version", "2023-06-01")
		// User-Agent comes from newRequest (FM-UAa): the identity is
		// attached in one place, never per call site.
	}

	var result *AgenticChatResult
	if sink == nil {
		respBody, err := c.doWithEnvelopeRetry(ctx, c.messagesURL(), body, setHeaders, "ChatWithTools(Anthropic)")
		if err != nil {
			return nil, err
		}

		var agenticResp AgenticResponse
		if err := json.Unmarshal(respBody, &agenticResp); err != nil {
			return nil, fmt.Errorf("failed to parse response: %w (body: %s)", err, truncate(string(respBody), 300))
		}

		result = &AgenticChatResult{
			StopReason: agenticResp.StopReason,
			Usage: Usage{
				InputTokens:      agenticResp.Usage.InputTokens,
				OutputTokens:     agenticResp.Usage.OutputTokens,
				CacheReadTokens:  agenticResp.Usage.CacheReadInputTokens,
				CacheWriteTokens: agenticResp.Usage.CacheCreationInputTokens,
			},
		}

		for _, block := range agenticResp.Content {
			switch block.Type {
			case "text":
				result.Text += block.Text
				result.Blocks = append(result.Blocks, Block{Type: "text", Text: block.Text})
			case "thinking":
				result.Blocks = append(result.Blocks, Block{Type: "thinking", Text: block.Thinking})
			case "tool_use":
				result.ToolUses = append(result.ToolUses, ToolUseBlock{
					Type: "tool_use", ID: block.ID, Name: block.Name, Input: block.Input,
				})
				result.Blocks = append(result.Blocks, Block{
					Type: "tool_use", ID: block.ID, Name: block.Name, Input: block.Input,
				})
			}
		}
	} else {
		respBody, err := c.openStream(ctx, c.messagesURL(), body, setHeaders)
		if err != nil {
			return nil, err
		}
		defer respBody.Close()

		// FM-ANTa: one reader parses anthropic's stream for both its
		// consumers. This adapter forwards each delta to the sink as it
		// arrives — thinking marked exactly as the openai branch marks
		// it — while the reader assembles the same result the buffered
		// path above returns, so streaming changes latency, not contract.
		result, err = readAnthropicStream(respBody, &anthropicStreamHooks{
			onStart: func(string, string) bool { return true },
			onDelta: func(kind, delta string) bool {
				sink(delta, kind == "thinking")
				return true
			},
			onStop: func(string) bool { return true },
		})
		if err != nil {
			return nil, err
		}
		if result.StopReason == "" {
			// The stream closed without the gateway's word on how the
			// round ended. Accepting it would be a silently toolless
			// round — upstream's retry ladder gets a failed round it can
			// actually retry instead.
			return nil, fmt.Errorf("the AI stream ended before the model finished (no stop_reason)")
		}
	}

	log.Printf("ChatWithTools(Anthropic): stop=%s text=%d tools=%d in=%d out=%d cached=%d",
		result.StopReason, len(result.Text), len(result.ToolUses),
		result.Usage.InputTokens, result.Usage.OutputTokens, result.Usage.CacheReadTokens)

	return result, nil
}
