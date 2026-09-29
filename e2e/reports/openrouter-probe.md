# OpenRouter free-model probe — three candidates (2026-09-29)

Short, simple, direct-API probe of the three operator-nominated free
models. No server, no scorecard — a pre-filter before any candidate
earns a harness run. Probe script: `/tmp/opencode/orprobe.py` (temp,
same convention as L30's measurement probes).

## Method

Three stages per model, sequential, ~6–20 s paced for the free tier:

1. **ping** — one-shot, does the slug respond at all (512-token budget:
   reasoning models burn hidden tokens before prose).
2. **tool contract** — `tools=[write_file, read_file]`, one instruction
   to create `app/Welcome.tsx`. Pass = valid `tool_calls`, `write_file`
   chosen, arguments parse as JSON, path ends `.tsx`, content sane.
3. **plan-sized** — the `.env.example` bar: a 6-screen plan
   (≥4 features + ≥2 acceptance criteria per screen, packages, risks),
   3072-token cap, `max_tokens` raised to 6144 where reasoning eats the
   budget. Must finish inside the provider's ~5-minute window **and**
   parse as JSON — Talon's plan door strict-unmarshals (L26: opens with
   `{` but fails to parse ⇒ terminal failed turn).

Key: OpenRouter `8768…889` (masked), fresh daily quota. **Total probe
cost: $0.024** (free slugs $0; the paid-qwen fair retest ~2¢).

## Results

| Model | ping | tool contract | plan-sized | JSON |
|---|---|---|---|---|
| `qwen/qwen3.8-27b:free` | ❌ **429 ×18 over ~20 min** — sole free provider `ModelRun` (fp4) shared pool saturated | — | — | — |
| `qwen/qwen3.8-27b` *(paid id, same weights — Mancer 2 / Darkbloom)* | ✅ 1.8 s | ✅ 4.3 s | ✅ **46.5 s**, 3088 tok, 66.4 tok/s | ✅ 6 screens / 24 features |
| `poolside/laguna-s-2.1:free` | ✅ 1.9 s (needed 1–2 × 429 retries) | ✅ 9.7 s / 7.3 s | ✅ 12.6 s / 502 tok / 40.0 tok/s · 23.4 s / 914 tok / 39.1 tok/s | ✅ **2/2** (6 screens; 24 and 30 features) |
| `cohere/north-mini-code:free` | ✅ 0.9–1.9 s | ✅ 3.1 s / 4.9 s | ✅ speed: 44.3 s / 1761 tok / 39.7 tok/s · 26.7 s / 2942 tok / **110.1 tok/s** | ❌ **0/2** |

## Findings

- **qwen `:free` is unusable right now.** One provider behind the free
  route (`ModelRun`, fp4), persistent upstream 429 across ~18 attempts
  / ~20 minutes. The **paid id of the same weights works** and is the
  fastest measured here: 66.4 tok/s, clean JSON — but only after
  raising the output cap: it is a **reasoning model** (~8 KB of
  reasoning before the plan), so a 3072-token cap truncates it
  (`finish=length`, invalid JSON — an artefact of the cap, proven by
  the 6144 rerun completing at `finish=stop` in 46.5 s). For Talon:
  the plan turn already allows 32768 (L26), so the cap is fine; a
  reasoning model costs output budget, not correctness.
- **laguna is the cleanest of the three**: 2/2 valid tool calls, 2/2
  valid JSON plans, exact-on-shape and terse (502 tok for a full
  6×4 plan), steady ~39–40 tok/s, well inside every bar. Free-tier
  429s appear but retry cleanly.
- **cohere is fast but structurally undisciplined in this probe**:
  tool contract 2/2 ✅, speed bar ✅ (39.7 → 110.1 tok/s across runs),
  but **0/2 plans parsed** — run 1 emitted one stray `}` after an
  otherwise-complete object (3412 of 3413 chars valid), run 2 closed
  an object prematurely at char 3442. Both tails, `finish=stop`, not
  truncated. Under Talon's strict plan door that is two failed plan
  turns; whether its prescriptive plan prompt + retry ladder rescues it
  is a harness-run question this probe does not answer.
- All three serve 256–262K context — compatible with the 200K
  compaction ceiling (L31: peak request ≈ 229K).

## Verdict

| Candidate | Pre-filter verdict |
|---|---|
| `poolside/laguna-s-2.1:free` | **advance to a short harness run** — clean contract, clean JSON, cheapest wall time |
| `cohere/north-mini-code:free` | advance **with the JSON flag** — needs one plan-door attempt through the real prompt before any adoption decision |
| `qwen/qwen3.8-27b:free` | **blocked on availability** (sole free provider saturated); paid id is the strongest performer measured — revisit the free route later or accept ~2¢/run on the paid id |

Next step when wanted: a single short build turn through
`run-behavior.sh` with `AI_MIN_REQUEST_INTERVAL=8s` (the .env knob
already exists for exactly this) against each advancing candidate.
