# Breaktest — enumerated failure modes (written before the code)

E2E break-test of the backend API. Sole testing mechanism: a live driver
(`backend/cmd/breaktest`) against a real stack (`e2e/run-breaktest.sh`).
Every case below is enumerated *before* the harness exists, with its
expected behavior; the driver asserts them by the same IDs and publishes a
scorecard to `e2e/reports/breaktest-latest.md` (override: `BREAKTEST_REPORT`).

Stack: scratch DB + workspaces, ports 8096 (server) / 8095 (stub OAuth) /
8094 (stub model gateway). The stub gateway streams slowly while a turn is
meant to be stopped mid-flight and answers instantly otherwise — stop/resume
timing is deterministic and the run costs nothing.

## Rate / quota budget (code-derived)

- Prompt-metered routes: `POST /v1/prompts`, `/stream`, `/resume`,
  `/answer`, `/steer` — token bucket **5/min per user** (`promptLimiter`).
  Everything else (GET, discard, project/file/conversation routes) is unmetered.
  The driver *reserves* metered calls per user so only the deliberate
  `BT-7.13` case ever trips 429.
- Auth exchange: **10/min per IP** — three logins + one bad-code probe fits.
- Free-tier quota: 20 metered prompts/cycle/user; this run spends ≤ 5.

## 1. monitor

| ID | Case | Expected |
|----|------|----------|
| BT-1.1 | `GET /healthz` unauthenticated | 200 |
| BT-1.2 | `GET /readyz` unauthenticated | 200 |
| BT-1.3 | `GET /v1/metrics` no auth | 401 |
| BT-1.4 | `GET /v1/errors` no auth | 401 |
| BT-1.5 | `GET /v1/metrics` with JWT | 200, non-empty body |
| BT-1.6 | `GET /v1/errors` with JWT | 200 |
| BT-1.7 | `Authorization: Bearer garbage` | 401 (not 500) |
| BT-1.8 | `Authorization: Basic xyz` (wrong scheme) | 401 |
| BT-1.9 | unknown route `/definitely/not/a/route` | 404 JSON (no stack) |
| BT-1.10 | `GET /healthz` after the whole abuse run | 200 (server survived) |

## 2. auth

| ID | Case | Expected |
|----|------|----------|
| BT-2.1 | alice: full OAuth flow via stub (start→consent→callback→exchange) | JWT, `/v1/me` 200 |
| BT-2.2 | bob: second full flow | JWT, **different** user id than alice |
| BT-2.3 | carol: third full flow | JWT, different id again |
| BT-2.4 | `POST /v1/auth/exchange` with bogus code | 4xx (not 500) |

## 3. project

| ID | Case | Expected |
|----|------|----------|
| BT-3.1 | create `Breaktest Alpha` | 201, id > 0, workspace dir exists under `WORKSPACE_DIR` |
| BT-3.2 | create empty name / 121-char name | 400 each |
| BT-3.3 | create 120-char name | 201 |
| BT-3.4 | malformed JSON body | 400 |
| BT-3.5 | duplicate name | 201, distinct slug |
| BT-3.6 | name `../../etc/passwd` | 201 and workspace path still inside `WORKSPACE_DIR` (no escape) |
| BT-3.7 | ids `0`, `-1`, `abc`, `1e20` → GET | 400; `9999999999` → 404; **never 500** |
| BT-3.8 | bob on alice's project: GET / PUT plan / DELETE / GET files / PUT files / DELETE file | 404 ×6; alice's project untouched afterwards |
| BT-3.9 | PUT plan invalid JSON → then valid plan | 400 then 200; GET reflects the plan |
| BT-3.10 | create with ~16 MB JSON body | completes < 20 s, not 500 (record actual; no global body cap exists — documented, not asserted) |
| BT-3.11 | bob `GET/POST …/conversations` on alice's project | 404 |

## 4. files (dedicated project `Breaktest Files`)

| ID | Case | Expected |
|----|------|----------|
| BT-4.1 | write valid file → read tree → delete it → delete again | 200, present, 200, 404 |
| BT-4.2 | write `../escape.txt` | 400; no file appears in `WORKSPACE_DIR` |
| BT-4.3 | write `/tmp/bt-escape.txt` (absolute) | 400; file not created |
| BT-4.4 | write `node_modules/evil.js` | 400 |
| BT-4.5 | write path containing a NUL byte | 400 |
| BT-4.6 | write 3 MiB file (over 2 MiB cap) | 413, nothing written |
| BT-4.7 | batch write: 1 good + 1 traversal path | 400 **and the good file not written** (atomicity) |
| BT-4.8 | `DELETE …/files?path=.` (also `./`, `app/..`) | **400 — root must be refused; `os.RemoveAll(workspace)` must never run** (suspect: `resolveInside` returns `"."`, which is not in `skipDirs` → workspace wipe) |
| BT-4.9 | `DELETE …/files?path=.git` | 400 (skipDirs) |

## 5. bob setup

| ID | Case | Expected |
|----|------|----------|
| BT-5.1 | bob creates `Breaktest Beta` | 201 |
| BT-5.2 | bob runs one fast build turn over SSE | `done` success, status `completed` |
| BT-5.3 | bob conversation listing | ≥1 turn, conversation id captured for isolation cases |
| BT-5.4 | alice runs a marker turn on `Breaktest Alpha` | completed (her history is non-empty for BT-6.10) |

## 6. isolation (cross-tenant + paging/search edges)

| ID | Case | Expected |
|----|------|----------|
| BT-6.1 | alice `GET her project /conversation?conversation_id=<bob's>` | **no foreign turns** (4xx or empty; suspect: `GetConversationThread` joins only on `conversation_id`) |
| BT-6.2 | `conversation_id=99999999999` | 4xx/200-empty, never 500 |
| BT-6.3 | alice `POST /v1/prompts` on **her** project with `conversation_id=<bob's>` (metered) | **4xx rejected** (suspect: `validate` never checks thread ownership → foreign-thread write + `TouchConversation` on victim's thread) |
| BT-6.4 | bob re-reads his thread | must not contain alice's probe text |
| BT-6.5 | `?limit=-1` / `limit=0` / `limit=abc` | 200, bounded (≤ 500 rows), never 500 (suspect: `limit<=0` returns whole transcript) |
| BT-6.6 | `before_seq=0` / `=-3` / `=abc` | 400; valid → 200 |
| BT-6.7 | search `q=1 char`, `q=201 chars` | 400; `q=100%`, `q=" OR 1=1 --` | 200 (FTS metachars tolerated) |
| BT-6.8 | prompts list `limit=100000` → ≤100 rows; `limit=0` → default; `before=abc`/`-1` → 400 | as stated |
| BT-6.9 | bob reads alice's conversation / prompts / memory / audit / checkpoints / feedback / files | 404 ×7 |
| BT-6.10 | bob clears alice's conversation | 404, her turns survive |

## 7. turns — stop mid-run, resume, races

Stop semantics: there is no `/stop`; a stop is the client closing the SSE
connection → `r.Context()` cancel → loop returns ctx error →
`terminalStatus` ⇒ `cancelled` (resumable).

| ID | Case | Expected |
|----|------|----------|
| BT-7.1 | alice opens a build turn (stub slow) | ≥1 SSE event within 30 s |
| BT-7.1b | the running turn's id is captured | prompt id known, status `running` |
| BT-7.2 | while running: `steer` | 202 |
| BT-7.3 | while running: `discard` | 409 (session guard) |
| BT-7.4 | while running: `resume` (metered) | 409 `still running` |
| BT-7.5 | abort the SSE connection mid-turn → poll status ≤ 30 s | terminal **`cancelled`** |
| BT-7.6 | conversation after abort | `latest.status == cancelled`, turn still in history |
| BT-7.7 | concurrent double `resume` ×2 (metered ×2) | **exactly one** streams (200) and one 409 — two 200s = TOCTOU double loop (suspect: `GetSession` check then `CreateSession` overwrite) |
| BT-7.8 | `resume` on completed | 409 |
| BT-7.9 | `discard` on completed | 409 and status **stays `completed`** (suspect: no status guard → flips to `abandoned`) |
| BT-7.10 | second full cycle: slow → abort → `cancelled` → resume (metered ×2) | resumes to `completed`; **exactly one** user-turn row for that prompt (no duplicate after resume) |
| BT-7.11 | `answer` on a completed turn (metered) | 409 |
| BT-7.12 | `steer` on a finished turn (metered) | 409 |
| BT-7.13 | carol fires 6 metered calls in one window | first five not-429 (404 fine), sixth **429** with `Retry-After` |
| BT-7.14 | `GET /v1/usage` | 200 (record prompts_used) |

## 8. delete

| ID | Case | Expected |
|----|------|----------|
| BT-8.1 | delete a fresh, childless project | 200, then GET 404, workspace dir gone |
| BT-8.2 | delete a **used** project (has prompts/conversations/messages) | **200** (suspect: FKs are ON, no `ON DELETE CASCADE`, `DeleteProject` deletes only the projects row → `FOREIGN KEY constraint failed` → 500) |
| BT-8.3 | delete while a turn is running (bob, stub slow) | **409**; suspect order: 500 (FK) today, and after FK fix the workspace could be destroyed under a live loop (no guard) |

## Suspects (from code reading, confirmed live by the IDs above)

1. `?path=.` / `./` / `app/..` → `resolveInside` yields `"."`, `skipDirs`
   misses it → `os.RemoveAll` wipes the workspace. (BT-4.8)
2. `conversation_id` never validated against the project, read or write
   direction → cross-tenant thread read + foreign-thread write.
   (BT-6.1 / BT-6.3 / BT-6.4)
3. `DeleteProject` is a bare `DELETE FROM projects` under
   `_pragma=foreign_keys(ON)` with no cascade and no child cleanup →
   500 on any used project. (BT-8.2)
4. Concurrent `Resume`/`Answer` claim the turn with a check-then-set
   (`GetSession` nil → later `CreateSession` overwrites the map entry) →
   two loops on one workspace. (BT-7.7)
5. `Discard` has no status guard → a `completed` turn flips to `abandoned`.
   (BT-7.9)
6. Delete-while-running has no guard. (BT-8.3)

Non-asserted, recorded if seen: unbounded JSON bodies (no `MaxBytesReader`
outside the billing webhook), `limit<=0` unbounded transcript reads.
