package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/luckyisrael/talon/backend/internal/secrets"
)

// ErrDuplicateEmail is returned by CreateUser when the address is already taken,
// so the handler can tell that apart from a database failure.
var ErrDuplicateEmail = errors.New("email already registered")

// ErrUserNotFound is the sentinel for lookups that miss: the OAuth flow
// distinguishes "no such identity yet" (create the user) from a store fault.
var ErrUserNotFound = errors.New("user not found")

// ErrChallengeNotFound is the wallet-auth miss: the challenge was never
// issued, was already used (single-use by construction — the read deletes),
// or expired and was swept. All three look identical to the caller, on
// purpose: never tell a forger which.
var ErrChallengeNotFound = errors.New("challenge not found")

type User struct {
	ID           int64     `json:"id"`
	Email        string    `json:"email"`
	PasswordHash string    `json:"-"`
	Tier         string    `json:"tier"`
	DisplayName  string    `json:"display_name"`
	CreatedAt    time.Time `json:"created_at"`
}

type Project struct {
	ID              int64     `json:"id"`
	UserID          int64     `json:"user_id"`
	Name            string    `json:"name"`
	Slug            string    `json:"slug"`
	AndroidPackage  string    `json:"android_package"`
	IOSBundleID     string    `json:"ios_bundle_id"`
	Scheme          string    `json:"scheme"`
	PlanJSON        string    `json:"plan_json,omitempty"`
	Status          string    `json:"status"`
	TemplateVersion string    `json:"template_version,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}

type Prompt struct {
	ID        int64  `json:"id"`
	ProjectID int64  `json:"project_id"`
	Mode      string `json:"mode"`
	Prompt    string `json:"prompt"`
	// Title is the H13 session title: the human-facing line history lists
	// render — LLM-titled from the first prompt when a client answers, the
	// honest truncated prompt otherwise.
	Title    string `json:"title,omitempty"`
	Response string `json:"response,omitempty"`
	// Status is the run state of the turn this prompt drove: running while the
	// agent loop is live, then a terminal completed | cancelled | failed, or
	// abandoned when the user discards an interrupted turn. A row left at
	// running is how a server crash is told apart from a clean finish — the
	// only state the loop never writes itself.
	Status string `json:"status"`
	// Round is how far the agent loop got, for a resumable turn's progress hint.
	Round int `json:"round"`
	// SkippedGatesJSON holds the verify-ladder stages that could not run when
	// the turn verified (missing infra) — JSON string array. "Verified" then
	// honestly means "everything that could run, ran", and the UI can offer
	// to run the missing gates.
	SkippedGatesJSON string     `json:"skipped_gates,omitempty"`
	UpdatedAt        *time.Time `json:"updated_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	// FilesTouched is the union of workspace paths every leg of this turn
	// changed — the durable cross-leg set. Each leg's tools.NewFS journal is
	// run-scoped, so without this row a resumed turn that writes nothing new
	// would claim no changes and skip verify over earlier legs' files, while
	// export fails closed on the missing project_verify_state row. Empty
	// omits the key on the wire.
	FilesTouched []string `json:"files_touched,omitempty"`
}

type Session struct {
	ID             int64      `json:"id"`
	ProjectID      int64      `json:"project_id"`
	State          string     `json:"state"`
	ContainerID    string     `json:"container_id,omitempty"`
	ContainerName  string     `json:"container_name,omitempty"`
	ADBPort        int        `json:"adb_port,omitempty"`
	ContainerState string     `json:"container_state,omitempty"`
	StartedAt      time.Time  `json:"started_at"`
	EndedAt        *time.Time `json:"ended_at,omitempty"`
}

type Usage struct {
	ID            int64     `json:"id"`
	UserID        int64     `json:"user_id"`
	PromptsUsed   int       `json:"prompts_used"`
	DeviceMinutes float64   `json:"device_minutes"`
	CostUSD       float64   `json:"cost_usd"`
	PeriodStart   time.Time `json:"period_start"`
}

type ProjectMemory struct {
	ID        int64     `json:"id"`
	ProjectID int64     `json:"project_id"`
	EntryType string    `json:"entry_type"`
	Content   string    `json:"content"`
	Metadata  string    `json:"metadata,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type ArchitectureDecision struct {
	ID        int64     `json:"id"`
	ProjectID int64     `json:"project_id"`
	Decision  string    `json:"decision"`
	Context   string    `json:"context"`
	Options   string    `json:"options,omitempty"`
	Outcome   string    `json:"outcome"`
	Status    string    `json:"status"` // "proposed", "accepted", "deprecated", "superseded"
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TriageReport is the Play Billing cutover triage for one project: the
// cutover decision, the cutover date, and the dollar exposure. Exposure is
// integer cents — floats never touch money, so what the form sends is what
// comes back, exactly.
type TriageReport struct {
	ID            int64     `json:"id"`
	ProjectID     int64     `json:"project_id"`
	Decision      string    `json:"decision"`
	CutoverDate   string    `json:"cutover_date"`
	ExposureCents int64     `json:"exposure_cents"`
	Notes         string    `json:"notes"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type Store struct {
	db *sql.DB
}

func New(dbPath string) (*Store, error) {
	// WAL + a busy timeout let multiple readers run alongside a single writer.
	// Without _busy_timeout, concurrent builds fail immediately with SQLITE_BUSY.
	dsn := dbPath + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}

	// SQLite serialises writes internally; a small pool lets reads proceed in
	// parallel instead of queueing behind whatever build is currently writing.
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(8)
	db.SetConnMaxLifetime(0)

	if err := db.Ping(); err != nil {
		return nil, err
	}

	// Restrict database file permissions to owner-only (0600)
	if err := os.Chmod(dbPath, 0600); err != nil {
		// Non-fatal: log but don't fail
		log.Printf("WARNING: could not restrict database permissions: %v", err)
	}

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		return nil, err
	}

	return s, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

// Ping checks that the database is reachable, for the readiness endpoint.
func (s *Store) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

func (s *Store) migrate() error {
	queries := []string{
		// FM-IDENTa: no column-level UNIQUE on email — uniqueness over real
		// addresses is the partial index migrateUsersEmailUnique creates, so
		// any number of wallet users may share the empty string.
		`CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			email TEXT NOT NULL,
			password_hash TEXT NOT NULL,
			tier TEXT DEFAULT 'free',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS projects (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL,
			name TEXT NOT NULL,
			plan_json TEXT DEFAULT '',
			status TEXT DEFAULT 'active',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (user_id) REFERENCES users(id)
		)`,
		`CREATE TABLE IF NOT EXISTS conversations (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			project_id INTEGER NOT NULL,
			title TEXT DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			last_active_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (project_id) REFERENCES projects(id)
		)`,
		`CREATE TABLE IF NOT EXISTS prompts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			project_id INTEGER NOT NULL,
			mode TEXT NOT NULL,
			prompt TEXT NOT NULL,
			response TEXT DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (project_id) REFERENCES projects(id)
		)`,
		`CREATE TABLE IF NOT EXISTS sessions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			project_id INTEGER NOT NULL,
			state TEXT DEFAULT 'starting',
			started_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			ended_at DATETIME,
			FOREIGN KEY (project_id) REFERENCES projects(id)
		)`,
		`CREATE TABLE IF NOT EXISTS usage (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL,
			prompts_used INTEGER DEFAULT 0,
			device_minutes REAL DEFAULT 0,
			period_start DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (user_id) REFERENCES users(id)
		)`,
		`CREATE TABLE IF NOT EXISTS project_memory (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			project_id INTEGER NOT NULL,
			user_id INTEGER,
			entry_type TEXT NOT NULL,
			content TEXT NOT NULL,
			metadata TEXT DEFAULT '',
			embedding BLOB,
			content_encrypted TEXT DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (project_id) REFERENCES projects(id),
			FOREIGN KEY (user_id) REFERENCES users(id)
		)`,
		`CREATE TABLE IF NOT EXISTS architecture_decisions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			project_id INTEGER NOT NULL,
			decision TEXT NOT NULL,
			context TEXT NOT NULL,
			options TEXT DEFAULT '',
			outcome TEXT NOT NULL,
			status TEXT DEFAULT 'accepted',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (project_id) REFERENCES projects(id)
		)`,
		// conversation_messages is the agent's actual memory: the verbatim turn
		// sequence, including tool_use / tool_result blocks, replayed to the model
		// on every request so follow-up prompts ("now make it blue") resolve.
		`CREATE TABLE IF NOT EXISTS conversation_messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			project_id INTEGER NOT NULL,
			user_id INTEGER,
			prompt_id INTEGER DEFAULT 0,
			seq INTEGER NOT NULL,
			role TEXT NOT NULL,
			blocks TEXT NOT NULL,
			input_tokens INTEGER DEFAULT 0,
			output_tokens INTEGER DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (project_id) REFERENCES projects(id),
			FOREIGN KEY (user_id) REFERENCES users(id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_conv_project_seq ON conversation_messages(project_id, seq)`,
		// session_events is the H1 harness session log: the append-only,
		// event-sourced record behind every agent turn. Messages are derived
		// from it, never stored; compaction shadows rows for derivation
		// without deleting them, so replay reproduces the exact conversation.
		`CREATE TABLE IF NOT EXISTS session_events (
			seq INTEGER PRIMARY KEY AUTOINCREMENT,
			log_key TEXT NOT NULL,
			kind TEXT NOT NULL,
			payload TEXT NOT NULL,
			created_at DATETIME NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_session_events_key ON session_events(log_key, seq)`,
		// checkpoints hold the pre-promotion content of every file a turn touches,
		// so a build can be rewound after the fact.
		`CREATE TABLE IF NOT EXISTS checkpoints (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			project_id INTEGER NOT NULL,
			prompt_id INTEGER DEFAULT 0,
			label TEXT DEFAULT '',
			files TEXT NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (project_id) REFERENCES projects(id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_checkpoints_project ON checkpoints(project_id, id DESC)`,
		// turn_approvals records user approvals granted mid-turn (e.g. answering
		// "yes, build the APK" to an ask_user question). Answers arrive as
		// tool_result turns, never as user text, so text-scanning guardrails
		// cannot see them — approvals must be stored explicitly, keyed to the
		// prompt lifecycle: a new prompt re-evaluates from its own words.
		`CREATE TABLE IF NOT EXISTS turn_approvals (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			project_id INTEGER NOT NULL,
			prompt_id INTEGER NOT NULL,
			scope TEXT NOT NULL,
			granted_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(prompt_id, scope),
			FOREIGN KEY (project_id) REFERENCES projects(id)
		)`,
		// build_receipts records every build_apk completion (built or failed),
		// written by the executor the moment the tool returns — not by the
		// turn, which may already be gone (cancelled, restarted, timed out).
		// The next turn picks unacknowledged receipts up into its context
		// and acks them, so a finished build is never silently lost and the
		// model always reads the verdict instead of rebuilding blind. No
		// polling: the receipt waits; nothing spins.
		`CREATE TABLE IF NOT EXISTS build_receipts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			project_id INTEGER NOT NULL,
			prompt_id INTEGER NOT NULL,
			status TEXT NOT NULL,
			apk_path TEXT NOT NULL DEFAULT '',
			summary TEXT NOT NULL DEFAULT '',
			acknowledged INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (project_id) REFERENCES projects(id)
		)`,
		// plan_task_status is the durable project backlog. One canonical row
		// per (project, task) so every turn — and every restart — sees the
		// same truth: a server restart used to blank the checklist and make
		// resume redo work. completed_by_prompt keeps turn attribution.
		// Superseded rows stay (history is cheap; deletion loses the audit
		// trail of what was once claimed done).
		`CREATE TABLE IF NOT EXISTS plan_task_status (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			project_id INTEGER NOT NULL,
			task_id TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'pending',
			result TEXT NOT NULL DEFAULT '',
			completed_by_prompt INTEGER NOT NULL DEFAULT 0,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(project_id, task_id),
			FOREIGN KEY (project_id) REFERENCES projects(id)
		)`,
		// message_feedback is the human-feedback sidecar. Deliberately NOT in
		// the session log: feedback is about the model's output, and writing it
		// into the log would put a human judgement of a message into the context
		// the model replays — the model would read its own reviews and start
		// performing for them. The sidecar keeps the signal for tuning while the
		// conversation stays clean. One row per (project, message): a re-vote
		// updates in place, so the signal reflects the person's settled view.
		`CREATE TABLE IF NOT EXISTS message_feedback (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			project_id INTEGER NOT NULL,
			message_id TEXT NOT NULL,
			rating INTEGER NOT NULL,
			comment TEXT NOT NULL DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(project_id, message_id),
			FOREIGN KEY (project_id) REFERENCES projects(id)
		)`,
		// customers is the link between a user and the payment provider's own
		// customer record. user_id is UNIQUE so there is exactly one canonical
		// customer per user (Phase 9 rule 2.2) — a re-visit reuses the row rather
		// than minting a duplicate provider customer.
		`CREATE TABLE IF NOT EXISTS customers (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL UNIQUE,
			provider TEXT NOT NULL,
			provider_customer_id TEXT NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (user_id) REFERENCES users(id)
		)`,
		// subscriptions is the single source of truth for a user's paid state
		// (rule 2.6): tier, renewal date, last-4, and whether a cancel is pending.
		// One row per user, upserted from verified provider webhooks (rule 2.3) —
		// never from a client claim.
		`CREATE TABLE IF NOT EXISTS subscriptions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL UNIQUE,
			provider_subscription_id TEXT DEFAULT '',
			status TEXT DEFAULT 'inactive',
			tier TEXT DEFAULT 'free',
			current_period_end DATETIME,
			cancel_at_period_end INTEGER DEFAULT 0,
			payment_last4 TEXT DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (user_id) REFERENCES users(id)
		)`,
		// billing_events is the audit log (rule 2.10). provider_event_id is UNIQUE,
		// which is also the idempotency key (rule 2.4): a duplicate webhook delivery
		// fails the INSERT, and the handler reads RowsAffected to skip re-processing.
		`CREATE TABLE IF NOT EXISTS billing_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER DEFAULT 0,
			provider_event_id TEXT NOT NULL UNIQUE,
			type TEXT NOT NULL,
			payload TEXT DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_billing_events_user ON billing_events(user_id, id DESC)`,
		// app_settings owns the few facts the server generates for itself and
		// must keep across restarts — today only the devnet-SOL export treasury.
		// A treasury that vanished on reboot would strand every payment already
		// sent to it, so it sits beside the ledger it buys from.
		`CREATE TABLE IF NOT EXISTS app_settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
		// user_git_credentials holds one connected git provider account per user.
		// access_token is stored ENCRYPTED (see internal/secrets) and never in the
		// clear — the struct tags it json:"-" so it cannot leak through an API
		// response. One row per (user, provider); reconnecting upserts in place.
		`CREATE TABLE IF NOT EXISTS user_git_credentials (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL,
			provider TEXT NOT NULL DEFAULT 'github',
			provider_login TEXT DEFAULT '',
			provider_user_id INTEGER DEFAULT 0,
			access_token TEXT NOT NULL,
			scopes TEXT DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(user_id, provider),
			FOREIGN KEY (user_id) REFERENCES users(id)
		)`,
		// project_git_remotes maps a project to the repository it pushes to. One
		// row per project (project_id UNIQUE), so a project targets exactly one
		// remote and re-pushing reuses it.
		`CREATE TABLE IF NOT EXISTS project_git_remotes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			project_id INTEGER NOT NULL UNIQUE,
			provider TEXT NOT NULL DEFAULT 'github',
			owner TEXT NOT NULL,
			repo TEXT NOT NULL,
			clone_url TEXT DEFAULT '',
			html_url TEXT DEFAULT '',
			default_branch TEXT DEFAULT 'main',
			private INTEGER DEFAULT 1,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (project_id) REFERENCES projects(id)
		)`,
		// project_keystores holds one signing keystore per project for release
		// export. The keystore FILE lives on disk (see release.KeystoreDir);
		// only its path, alias, fingerprint, and the ENCRYPTED password live
		// here. project_id UNIQUE so a project always re-signs with the same
		// key — app stores require it.
		`CREATE TABLE IF NOT EXISTS project_keystores (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			project_id INTEGER NOT NULL UNIQUE,
			path TEXT NOT NULL,
			alias TEXT NOT NULL,
			fingerprint TEXT NOT NULL,
			enc_password TEXT NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (project_id) REFERENCES projects(id)
		)`,
		// export_jobs tracks release export runs: async because a release
		// gradle build takes tens of minutes and must never block the dev loop.
		`CREATE TABLE IF NOT EXISTS export_jobs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			project_id INTEGER NOT NULL,
			status TEXT NOT NULL DEFAULT 'queued',
			artifact_path TEXT DEFAULT '',
			manifest_path TEXT DEFAULT '',
			error TEXT DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (project_id) REFERENCES projects(id)
		)`,
		// export_credits is the SKR ledger: one row per grant, `remaining`
		// decremented as exports are spent. The balance IS the ledger —
		// SpareCredits is SUM(remaining), so there is no second number to
		// drift the way quota.go's three disagreeing limit copies did.
		// tx_signature is UNIQUE, the same idempotency law
		// billing_events.provider_event_id enforces: a replayed payment
		// cannot grant twice, whatever the client claims.
		`CREATE TABLE IF NOT EXISTS export_credits (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL,
			credits INTEGER NOT NULL,
			remaining INTEGER NOT NULL,
			source TEXT NOT NULL,
			tx_signature TEXT NOT NULL UNIQUE,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (user_id) REFERENCES users(id)
		)`,
		// project_verify_state is W6's durable shadow of the last finished
		// verification sweep: export/release/APK-ship consult it before
		// shipping. One row per project; the newest sweep wins.
		`CREATE TABLE IF NOT EXISTS project_verify_state (
			project_id INTEGER PRIMARY KEY,
			status TEXT NOT NULL,
			policy TEXT NOT NULL DEFAULT '',
			skipped TEXT NOT NULL DEFAULT '[]',
			remaining TEXT NOT NULL DEFAULT '[]',
			reason TEXT NOT NULL DEFAULT '',
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (project_id) REFERENCES projects(id)
		)`,
		// project_assets holds validated store assets. kind is one of
		// screenshot, icon, banner — validated at upload time, never conflated:
		// an emulator screenshot cannot become an icon by accident.
		`CREATE TABLE IF NOT EXISTS project_assets (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			project_id INTEGER NOT NULL,
			kind TEXT NOT NULL,
			path TEXT NOT NULL,
			width INTEGER NOT NULL,
			height INTEGER NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (project_id) REFERENCES projects(id)
		)`,
		// prompt_attachments holds images and markdown docs attached to a
		// turn. kind is image | doc. prompt_id starts NULL (uploaded but not
		// yet sent) and is claimed by exactly one turn — see ClaimAttachments.
		`CREATE TABLE IF NOT EXISTS prompt_attachments (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			project_id INTEGER NOT NULL,
			prompt_id INTEGER NULL,
			kind TEXT NOT NULL,
			name TEXT NOT NULL,
			mime TEXT NOT NULL,
			size INTEGER NOT NULL,
			path TEXT NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (project_id) REFERENCES projects(id)
		)`,
		// refresh_tokens holds opaque session-continuation credentials. Only
		// the hash is stored — a database read never yields a usable token.
		// revoked_at marks spent (rotated) or killed tokens; replaced_by
		// links a rotation for reuse detection.
		`CREATE TABLE IF NOT EXISTS refresh_tokens (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL,
			token_hash TEXT NOT NULL UNIQUE,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			expires_at DATETIME NOT NULL,
			revoked_at DATETIME NULL,
			replaced_by INTEGER NULL,
			last_used_at DATETIME NULL,
			device_label TEXT NOT NULL DEFAULT '',
			FOREIGN KEY (user_id) REFERENCES users(id)
		)`,
		// triage_reports holds the Play Billing cutover triage: the decision,
		// the cutover date, and the dollar exposure in integer cents (exact —
		// floats never touch money). One row per project, upserted.
		`CREATE TABLE IF NOT EXISTS triage_reports (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			project_id INTEGER NOT NULL UNIQUE,
			decision TEXT NOT NULL DEFAULT '',
			cutover_date TEXT NOT NULL DEFAULT '',
			exposure_cents INTEGER NOT NULL DEFAULT 0,
			notes TEXT NOT NULL DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (project_id) REFERENCES projects(id)
		)`,
	}

	for _, q := range queries {
		if _, err := s.db.Exec(q); err != nil {
			return err
		}
	}

	// DV4: the idle reaper's clock. Fresh sessions default to their start
	// time — a session never touched starts idle. Tolerated if present.
	s.db.Exec("ALTER TABLE sessions ADD COLUMN last_active_at DATETIME DEFAULT CURRENT_TIMESTAMP")

	// Migration: add template_version column if missing
	s.db.Exec("ALTER TABLE projects ADD COLUMN template_version TEXT DEFAULT ''")

	// Migration: add identity columns if missing
	s.db.Exec("ALTER TABLE projects ADD COLUMN slug TEXT DEFAULT ''")
	s.db.Exec("ALTER TABLE projects ADD COLUMN android_package TEXT DEFAULT ''")
	s.db.Exec("ALTER TABLE projects ADD COLUMN ios_bundle_id TEXT DEFAULT ''")
	s.db.Exec("ALTER TABLE projects ADD COLUMN scheme TEXT DEFAULT ''")

	// Migration: session visibility columns on refresh tokens. Fresh tables
	// already carry them (see schema above); existing ones grow them here.
	s.db.Exec("ALTER TABLE refresh_tokens ADD COLUMN last_used_at DATETIME NULL")
	s.db.Exec("ALTER TABLE refresh_tokens ADD COLUMN device_label TEXT NOT NULL DEFAULT ''")

	// Add uniqueness constraints (ignore errors if already exist)
	s.db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_projects_slug ON projects(slug) WHERE slug != ''")
	s.db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_projects_android_package ON projects(android_package) WHERE android_package != ''")
	s.db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_projects_ios_bundle_id ON projects(ios_bundle_id) WHERE ios_bundle_id != ''")

	// Migration: add container tracking columns to sessions
	s.db.Exec("ALTER TABLE sessions ADD COLUMN container_id TEXT DEFAULT ''")
	s.db.Exec("ALTER TABLE sessions ADD COLUMN container_name TEXT DEFAULT ''")
	s.db.Exec("ALTER TABLE sessions ADD COLUMN adb_port INTEGER DEFAULT 0")
	s.db.Exec("ALTER TABLE sessions ADD COLUMN container_state TEXT DEFAULT ''")

	// Migration: multiple chats per project. 0 means "not yet assigned" —
	// pre-migration rows and any caller that skips the field both read as
	// the project's whole flat history, exactly today's behavior.
	s.db.Exec("ALTER TABLE prompts ADD COLUMN conversation_id INTEGER DEFAULT 0")

	// Migration: per-prompt token accounting
	s.db.Exec("ALTER TABLE prompts ADD COLUMN input_tokens INTEGER DEFAULT 0")
	s.db.Exec("ALTER TABLE prompts ADD COLUMN output_tokens INTEGER DEFAULT 0")
	s.db.Exec("ALTER TABLE prompts ADD COLUMN cache_read_tokens INTEGER DEFAULT 0")

	// Migration: routing + cost accounting. cost_usd lands on the same rows
	// the token counters already update — one ledger, not two — so billing
	// reads tokens and dollars from the same source of truth.
	s.db.Exec("ALTER TABLE prompts ADD COLUMN cost_usd REAL DEFAULT 0")
	s.db.Exec("ALTER TABLE prompts ADD COLUMN routed_model TEXT DEFAULT ''")
	s.db.Exec("ALTER TABLE prompts ADD COLUMN skipped_gates TEXT DEFAULT '[]'")
	// H13: the session title — the human-facing line history lists render.
	// LLM-titled from the first prompt when a client answers, the honest
	// truncated prompt otherwise; never blank.
	s.db.Exec("ALTER TABLE prompts ADD COLUMN title TEXT DEFAULT ''")
	s.db.Exec("ALTER TABLE prompts ADD COLUMN route_reason TEXT DEFAULT ''")
	// The turn's durable cross-leg touched-set (JSON string array): a resumed
	// leg's fresh journal cannot see earlier legs' files, so the row carries
	// the union the verify gate and fail-closed export read. Existing rows
	// default to empty — they finished (or were discarded) before this law
	// had a set to enforce.
	s.db.Exec("ALTER TABLE prompts ADD COLUMN files_touched TEXT DEFAULT '[]'")
	s.db.Exec("ALTER TABLE usage ADD COLUMN cost_usd REAL DEFAULT 0")

	// Migration: run-status tracking, so an interrupted turn is distinguishable
	// from a finished one and can be resumed. Existing rows backfill to
	// 'completed' — they ran to their end before this column existed, and a
	// historical prompt must never surface as resumable. New rows are set to
	// 'running' at CreatePrompt and moved to a terminal status when the turn ends.
	s.db.Exec("ALTER TABLE prompts ADD COLUMN status TEXT DEFAULT 'completed'")
	s.db.Exec("ALTER TABLE prompts ADD COLUMN round INTEGER DEFAULT 0")
	s.db.Exec("ALTER TABLE prompts ADD COLUMN updated_at DATETIME")

	// Migration: aggregate token counters on usage
	s.db.Exec("ALTER TABLE usage ADD COLUMN input_tokens INTEGER DEFAULT 0")
	s.db.Exec("ALTER TABLE usage ADD COLUMN output_tokens INTEGER DEFAULT 0")

	// Migration: profile fields on users. display_name is the editable name shown
	// on the settings page; it defaults to '' so the auth-path SELECTs never scan a
	// NULL. Billing state (tier, renewal, last-4) lives in subscriptions, not here.
	s.db.Exec("ALTER TABLE users ADD COLUMN display_name TEXT DEFAULT ''")

	// Migration: OAuth identity (PLAN_CORE_PRODUCT.md D). Sign-in is GitHub +
	// Google; email/password is gone. password_hash stays as a column — legacy
	// rows keep their value, new rows write '' — because SQLite cannot drop a
	// column's NOT NULL without a table rebuild, and the value is dead either
	// way: no code path reads it anymore. The users_oauth_identity table is the
	// new identity: (provider, provider_id) is unique, and rows link to users.
	s.db.Exec("ALTER TABLE users ADD COLUMN provider TEXT DEFAULT ''")
	s.db.Exec("ALTER TABLE users ADD COLUMN avatar_url TEXT DEFAULT ''")
	s.db.Exec(`CREATE TABLE IF NOT EXISTS users_oauth_identity (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id INTEGER NOT NULL REFERENCES users(id),
		provider TEXT NOT NULL,
		provider_id TEXT NOT NULL,
		email TEXT DEFAULT '',
		name TEXT DEFAULT '',
		avatar_url TEXT DEFAULT '',
		UNIQUE (provider, provider_id)
	)`)

	// P17: wallet auth challenges. One row per issued challenge, single-use
	// by construction — ConsumeWalletChallenge deletes on read — and swept
	// by TTL so an abandoned QR can't pile up. The nonce is the address of
	// the challenge; the wallet signs its exact bytes.
	s.db.Exec(`CREATE TABLE IF NOT EXISTS wallet_challenges (
		nonce TEXT PRIMARY KEY,
		message TEXT NOT NULL,
		created_at DATETIME NOT NULL DEFAULT (datetime('now'))
	)`)

	// E: push notifications. onesignal_id is the user's registered device
	// (OneSignal external id) — the push target. The notifications table is
	// the in-app history: every Dispatch records here regardless of whether
	// OneSignal is configured, so the surface is honest in both modes.
	s.db.Exec("ALTER TABLE users ADD COLUMN onesignal_id TEXT DEFAULT ''")
	s.db.Exec(`CREATE TABLE IF NOT EXISTS notifications (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id INTEGER NOT NULL REFERENCES users(id),
		kind TEXT NOT NULL,
		title TEXT DEFAULT '',
		body TEXT DEFAULT '',
		sent_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		read_at DATETIME
	)`)

	// P0b: per-tenant user_id on hot tables — defense-in-depth RLS. Every
	// project_memory / conversation row is now tagged with its owner's user_id
	// so a future JOIN can enforce tenant isolation at the DB, not only at the
	// handler. Backfill from projects; new writes fill via SELECT subquery.
	s.db.Exec("ALTER TABLE project_memory ADD COLUMN user_id INTEGER")
	s.db.Exec("ALTER TABLE conversation_messages ADD COLUMN user_id INTEGER")
	s.db.Exec("UPDATE project_memory SET user_id = (SELECT user_id FROM projects WHERE projects.id = project_memory.project_id) WHERE user_id IS NULL")
	s.db.Exec("UPDATE conversation_messages SET user_id = (SELECT user_id FROM projects WHERE projects.id = conversation_messages.project_id) WHERE user_id IS NULL")
	s.db.Exec("CREATE INDEX IF NOT EXISTS idx_project_memory_user ON project_memory(user_id)")
	s.db.Exec("CREATE INDEX IF NOT EXISTS idx_conv_user ON conversation_messages(user_id)")

	// P1b: FTS5 for semantic recall — substring → tokenised search. Content sync
	// via triggers so project_memory is source of truth; FTS is just the index.
	s.db.Exec(`CREATE VIRTUAL TABLE IF NOT EXISTS project_memory_fts USING fts5(content, entry_type, metadata, content='project_memory', content_rowid='id', tokenize='porter unicode61')`)
	s.db.Exec(`CREATE TRIGGER IF NOT EXISTS project_memory_fts_ai AFTER INSERT ON project_memory BEGIN INSERT INTO project_memory_fts(rowid, content, entry_type, metadata) VALUES (new.id, new.content, new.entry_type, new.metadata); END`)
	s.db.Exec(`CREATE TRIGGER IF NOT EXISTS project_memory_fts_ad AFTER DELETE ON project_memory BEGIN INSERT INTO project_memory_fts(project_memory_fts, rowid, content, entry_type, metadata) VALUES('delete', old.id, old.content, old.entry_type, old.metadata); END`)
	s.db.Exec(`CREATE TRIGGER IF NOT EXISTS project_memory_fts_au AFTER UPDATE ON project_memory BEGIN INSERT INTO project_memory_fts(project_memory_fts, rowid, content, entry_type, metadata) VALUES('delete', old.id, old.content, old.entry_type, old.metadata); INSERT INTO project_memory_fts(rowid, content, entry_type, metadata) VALUES (new.id, new.content, new.entry_type, new.metadata); END`)
	// Backfill any rows that predate the FTS table (INSERT trigger missed them).
	s.db.Exec(`INSERT INTO project_memory_fts(project_memory_fts) VALUES('rebuild')`)

	// P1c: vector placeholder — embedding BLOB for future sqlite-vss. Empty until
	// an embedding provider is wired; schema is ready so P1b FTS is not blocked.
	s.db.Exec("ALTER TABLE project_memory ADD COLUMN embedding BLOB")

	// P2b: encryption at rest — content_encrypted holds AES-GCM sealed copy when
	// MEMORY_ENCRYPTION_KEY (or JWT_SECRET) is set; plaintext stays for FTS until
	// sqlcipher replaces file-level encryption. Schema ready, no behavior change
	// until key is configured.
	s.db.Exec("ALTER TABLE project_memory ADD COLUMN content_encrypted TEXT DEFAULT ''")

	// P2a retired (W1.1): conversation_archive was write-only — TrimConversation
	// archived then deleted, and nothing ever read it back. The delete path is
	// gone; legacy rows are recovered into conversation_messages after the
	// audit block below (the stale-clear purge reads audit_log, so recovery
	// must follow its CREATE), and the table is dropped there.

	// W1.3: conversation search → FTS5, mirroring project_memory_fts above.
	// External content: conversation_messages.blocks is the source text (the
	// same bytes the LIKE leg matches, so both legs stay in parity), FTS is
	// only the index — triggers keep it in lockstep with append/clear
	// and rebuild backfills rows that predate it.
	s.db.Exec(`CREATE VIRTUAL TABLE IF NOT EXISTS conversation_fts USING fts5(blocks, content='conversation_messages', content_rowid='id', tokenize='porter unicode61')`)
	s.db.Exec(`CREATE TRIGGER IF NOT EXISTS conversation_fts_ai AFTER INSERT ON conversation_messages BEGIN INSERT INTO conversation_fts(rowid, blocks) VALUES (new.id, new.blocks); END`)
	s.db.Exec(`CREATE TRIGGER IF NOT EXISTS conversation_fts_ad AFTER DELETE ON conversation_messages BEGIN INSERT INTO conversation_fts(conversation_fts, rowid, blocks) VALUES('delete', old.id, old.blocks); END`)
	s.db.Exec(`CREATE TRIGGER IF NOT EXISTS conversation_fts_au AFTER UPDATE ON conversation_messages BEGIN INSERT INTO conversation_fts(conversation_fts, rowid, blocks) VALUES('delete', old.id, old.blocks); INSERT INTO conversation_fts(rowid, blocks) VALUES (new.id, new.blocks); END`)
	s.db.Exec(`INSERT INTO conversation_fts(conversation_fts) VALUES('rebuild')`)

	// W4: codebase index (TALONSTUDIO_INDEX, shipped dark). code_files holds
	// the per-file content hash — the Merkle-ish state that lets a reconcile
	// touch only changed paths instead of re-reading the tree. chunks/symbols/
	// edges are the lexical and graph layers; code_chunks_fts mirrors chunks
	// with the same external-content + trigger pattern as project_memory_fts —
	// bm25 ranking comes free with FTS5, which is the whole lexical layer of
	// W4.1; W4.3 vectors ride the same rows behind the provider interface,
	// keyed by chunk hash so an unchanged chunk never re-embeds. Rows are
	// keyed by project_id; FTS queries join through the base table for the
	// tenant filter because the mirror carries no project column of its own.
	s.db.Exec(`CREATE TABLE IF NOT EXISTS code_files (
		project_id INTEGER NOT NULL,
		path TEXT NOT NULL,
		hash TEXT NOT NULL,
		exports TEXT NOT NULL DEFAULT '[]',
		imports TEXT NOT NULL DEFAULT '[]',
		PRIMARY KEY (project_id, path)
	)`)
	// W4.6: the map view reads per-file exports/imports from this table.
	// Databases built before it can't backfill parsed data in SQL, so the
	// first boot that successfully ADD COLUMNs invalidates hashes — the next
	// reconcile repopulates every row. The duplicate-column error on fresh
	// and already-upgraded boots is the same benign no-op every guarded
	// ALTER relies on, and it must NOT invalidate: that would re-parse the
	// whole workspace on every restart.
	mapCols := false
	if _, err := s.db.Exec("ALTER TABLE code_files ADD COLUMN exports TEXT NOT NULL DEFAULT '[]'"); err == nil {
		mapCols = true
	}
	if _, err := s.db.Exec("ALTER TABLE code_files ADD COLUMN imports TEXT NOT NULL DEFAULT '[]'"); err == nil {
		mapCols = true
	}
	if mapCols {
		s.db.Exec(`UPDATE code_files SET hash = ''`)
	}
	s.db.Exec(`CREATE TABLE IF NOT EXISTS code_chunks (
		id INTEGER PRIMARY KEY,
		project_id INTEGER NOT NULL,
		path TEXT NOT NULL,
		hash TEXT NOT NULL,
		name TEXT NOT NULL DEFAULT '',
		start_line INTEGER NOT NULL DEFAULT 1,
		body TEXT NOT NULL,
		embedding BLOB
	)`)
	// W4.3: databases built before the vector layer lack the column; the
	// duplicate-column error on fresh databases is the same benign no-op
	// every guarded ALTER above relies on.
	s.db.Exec("ALTER TABLE code_chunks ADD COLUMN embedding BLOB")
	s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_code_chunks_file ON code_chunks(project_id, path)`)
	s.db.Exec(`CREATE VIRTUAL TABLE IF NOT EXISTS code_chunks_fts USING fts5(body, name, path, content='code_chunks', content_rowid='id', tokenize='porter unicode61')`)
	s.db.Exec(`CREATE TRIGGER IF NOT EXISTS code_chunks_fts_ai AFTER INSERT ON code_chunks BEGIN INSERT INTO code_chunks_fts(rowid, body, name, path) VALUES (new.id, new.body, new.name, new.path); END`)
	s.db.Exec(`CREATE TRIGGER IF NOT EXISTS code_chunks_fts_ad AFTER DELETE ON code_chunks BEGIN INSERT INTO code_chunks_fts(code_chunks_fts, rowid, body, name, path) VALUES('delete', old.id, old.body, old.name, old.path); END`)
	s.db.Exec(`CREATE TRIGGER IF NOT EXISTS code_chunks_fts_au AFTER UPDATE ON code_chunks BEGIN INSERT INTO code_chunks_fts(code_chunks_fts, rowid, body, name, path) VALUES('delete', old.id, old.body, old.name, old.path); INSERT INTO code_chunks_fts(rowid, body, name, path) VALUES (new.id, new.body, new.name, new.path); END`)
	s.db.Exec(`INSERT INTO code_chunks_fts(code_chunks_fts) VALUES('rebuild')`)
	s.db.Exec(`CREATE TABLE IF NOT EXISTS code_symbols (
		project_id INTEGER NOT NULL,
		path TEXT NOT NULL,
		name TEXT NOT NULL,
		kind TEXT NOT NULL,
		line INTEGER NOT NULL
	)`)
	s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_code_symbols_name ON code_symbols(project_id, name)`)
	s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_code_symbols_path ON code_symbols(project_id, path)`)
	s.db.Exec(`CREATE TABLE IF NOT EXISTS code_edges (
		project_id INTEGER NOT NULL,
		from_path TEXT NOT NULL,
		from_name TEXT NOT NULL,
		kind TEXT NOT NULL,
		to_path TEXT NOT NULL DEFAULT '',
		to_name TEXT NOT NULL
	)`)
	s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_code_edges_to ON code_edges(project_id, kind, to_name)`)
	s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_code_edges_from ON code_edges(project_id, from_path, from_name)`)

	// P3a: audit log — append-only, who/when/what for every memory/history mutation.
	// Never DELETE. Hash chain is implicit via id ordering; before/after can be
	// added when needed for tamper audit.
	s.db.Exec(`CREATE TABLE IF NOT EXISTS audit_log (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id INTEGER,
		project_id INTEGER,
		action TEXT NOT NULL,
		target_id TEXT DEFAULT '',
		detail TEXT DEFAULT '',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`)
	s.db.Exec("CREATE INDEX IF NOT EXISTS idx_audit_user ON audit_log(user_id)")
	s.db.Exec("CREATE INDEX IF NOT EXISTS idx_audit_project ON audit_log(project_id)")

	// W1.1: recover rows a legacy build archived (the write-only table P2a
	// created; its writer is gone). Runs after audit_log because the purge
	// below reads clear_conversation, and after the FTS block because the
	// conversation_fts_ai trigger indexes each recovered row as it lands.
	// Order inside: purge stale rows first — archived before a later
	// clear_conversation must not resurrect a wipe the user asked for — then
	// copy survivors back. Guards make it idempotent under crash-retry: ids
	// never collide (AUTOINCREMENT ids are never reused, OR IGNORE anyway) and
	// (project_id, seq) skips anything already live (post-clear generations
	// restart seq at 1, so overlap is real). Drop only after a clean copy;
	// any failure keeps the table so the next boot tries again.
	var archiveTable int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='conversation_archive'`,
	).Scan(&archiveTable); err == nil && archiveTable > 0 {
		if _, err := s.db.Exec(`DELETE FROM conversation_archive WHERE EXISTS (
			SELECT 1 FROM audit_log
			WHERE audit_log.project_id = conversation_archive.project_id
				AND audit_log.action = 'clear_conversation'
				AND audit_log.created_at >= conversation_archive.archived_at
		)`); err != nil {
			log.Printf("store: archive purge failed (will retry next boot): %v", err)
		} else if _, err := s.db.Exec(`INSERT OR IGNORE INTO conversation_messages
			(id, project_id, user_id, prompt_id, seq, role, blocks, input_tokens, output_tokens, created_at)
			SELECT id, project_id, user_id, prompt_id, seq, role, blocks, input_tokens, output_tokens, created_at
			FROM conversation_archive a
			WHERE NOT EXISTS (
				SELECT 1 FROM conversation_messages m
				WHERE m.project_id = a.project_id AND m.seq = a.seq
			)`); err != nil {
			log.Printf("store: archive recovery failed (will retry next boot): %v", err)
		} else if _, err := s.db.Exec(`DROP TABLE conversation_archive`); err != nil {
			log.Printf("store: archive drop failed (will retry next boot): %v", err)
		}
	}

	// FM-IDENTa, schema half: enforce uniqueness over real addresses only.
	// Last in the migration so every users column added above already exists
	// for the copy; a legacy file (email TEXT UNIQUE NOT NULL) is rebuilt
	// once, a fresh file just gets the index.
	if err := s.migrateUsersEmailUnique(); err != nil {
		return err
	}

	return nil
}

// migrateUsersEmailUnique enforces the schema half of FM-IDENTa: uniqueness
// over REAL addresses only, so the empty string any number of wallet users
// share is not a key. Fresh files are born without a column-level UNIQUE (see
// the CREATE TABLE above) and get their partial index here — unconditionally,
// because a fresh file never matches the legacy DDL below and this is the
// only place its index comes from: a silently skipped CREATE would ship a
// users table with no email uniqueness at all (FM-IDENTa(iv)). Files created
// before FM-IDENTa carry email TEXT UNIQUE NOT NULL, which SQLite cannot drop
// in place, so the table is rebuilt once — the rebuild's own output does not
// contain the legacy text, so detection cannot fire twice.
func (s *Store) migrateUsersEmailUnique() error {
	var ddl string
	if err := s.db.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name='users'`).Scan(&ddl); err != nil {
		return fmt.Errorf("users table: %w", err)
	}
	if strings.Contains(ddl, "email TEXT UNIQUE NOT NULL") {
		if err := s.rebuildUsersEmail(); err != nil {
			return fmt.Errorf("users.email rebuild: %w", err)
		}
	}
	if _, err := s.db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_users_email ON users(email) WHERE email != ''`); err != nil {
		return fmt.Errorf("users.email partial index: %w", err)
	}
	return nil
}

// rebuildUsersEmail swaps the legacy users table for one without the
// column-level UNIQUE. The whole thing rides a single pinned connection: the
// DSN opens every pooled conn _pragma=foreign_keys(ON) and eleven tables
// reference users(id) — DROP TABLE users fails outright with children present
// (FM-IDENTa(iii)) — and PRAGMA foreign_keys is per-connection, so an OFF on
// one pool member says nothing about the conn that runs the next statement.
// Defers run LIFO: rollback the uncommitted transaction, restore enforcement,
// then hand the conn back to the pool.
func (s *Store) rebuildUsersEmail() error {
	ctx := context.Background()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	defer func() {
		// A no-op inside an open transaction, hence deferred past the rollback.
		_, _ = conn.ExecContext(ctx, "PRAGMA foreign_keys=ON")
	}()
	if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys=OFF"); err != nil {
		return err
	}

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			tx.Rollback()
		}
	}()

	// The official SQLite rebuild: copy carries the explicit ids, so
	// AUTOINCREMENT continues past the copied max, and the index is created
	// inside the transaction so a crash cannot leave the new table unindexed.
	for _, q := range []string{
		`CREATE TABLE users_new (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			email TEXT NOT NULL,
			password_hash TEXT NOT NULL,
			tier TEXT DEFAULT 'free',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			display_name TEXT DEFAULT '',
			provider TEXT DEFAULT '',
			avatar_url TEXT DEFAULT '',
			onesignal_id TEXT DEFAULT ''
		)`,
		`INSERT INTO users_new (id, email, password_hash, tier, created_at, display_name, provider, avatar_url, onesignal_id)
			SELECT id, email, password_hash, tier, created_at, display_name, provider, avatar_url, onesignal_id FROM users`,
		`DROP TABLE users`,
		`ALTER TABLE users_new RENAME TO users`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_users_email ON users(email) WHERE email != ''`,
	} {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("%s: %w", strings.Split(q, "\n")[0], err)
		}
	}

	// The copy must leave every FK resolvable. A dangling reference rolls the
	// whole swap back rather than shipping a schema the next query trips over.
	fk, err := tx.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return fmt.Errorf("foreign_key_check: %w", err)
	}
	bad := ""
	for fk.Next() {
		var table, parent sql.NullString
		var rowid, fkid sql.NullInt64
		if err := fk.Scan(&table, &rowid, &parent, &fkid); err != nil {
			fk.Close()
			return err
		}
		if bad == "" {
			bad = table.String
		}
	}
	fk.Close()
	if bad != "" {
		return fmt.Errorf("foreign_key_check found a dangling reference after the rebuild (first: %s)", bad)
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func (s *Store) CreateUser(email, passwordHash string) (*User, error) {
	res, err := s.db.Exec("INSERT INTO users (email, password_hash) VALUES (?, ?)", email, passwordHash)
	if err != nil {
		// Registration used to report every failure here as "email already exists",
		// which leaked whether an address was registered and hid real problems like
		// a read-only database behind a message that made no sense.
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return nil, ErrDuplicateEmail
		}
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetUserByID(id)
}

// UpsertOAuthUser is the sign-in primitive of the OAuth world: find the
// user bound to (provider, provider_id), or create one. The same provider
// account signing in twice is one user; profile drift (a new avatar, a
// changed name) refreshes the cached fields. A provider identity whose
// email collides with an existing user links to that user — the email was
// already verified by the provider, so it is the same human. Only a
// *presented* email can collide: an absent one ("" — wallet sign-ins carry
// none) links nothing and gets its own account, because the empty string is
// not an identity (FM-IDENTa).
func (s *Store) UpsertOAuthUser(provider, providerID, email, name, avatarURL string) (*User, error) {
	// 1) The bound path: this provider account already exists.
	if u, err := s.GetUserByProvider(provider, providerID); err == nil {
		if _, err := s.db.Exec(
			`UPDATE users_oauth_identity SET email = ?, name = ?, avatar_url = ? WHERE provider = ? AND provider_id = ?`,
			email, name, avatarURL, provider, providerID,
		); err == nil {
			if avatarURL != "" {
				s.db.Exec("UPDATE users SET avatar_url = ? WHERE id = ?", avatarURL, u.ID)
			}
			refreshed, err := s.GetUserByID(u.ID)
			if err != nil {
				return nil, err
			}
			return refreshed, nil
		}
		return nil, err
	} else if !errors.Is(err, ErrUserNotFound) {
		return nil, err
	}

	// 2) The email path: the same human, already known. Link the provider
	// identity to the existing account — but only when an address was
	// actually presented. GetUserByEmail("") would match the first
	// email-less row and graft this identity onto that account, so a wallet
	// sign-in must fall through to "create fresh" instead (FM-IDENTa(i);
	// the schema half is migrateUsersEmailUnique).
	if email != "" {
		if existing, err := s.GetUserByEmail(email); err == nil {
			if _, err := s.db.Exec(
				`INSERT INTO users_oauth_identity (user_id, provider, provider_id, email, name, avatar_url) VALUES (?, ?, ?, ?, ?, ?)`,
				existing.ID, provider, providerID, email, name, avatarURL,
			); err != nil {
				return nil, err
			}
			if _, err := s.db.Exec("UPDATE users SET provider = ?, avatar_url = ? WHERE id = ?", provider, avatarURL, existing.ID); err != nil {
				return nil, err
			}
			return s.GetUserByID(existing.ID)
		} else if !errors.Is(err, ErrUserNotFound) && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
	}

	// 3) A brand-new human: users row + identity row.
	res, err := s.db.Exec("INSERT INTO users (email, password_hash, provider, avatar_url) VALUES (?, '', ?, ?)", email, provider, avatarURL)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			// Raced between the email check and here — treat as the bound path.
			return s.UpsertOAuthUser(provider, providerID, email, name, avatarURL)
		}
		return nil, err
	}
	id, _ := res.LastInsertId()
	if name != "" {
		s.db.Exec("UPDATE users SET display_name = ? WHERE id = ?", name, id)
	}
	if _, err := s.db.Exec(
		`INSERT INTO users_oauth_identity (user_id, provider, provider_id, email, name, avatar_url) VALUES (?, ?, ?, ?, ?, ?)`,
		id, provider, providerID, email, name, avatarURL,
	); err != nil {
		return nil, err
	}
	return s.GetUserByID(id)
}

// GetUserByProvider resolves the users row bound to a provider identity.
func (s *Store) GetUserByProvider(provider, providerID string) (*User, error) {
	u := &User{}
	err := s.db.QueryRow(`
		SELECT u.id, u.email, u.password_hash, u.tier, u.display_name, u.created_at
		FROM users u JOIN users_oauth_identity i ON i.user_id = u.id
		WHERE i.provider = ? AND i.provider_id = ?`, provider, providerID).
		Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Tier, &u.DisplayName, &u.CreatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrUserNotFound
		}
		return nil, err
	}
	return u, nil
}

// ErrWalletOwnedElsewhere reports a wallet that already belongs to another
// account. The one-wallet-one-account law has two sides and one message:
// a wallet may serve exactly one account (UNIQUE(provider, provider_id) is
// the last line), and an account may hold exactly one wallet (replaced in
// place, never accumulated).
var ErrWalletOwnedElsewhere = errors.New("this wallet is already linked to another account")

// LinkWallet binds a solana identity to exactly one account: the caller's.
//
//   - a pubkey already bound elsewhere → ErrWalletOwnedElsewhere, named —
//     not a bare UNIQUE failure, and never a silent attach.
//   - an account that already holds a wallet → provider_id replaced in
//     place (the second connect swaps, it does not add).
//   - otherwise → inserted with the absent email a wallet carries
//     (FM-IDENTa: an empty email links nothing).
//
// The bind check reads first so the error can carry the fact; the UNIQUE
// constraint stays the arbiter of the race.
func (s *Store) LinkWallet(userID int64, wallet string) error {
	if bound, err := s.GetUserByProvider("solana", wallet); err == nil && bound.ID != userID {
		return ErrWalletOwnedElsewhere
	}
	var id int64
	err := s.db.QueryRow(`SELECT id FROM users_oauth_identity WHERE user_id = ? AND provider = 'solana' LIMIT 1`,
		userID).Scan(&id)
	switch {
	case err == nil:
		// Replace: this account's one wallet becomes the new one.
		_, err = s.db.Exec(`UPDATE users_oauth_identity SET provider_id = ? WHERE id = ?`, wallet, id)
	case errors.Is(err, sql.ErrNoRows):
		_, err = s.db.Exec(`INSERT INTO users_oauth_identity (user_id, provider, provider_id) VALUES (?, 'solana', ?)`,
			userID, wallet)
	default:
		return err
	}
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		// Lost the race for a wallet another account took between the
		// bind check and the write — same fact, same name.
		return ErrWalletOwnedElsewhere
	}
	return err
}

// HasWalletLink reports whether the account holds its one solana identity.
// /v1/me states it as `wallet_linked`, so every navigation surface reads
// one server fact instead of inventing probes that disagree (FM-WALLb(ii)).
func (s *Store) HasWalletLink(userID int64) (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM users_oauth_identity WHERE user_id = ? AND provider = 'solana'`,
		userID).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// ── P17: wallet-auth challenges ──
//
// The wallet signs one server-issued message, once. StoreWalletChallenge
// records it keyed by nonce; ConsumeWalletChallenge returns the message and
// deletes the row in the same breath — a replayed challenge is a miss by
// the time the second attempt arrives. SweepWalletChallenges drops stale
// rows; the issuer runs it opportunistically.

// StoreWalletChallenge records a single-use challenge.
func (s *Store) StoreWalletChallenge(nonce, message string) error {
	_, err := s.db.Exec(`INSERT INTO wallet_challenges (nonce, message) VALUES (?, ?)`, nonce, message)
	return err
}

// ConsumeWalletChallenge returns the challenge's message, deleting it —
// single-use enforced at the read.
func (s *Store) ConsumeWalletChallenge(nonce string) (string, error) {
	var message string
	err := s.db.QueryRow(`DELETE FROM wallet_challenges WHERE nonce = ? RETURNING message`, nonce).Scan(&message)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrChallengeNotFound
		}
		return "", err
	}
	return message, nil
}

// SweepWalletChallenges drops challenges older than the TTL. Returns rows
// removed — callers log it, they don't act on it.
func (s *Store) SweepWalletChallenges(olderThan time.Duration) (int64, error) {
	res, err := s.db.Exec(
		`DELETE FROM wallet_challenges WHERE created_at < datetime('now', ?)`,
		"-"+strconv.Itoa(int(olderThan.Minutes()))+" minutes",
	)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

func (s *Store) GetUserByEmail(email string) (*User, error) {
	u := &User{}
	err := s.db.QueryRow("SELECT id, email, password_hash, tier, display_name, created_at FROM users WHERE email = ?", email).
		Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Tier, &u.DisplayName, &u.CreatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrUserNotFound
		}
		return nil, err
	}
	return u, nil
}

func (s *Store) GetUserByID(id int64) (*User, error) {
	u := &User{}
	err := s.db.QueryRow("SELECT id, email, password_hash, tier, display_name, created_at FROM users WHERE id = ?", id).
		Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Tier, &u.DisplayName, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	return u, nil
}

// UpdateUser writes the editable profile fields for a user. Only the display
// name is user-editable; email is the login identity and tier is provider-driven
// (never set from a profile edit — see SetTier, called only from a verified
// webhook). Returns the refreshed row so the caller can echo current state back.
func (s *Store) UpdateUser(id int64, displayName string) (*User, error) {
	if _, err := s.db.Exec("UPDATE users SET display_name = ? WHERE id = ?", displayName, id); err != nil {
		return nil, err
	}
	return s.GetUserByID(id)
}

func (s *Store) CreateProject(userID int64, name, templateVersion, slug, androidPackage, iosBundleID, scheme string) (*Project, error) {
	res, err := s.db.Exec(
		"INSERT INTO projects (user_id, name, template_version, slug, android_package, ios_bundle_id, scheme) VALUES (?, ?, ?, ?, ?, ?, ?)",
		userID, name, templateVersion, slug, androidPackage, iosBundleID, scheme,
	)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetProject(id)
}

func (s *Store) GetProject(id int64) (*Project, error) {
	p := &Project{}
	err := s.db.QueryRow(
		"SELECT id, user_id, name, slug, android_package, ios_bundle_id, scheme, plan_json, status, template_version, created_at FROM projects WHERE id = ?", id,
	).Scan(&p.ID, &p.UserID, &p.Name, &p.Slug, &p.AndroidPackage, &p.IOSBundleID, &p.Scheme, &p.PlanJSON, &p.Status, &p.TemplateVersion, &p.CreatedAt)
	if err != nil {
		return nil, err
	}
	return p, nil
}

func (s *Store) ListProjects(userID int64) ([]Project, error) {
	rows, err := s.db.Query(
		"SELECT id, user_id, name, slug, android_package, ios_bundle_id, scheme, plan_json, status, template_version, created_at FROM projects WHERE user_id = ? ORDER BY created_at DESC", userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var projects []Project
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.ID, &p.UserID, &p.Name, &p.Slug, &p.AndroidPackage, &p.IOSBundleID, &p.Scheme, &p.PlanJSON, &p.Status, &p.TemplateVersion, &p.CreatedAt); err != nil {
			return nil, err
		}
		projects = append(projects, p)
	}
	return projects, nil
}

func (s *Store) UpdateProjectPlan(id int64, planJSON string) error {
	_, err := s.db.Exec("UPDATE projects SET plan_json = ? WHERE id = ?", planJSON, id)
	return err
}

// projectChildTables is every table with a foreign key into projects, and it
// must stay exhaustive over the schema: a table missing here makes
// DeleteProject fail with a constraint error, so the project could not be
// deleted at all (the used-project 500 breaktest BT-8.2 found). No table
// here references another, so their relative deletion order is free.
var projectChildTables = []string{
	"conversations", "prompts", "sessions", "project_memory",
	"architecture_decisions", "conversation_messages", "checkpoints",
	"turn_approvals", "build_receipts", "plan_task_status",
	"message_feedback", "project_git_remotes", "project_keystores",
	"export_jobs", "project_verify_state", "project_assets",
	"prompt_attachments", "triage_reports",
}

// projectScopedTables carry a project_id column without a foreign key, so
// SQLite never blocks their deletion — which is worse than blocking: the
// rows would outlive the project, and since project ids are rowids (no
// AUTOINCREMENT), a later project that draws the same id would inherit a
// dead project's file index. audit_log deliberately stays out: it is an
// audit trail keyed by user, and history of a deleted project is exactly
// what an audit log must keep.
var projectScopedTables = []string{"code_files", "code_chunks", "code_symbols", "code_edges"}

// DeleteProject removes the project and everything hanging off it in one
// transaction: foreign-key children first (SQLite enforces immediately),
// then the FK-less project-scoped index rows, then the project's session
// log, then the project row. Either everything goes or nothing does — a
// partial delete would strand the workspace on disk with no row to attach
// it to, which is how the old single DELETE turned every used project into
// a 500 that could not be retried away.
func (s *Store) DeleteProject(id int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() // no-op once Commit succeeds

	for _, table := range projectChildTables {
		if _, err := tx.Exec("DELETE FROM "+table+" WHERE project_id = ?", id); err != nil {
			return fmt.Errorf("delete %s: %w", table, err)
		}
	}
	for _, table := range projectScopedTables {
		if _, err := tx.Exec("DELETE FROM "+table+" WHERE project_id = ?", id); err != nil {
			return fmt.Errorf("delete %s: %w", table, err)
		}
	}
	if _, err := tx.Exec("DELETE FROM session_events WHERE log_key = ?", ProjectLogKey(id)); err != nil {
		return fmt.Errorf("delete session_events: %w", err)
	}
	if _, err := tx.Exec("DELETE FROM projects WHERE id = ?", id); err != nil {
		return fmt.Errorf("delete project: %w", err)
	}
	return tx.Commit()
}

// HasRunningPrompt reports whether any turn of the project is mid-loop.
// The delete handler consults it before touching the database: destroying
// the workspace under a live loop, then letting its finalize write rows for
// a project that no longer exists, is worse than refusing the delete
// (breaktest BT-8.3).
func (s *Store) HasRunningPrompt(projectID int64) bool {
	var n int
	err := s.db.QueryRow(
		"SELECT COUNT(*) FROM prompts WHERE project_id = ? AND status = 'running'", projectID,
	).Scan(&n)
	return err == nil && n > 0
}

func (s *Store) CreatePrompt(projectID int64, mode, prompt string) (*Prompt, error) {
	return s.CreateTitledPrompt(projectID, mode, prompt, "")
}

// CreateTitledPrompt stores a prompt with its session title. Empty title
// degrades to the truncated prompt — the list line is never blank.
func (s *Store) CreateTitledPrompt(projectID int64, mode, prompt, title string) (*Prompt, error) {
	return s.CreateTitledPromptInConversation(projectID, 0, mode, prompt, title)
}

// CreateTitledPromptInConversation is CreateTitledPrompt with an explicit
// thread. conversationID <= 0 resolves to the project's default thread
// (EnsureDefaultConversation) — the one-conversation-per-project behavior
// every existing caller still gets for free.
func (s *Store) CreateTitledPromptInConversation(projectID, conversationID int64, mode, prompt, title string) (*Prompt, error) {
	if strings.TrimSpace(title) == "" {
		title = truncateTitleForList(prompt)
	}
	if conversationID <= 0 {
		id, err := s.EnsureDefaultConversation(projectID)
		if err != nil {
			return nil, err
		}
		conversationID = id
	}
	// New prompts start 'running': the turn is about to drive the agent loop, and
	// the terminal status is written when it ends. This overrides the column's
	// backfill default of 'completed', which only ever applies to rows that
	// predate the status column.
	res, err := s.db.Exec(
		"INSERT INTO prompts (project_id, conversation_id, mode, prompt, status, title) VALUES (?, ?, ?, ?, 'running', ?)",
		projectID, conversationID, mode, prompt, title,
	)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	_ = s.TouchConversation(conversationID)
	return s.GetPrompt(id)
}

// ProjectTokenUsage rolls the per-turn token ledger into a session total
// (H13's token meter): every prompt row's in/out/cache-read summed. The
// chat rail renders it once; metrics.go keeps its own counters.
func (s *Store) ProjectTokenUsage(projectID int64) (in, out, cacheRead int64, err error) {
	row := s.db.QueryRow(
		"SELECT COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0), COALESCE(SUM(cache_read_tokens),0) FROM prompts WHERE project_id = ?", projectID)
	err = row.Scan(&in, &out, &cacheRead)
	return in, out, cacheRead, err
}

// UpdatePromptTitle upgrades a turn's session title (H13): the LLM path
// replaces the fallback after the turn completes. Empty titles are ignored —
// the fallback is never worse.
func (s *Store) UpdatePromptTitle(promptID int64, title string) error {
	if strings.TrimSpace(title) == "" {
		return nil
	}
	_, err := s.db.Exec("UPDATE prompts SET title = ? WHERE id = ?", title, promptID)
	return err
}

// truncateTitleForList is the honest fallback title: the prompt's first
// line, word-boundary cut at 80 bytes.
func truncateTitleForList(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(s)
	if len(s) > 80 {
		cut := s[:80]
		if i := strings.LastIndex(cut, " "); i > 0 {
			cut = cut[:i]
		}
		s = cut
	}
	if s == "" {
		s = "Untitled turn"
	}
	return s
}

const promptColumns = "id, project_id, mode, prompt, response, status, round, skipped_gates, updated_at, created_at, title, files_touched"

// scanPrompt reads one prompt row in promptColumns order, mapping the nullable
// updated_at (existing rows predate it) onto the optional struct field.
func scanPrompt(row *sql.Row) (*Prompt, error) {
	p := &Prompt{}
	var updatedAt sql.NullTime
	var skipped sql.NullString
	var touched sql.NullString
	if err := row.Scan(&p.ID, &p.ProjectID, &p.Mode, &p.Prompt, &p.Response, &p.Status, &p.Round, &skipped, &updatedAt, &p.CreatedAt, &p.Title, &touched); err != nil {
		return nil, err
	}
	if skipped.Valid && strings.TrimSpace(skipped.String) != "" {
		p.SkippedGatesJSON = skipped.String
	}
	// Corrupt JSON degrades to an empty set — the turn then verifies over
	// this run's journal alone, exactly the pre-row behavior, never worse.
	if touched.Valid && touched.String != "" {
		if err := json.Unmarshal([]byte(touched.String), &p.FilesTouched); err != nil {
			log.Printf("prompt %d: corrupt files_touched: %v", p.ID, err)
		}
	}
	if updatedAt.Valid {
		t := updatedAt.Time
		p.UpdatedAt = &t
	}
	return p, nil
}

func (s *Store) GetPrompt(id int64) (*Prompt, error) {
	return scanPrompt(s.db.QueryRow("SELECT "+promptColumns+" FROM prompts WHERE id = ?", id))
}

// GetLatestPrompt returns the most recent prompt for a project, or (nil, nil)
// when the project has none. The chat uses it on load to tell whether the last
// turn ended cleanly or was interrupted and can be resumed.
func (s *Store) GetLatestPrompt(projectID int64) (*Prompt, error) {
	p, err := scanPrompt(s.db.QueryRow(
		"SELECT "+promptColumns+" FROM prompts WHERE project_id = ? ORDER BY id DESC LIMIT 1", projectID))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return p, err
}

// PromptIndexEntry is one row of the turn index: metadata only, never the
// full prompt text. History lists render from this; transcripts come from
// the conversation endpoints.
type PromptIndexEntry struct {
	ID        int64     `json:"id"`
	Mode      string    `json:"mode"`
	Title     string    `json:"title"`
	Status    string    `json:"status"`
	Round     int       `json:"round"`
	CreatedAt time.Time `json:"created_at"`
}

// ListPrompts returns the turn index newest first, cursor-paged by id.
// beforeID <= 0 starts from the newest. limit clamps to 1..100.
func (s *Store) ListPrompts(projectID, beforeID int64, limit int) ([]PromptIndexEntry, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	var rows *sql.Rows
	var err error
	if beforeID > 0 {
		rows, err = s.db.Query(
			`SELECT id, mode, title, status, round, created_at FROM prompts
			 WHERE project_id = ? AND id < ? ORDER BY id DESC LIMIT ?`,
			projectID, beforeID, limit)
	} else {
		rows, err = s.db.Query(
			`SELECT id, mode, title, status, round, created_at FROM prompts
			 WHERE project_id = ? ORDER BY id DESC LIMIT ?`,
			projectID, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PromptIndexEntry
	for rows.Next() {
		var e PromptIndexEntry
		var title sql.NullString
		if err := rows.Scan(&e.ID, &e.Mode, &title, &e.Status, &e.Round, &e.CreatedAt); err != nil {
			return nil, err
		}
		e.Title = title.String
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) UpdatePromptResponse(id int64, response string) error {
	_, err := s.db.Exec("UPDATE prompts SET response = ? WHERE id = ?", response, id)
	return err
}

// UpdatePromptSkippedGates persists the verify stages that could not run when
// the turn verified, as a JSON string array. Best-effort per caller intent:
// a bookkeeping failure never fails a finished turn.
func (s *Store) UpdatePromptSkippedGates(id int64, gates []string) {
	raw, err := json.Marshal(gates)
	if err != nil {
		raw = []byte("[]")
	}
	_, _ = s.db.Exec("UPDATE prompts SET skipped_gates = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?", string(raw), id)
}

// UpdatePromptFilesTouched persists the union of files every leg of this
// turn changed, as a JSON string array. The loop's FS journal is run-scoped
// (a fresh tools.NewFS per resume), so this row is the durable cross-leg set
// a resumed turn's verify gate reads. Best-effort per caller intent: a
// bookkeeping failure never fails a finished turn.
func (s *Store) UpdatePromptFilesTouched(id int64, files []string) {
	raw, err := json.Marshal(files)
	if err != nil {
		raw = []byte("[]")
	}
	_, _ = s.db.Exec("UPDATE prompts SET files_touched = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?", string(raw), id)
}

// UpdatePromptStatus records the run state of a turn and how far it got. The
// timestamp lets a later reader see when the turn last made progress.
func (s *Store) UpdatePromptStatus(id int64, status string, round int) error {
	_, err := s.db.Exec("UPDATE prompts SET status = ?, round = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?", status, round, id)
	return err
}

// MarkInterruptedPrompts flips every running/waiting prompt to interrupted.
// Called once at server start: a prompt whose loop died with the process
// (crash, restart, kill) is neither running nor answerable, and resume must
// not pretend the session's in-memory state survived. Returns the affected
// ids so callers can log/metric the restart blast radius.
func (s *Store) MarkInterruptedPrompts() ([]int64, error) {
	rows, err := s.db.Query("SELECT id FROM prompts WHERE status IN ('running','waiting','paused')")
	if err != nil {
		return nil, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	var projectID int64
	for _, id := range ids {
		if err := s.db.QueryRow("SELECT project_id FROM prompts WHERE id = ?", id).Scan(&projectID); err != nil {
			return ids, err
		}
		if _, err := s.db.Exec("UPDATE prompts SET status = 'interrupted', updated_at = CURRENT_TIMESTAMP WHERE id = ?", id); err != nil {
			return ids, err
		}
		// Close the orphan in the transcript too, not just the prompts row: the
		// chat reads conversation_messages, and a turn left running by a dead
		// process would otherwise look still in flight to anyone who reloads.
		// DSH calls this interruptedTurnClosers — close, don't truncate.
		if err := s.AppendMessage(projectID, id, "assistant",
			`[{"type":"turn_end","status":"interrupted"}]`, 0, 0); err != nil {
			return ids, err
		}
	}
	return ids, nil
}

// MigrateInjectedContext rewrites the orchestrator's injected guidance — auto-
// attached skill bodies and verify-gate repair notes — that older turns stored
// as ordinary user text. Those blocks render as chat bubbles, which put a
// 400-line skill document in the middle of the conversation; as context blocks
// the transcript shows one line and keeps the body a tap away.
//
// Detection is by the exact prefixes the orchestrator emits. These are machine
// texts with a stable shape, so a prefix match cannot touch real user prose.
// Idempotent: a context block is never rewritten, and a text block that no
// longer starts with a prefix is left alone.
func (s *Store) MigrateInjectedContext() (rewritten int, err error) {
	prefixes := []string{
		"A project skill applies to what you just did.",
		"The code compiles, but ESLint reports problems:",
	}
	args := make([]any, 0, len(prefixes))
	for _, p := range prefixes {
		args = append(args, p+"%")
	}
	rows, err := s.db.Query(`SELECT rowid, blocks FROM conversation_messages
		WHERE json_extract(blocks, '$[0].type') = 'text'
		AND (`+prefixClause(len(prefixes))+`)`, args...)
	if err != nil {
		return 0, err
	}
	var rowids []int64
	var blocksList []string
	for rows.Next() {
		var rid int64
		var blocks string
		if err := rows.Scan(&rid, &blocks); err != nil {
			rows.Close()
			return 0, err
		}
		rowids = append(rowids, rid)
		blocksList = append(blocksList, blocks)
	}
	rows.Close()

	for i, rid := range rowids {
		updated, ok := rewriteOneContext(blocksList[i], prefixes)
		if !ok {
			continue
		}
		if _, err := s.db.Exec("UPDATE conversation_messages SET blocks = ? WHERE rowid = ?", updated, rid); err != nil {
			return i, err
		}
	}
	return len(rowids), nil
}

func prefixClause(n int) string {
	parts := make([]string, n)
	for i := range parts {
		parts[i] = "json_extract(blocks, '$[0].text') LIKE ?"
	}
	return joinOr(parts)
}

func joinOr(parts []string) string {
	out := parts[0]
	for _, p := range parts[1:] {
		out += " OR " + p
	}
	return out
}

// rewriteOneContext converts a leading injected-text block into a context block
// carrying a one-line title. The body keeps the full text so the model's replay
// and the expandable UI still have it.
func rewriteOneContext(blocksJSON string, prefixes []string) (string, bool) {
	var blocks []map[string]any
	if json.Unmarshal([]byte(blocksJSON), &blocks) != nil || len(blocks) == 0 {
		return "", false
	}
	first, _ := blocks[0]["type"].(string)
	if first != "text" {
		return "", false
	}
	text, _ := blocks[0]["text"].(string)
	if text == "" {
		return "", false
	}
	title := ""
	matched := ""
	for _, p := range prefixes {
		if strings.HasPrefix(text, p) {
			title = contextTitle(p, text)
			matched = p
			break
		}
	}
	if title == "" {
		return "", false
	}
	blocks[0] = map[string]any{
		"type":  "context",
		"kind":  contextKind(matched),
		"title": title,
		"body":  text,
	}
	out, err := json.Marshal(blocks)
	if err != nil {
		return "", false
	}
	return string(out), true
}

func contextTitle(prefix, text string) string {
	switch {
	case strings.HasPrefix(prefix, "A project skill applies"):
		// The skill's own heading follows the fixed intro line; take the first
		// heading line as the title so the chip names the actual skill.
		rest := strings.TrimPrefix(text, "A project skill applies to what you just did. Follow it for the rest of this work:")
		for _, line := range strings.Split(rest, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			line = strings.TrimLeft(line, "# ")
			if line != "" {
				return "Applied skill: " + line
			}
		}
		return "Applied a project skill"
	case strings.HasPrefix(prefix, "The code compiles"):
		return "Verification failed — ESLint"
	default:
		return "Applied guidance"
	}
}

// contextKind reports the injection family for a prefix, for the chip icon.
func contextKind(prefix string) string {
	switch {
	case strings.HasPrefix(prefix, "A project skill applies"):
		return "skill"
	case strings.HasPrefix(prefix, "The code compiles"):
		return "verify"
	default:
		return "skill"
	}
}

// ScopeDeviceBuild is the turn_approvals scope unlocking build_apk.
const ScopeDeviceBuild = "device_build"

// ScopeTier2Shell is the turn_approvals scope recording consent for a
// surfaced tier-2 shell command (W1.6): the answer flow writes it on an
// affirmative answer, and the approval seam's escalation answerer reads it,
// so a granted turn runs exactly the command the user said yes to.
const ScopeTier2Shell = "tier2_shell"

// GrantApproval records a mid-turn user approval for one scope
// ("device_build", "tier2_shell").
// INSERT OR IGNORE: re-granting is a no-op, and approval never spans prompts.
func (s *Store) GrantApproval(projectID, promptID int64, scope string) error {
	_, err := s.db.Exec("INSERT OR IGNORE INTO turn_approvals (project_id, prompt_id, scope) VALUES (?, ?, ?)", projectID, promptID, scope)
	return err
}

// HasApproval reports whether the prompt lifecycle holds the scope.
func (s *Store) HasApproval(promptID int64, scope string) bool {
	var count int
	_ = s.db.QueryRow("SELECT COUNT(*) FROM turn_approvals WHERE prompt_id = ? AND scope = ?", promptID, scope).Scan(&count)
	return count > 0
}

// BuildReceipt is one finished build_apk run: its verdict, artifact, and a
// one-line summary. Status is "built" or "failed".
type BuildReceipt struct {
	ID        int64  `json:"id"`
	ProjectID int64  `json:"project_id"`
	PromptID  int64  `json:"prompt_id"`
	Status    string `json:"status"`
	ApkPath   string `json:"apk_path,omitempty"`
	Summary   string `json:"summary,omitempty"`
}

// RecordBuildReceipt files a finished build. Called from the tool executor,
// which outlives any particular turn state — the receipt is the durable
// half of the build contract.
func (s *Store) RecordBuildReceipt(projectID, promptID int64, status, apkPath, summary string) error {
	_, err := s.db.Exec(
		"INSERT INTO build_receipts (project_id, prompt_id, status, apk_path, summary) VALUES (?, ?, ?, ?, ?)",
		projectID, promptID, status, apkPath, summary)
	return err
}

// PendingBuildReceipts returns unacknowledged receipts, oldest first.
func (s *Store) PendingBuildReceipts(projectID int64) ([]BuildReceipt, error) {
	rows, err := s.db.Query(
		"SELECT id, project_id, prompt_id, status, apk_path, summary FROM build_receipts WHERE project_id = ? AND acknowledged = 0 ORDER BY id ASC",
		projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BuildReceipt
	for rows.Next() {
		var r BuildReceipt
		if err := rows.Scan(&r.ID, &r.ProjectID, &r.PromptID, &r.Status, &r.ApkPath, &r.Summary); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// AckBuildReceipts marks receipts read so they are delivered exactly once.
// Reading without acking would re-inject the same verdict every turn.
func (s *Store) AckBuildReceipts(ids []int64) error {
	for _, id := range ids {
		if _, err := s.db.Exec("UPDATE build_receipts SET acknowledged = 1 WHERE id = ?", id); err != nil {
			return err
		}
	}
	return nil
}

// PlanTask is the durable per-project backlog row. Status mirrors the in-memory
// tracker: pending, in_progress, completed, failed — plus superseded, set when
// a plan edit removes the work the row described.
type PlanTask struct {
	TaskID            string `json:"task_id"`
	Description       string `json:"description"`
	Status            string `json:"status"`
	Result            string `json:"result,omitempty"`
	CompletedByPrompt int64  `json:"completed_by_prompt,omitempty"`
}

// UpsertPlanTask writes one task's state, creating or refreshing the row.
// completed_by_prompt is recorded only when the status is terminal
// (completed) so attribution means "the turn that finished it".
// The conflict path keeps an existing terminal status: seeding writes
// "pending" for the whole backlog, and a blind overwrite would clobber a
// persisted completion back to pending on every session start.
func (s *Store) UpsertPlanTask(projectID int64, t PlanTask) error {
	by := t.CompletedByPrompt
	if t.Status != "completed" {
		by = 0
	}
	_, err := s.db.Exec(
		`INSERT INTO plan_task_status (project_id, task_id, description, status, result, completed_by_prompt)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(project_id, task_id) DO UPDATE SET
		   description = excluded.description,
		   status = CASE WHEN plan_task_status.status IN ('completed','superseded') AND excluded.status = 'pending'
		         THEN plan_task_status.status ELSE excluded.status END,
		   result = CASE WHEN plan_task_status.status IN ('completed','superseded') AND excluded.status = 'pending'
		         THEN plan_task_status.result ELSE excluded.result END,
		   completed_by_prompt = CASE WHEN plan_task_status.status = 'completed' AND excluded.status = 'pending'
		         THEN plan_task_status.completed_by_prompt ELSE excluded.completed_by_prompt END,
		   updated_at = CURRENT_TIMESTAMP`,
		projectID, t.TaskID, t.Description, t.Status, t.Result, by,
	)
	return err
}

// LoadPlanTasks returns the project's durable backlog, oldest first.
// ImportPlanTaskStatus writes a status that came from the plan artifact
// (.talon/plan.md) — W3.4's file→system direction. It deliberately bypasses
// UpsertPlanTask's terminal-status protection: that clause exists to stop a
// seeding sweep from clobbering recorded completions back to pending, while
// this write IS the authority it protects against — a human unchecked a box,
// and the file is editable precisely so they can reopen work. Attribution is
// cleared: no prompt finished this; the person at the keyboard did.
func (s *Store) ImportPlanTaskStatus(projectID int64, taskID, description, status string) error {
	switch status {
	case "pending", "in_progress", "completed", "failed":
	default:
		return fmt.Errorf("plan task %s: unknown status %q", taskID, status)
	}
	_, err := s.db.Exec(
		`INSERT INTO plan_task_status (project_id, task_id, description, status, result, completed_by_prompt)
		 VALUES (?, ?, ?, ?, '', 0)
		 ON CONFLICT(project_id, task_id) DO UPDATE SET
		   description = excluded.description,
		   status = excluded.status,
		   completed_by_prompt = 0,
		   updated_at = CURRENT_TIMESTAMP`,
		projectID, taskID, description, status,
	)
	return err
}

func (s *Store) LoadPlanTasks(projectID int64) ([]PlanTask, error) {
	rows, err := s.db.Query(
		"SELECT task_id, description, status, result, completed_by_prompt FROM plan_task_status WHERE project_id = ? ORDER BY id ASC", projectID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PlanTask
	for rows.Next() {
		var t PlanTask
		if err := rows.Scan(&t.TaskID, &t.Description, &t.Status, &t.Result, &t.CompletedByPrompt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}

// SupersedeMissingPlanTasks marks rows whose task_id no longer appears in the
// current plan as superseded (kept, not deleted). Returns the count so callers
// can log the drift. A row already superseded stays that way.
func (s *Store) SupersedeMissingPlanTasks(projectID int64, liveTaskIDs []string) (int, error) {
	rows, err := s.db.Query(
		"SELECT task_id FROM plan_task_status WHERE project_id = ? AND status != 'superseded'", projectID,
	)
	if err != nil {
		return 0, err
	}
	var stale []string
	live := make(map[string]bool, len(liveTaskIDs))
	for _, id := range liveTaskIDs {
		live[id] = true
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		if !live[id] {
			stale = append(stale, id)
		}
	}
	rows.Close()
	for _, id := range stale {
		if _, err := s.db.Exec(
			"UPDATE plan_task_status SET status = 'superseded', updated_at = CURRENT_TIMESTAMP WHERE project_id = ? AND task_id = ?", projectID, id,
		); err != nil {
			return len(stale), err
		}
	}
	return len(stale), nil
}

func (s *Store) CreateSession(projectID int64) (*Session, error) {
	res, err := s.db.Exec("INSERT INTO sessions (project_id) VALUES (?)", projectID)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetSession(id)
}

// TouchSession marks the session active — the reaper's clock. Every stream
// viewer join and every agent prompt calls this; a session no one touches
// is idle and the reaper's business.
func (s *Store) TouchSession(sessionID int64) error {
	_, err := s.db.Exec(`UPDATE sessions SET last_active_at = CURRENT_TIMESTAMP WHERE id = ? AND state != 'destroyed'`, sessionID)
	return err
}

// IdleSessions returns non-destroyed sessions whose last activity is older
// than the window — the reaper's candidates. A row with no last_active_at
// (pre-DV4) falls back to its started_at: an old session is idle.
func (s *Store) IdleSessions(olderThan time.Duration) ([]Session, error) {
	rows, err := s.db.Query(`
		SELECT id, project_id, state, container_id, container_name, adb_port,
		       container_state, started_at, ended_at, last_active_at
		FROM sessions
		WHERE state != 'destroyed'
		  AND COALESCE(last_active_at, started_at) < datetime('now', ?)
	`, fmt.Sprintf("-%d seconds", int(olderThan.Seconds())))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Session
	for rows.Next() {
		var sess Session
		var lastActive sql.NullTime
		if err := rows.Scan(&sess.ID, &sess.ProjectID, &sess.State, &sess.ContainerID,
			&sess.ContainerName, &sess.ADBPort, &sess.ContainerState, &sess.StartedAt,
			&sess.EndedAt, &lastActive); err != nil {
			return nil, err
		}
		out = append(out, sess)
	}
	return out, nil
}

// LatestLiveSession returns the project's most recent non-destroyed
// session — the device the preview and metro bridge attach to. Absence is
// "no device running", reported by the caller, never fabricated.
func (s *Store) LatestLiveSession(projectID int64) (*Session, error) {
	sess := &Session{}
	err := s.db.QueryRow(
		"SELECT id, project_id, state, container_id, container_name, adb_port, container_state, started_at, ended_at FROM sessions WHERE project_id = ? AND state != 'destroyed' ORDER BY id DESC LIMIT 1", projectID).
		Scan(&sess.ID, &sess.ProjectID, &sess.State, &sess.ContainerID, &sess.ContainerName, &sess.ADBPort, &sess.ContainerState, &sess.StartedAt, &sess.EndedAt)
	if err != nil {
		return nil, err
	}
	return sess, nil
}

func (s *Store) GetSession(id int64) (*Session, error) {
	sess := &Session{}
	err := s.db.QueryRow("SELECT id, project_id, state, container_id, container_name, adb_port, container_state, started_at, ended_at FROM sessions WHERE id = ?", id).
		Scan(&sess.ID, &sess.ProjectID, &sess.State, &sess.ContainerID, &sess.ContainerName, &sess.ADBPort, &sess.ContainerState, &sess.StartedAt, &sess.EndedAt)
	if err != nil {
		return nil, err
	}
	return sess, nil
}

func (s *Store) UpdateSessionState(id int64, state string) error {
	var err error
	if state == "destroyed" {
		_, err = s.db.Exec("UPDATE sessions SET state = ?, ended_at = CURRENT_TIMESTAMP WHERE id = ?", state, id)
	} else {
		_, err = s.db.Exec("UPDATE sessions SET state = ? WHERE id = ?", state, id)
	}
	return err
}

// UpdateSessionContainer stores container metadata for a session.
func (s *Store) UpdateSessionContainer(sessionID int64, containerID, containerName string, adbPort int) error {
	_, err := s.db.Exec(
		"UPDATE sessions SET container_id = ?, container_name = ?, adb_port = ? WHERE id = ?",
		containerID, containerName, adbPort, sessionID,
	)
	return err
}

// UpdateSessionContainerState stores the Docker container state.
func (s *Store) UpdateSessionContainerState(sessionID int64, containerState string) error {
	_, err := s.db.Exec("UPDATE sessions SET container_state = ? WHERE id = ?", containerState, sessionID)
	return err
}

// GetActiveSessions returns all sessions with state != "destroyed".
func (s *Store) GetActiveSessions() ([]Session, error) {
	rows, err := s.db.Query(
		"SELECT id, project_id, state, container_id, container_name, adb_port, container_state, started_at, ended_at FROM sessions WHERE state != 'destroyed' ORDER BY started_at DESC",
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []Session
	for rows.Next() {
		var sess Session
		if err := rows.Scan(&sess.ID, &sess.ProjectID, &sess.State, &sess.ContainerID, &sess.ContainerName, &sess.ADBPort, &sess.ContainerState, &sess.StartedAt, &sess.EndedAt); err != nil {
			return nil, err
		}
		sessions = append(sessions, sess)
	}
	return sessions, nil
}

// GetSessionByContainerID finds a session by its Docker container ID.
func (s *Store) GetSessionByContainerID(containerID string) (*Session, error) {
	sess := &Session{}
	err := s.db.QueryRow(
		"SELECT id, project_id, state, container_id, container_name, adb_port, container_state, started_at, ended_at FROM sessions WHERE container_id = ?", containerID,
	).Scan(&sess.ID, &sess.ProjectID, &sess.State, &sess.ContainerID, &sess.ContainerName, &sess.ADBPort, &sess.ContainerState, &sess.StartedAt, &sess.EndedAt)
	if err != nil {
		return nil, err
	}
	return sess, nil
}

func (s *Store) GetUsage(userID int64) (*Usage, error) {
	u := &Usage{}
	err := s.db.QueryRow("SELECT id, user_id, prompts_used, device_minutes, cost_usd, period_start FROM usage WHERE user_id = ? ORDER BY period_start DESC LIMIT 1", userID).
		Scan(&u.ID, &u.UserID, &u.PromptsUsed, &u.DeviceMinutes, &u.CostUSD, &u.PeriodStart)
	if err == sql.ErrNoRows {
		_, err2 := s.db.Exec("INSERT INTO usage (user_id) VALUES (?)", userID)
		if err2 != nil {
			return nil, err2
		}
		return s.GetUsage(userID)
	}
	if err != nil {
		return nil, err
	}
	return u, nil
}

func (s *Store) IncrementPrompts(userID int64) error {
	_, err := s.db.Exec("UPDATE usage SET prompts_used = prompts_used + 1 WHERE user_id = ? AND period_start = (SELECT MAX(period_start) FROM usage WHERE user_id = ?)", userID, userID)
	return err
}

func (s *Store) IncrementDeviceMinutes(userID int64, minutes float64) error {
	_, err := s.db.Exec("UPDATE usage SET device_minutes = device_minutes + ? WHERE user_id = ? AND period_start = (SELECT MAX(period_start) FROM usage WHERE user_id = ?)", minutes, userID, userID)
	return err
}

func memoryCipher() *secrets.Cipher {
	key := os.Getenv("MEMORY_ENCRYPTION_KEY")
	if key == "" {
		key = os.Getenv("JWT_SECRET")
	}
	if key == "" {
		return nil
	}
	return secrets.FromPassphrase(key)
}

func decryptContent(plain, enc string) string {
	if enc == "" {
		return plain
	}
	c := memoryCipher()
	if c == nil {
		return plain
	}
	b, err := c.Decrypt(enc)
	if err != nil {
		return plain
	}
	return string(b)
}

// Project Memory methods

func (s *Store) AddMemory(projectID int64, entryType, content, metadata string) error {
	enc := ""
	if c := memoryCipher(); c != nil {
		if sealed, err := c.Encrypt([]byte(content)); err == nil {
			enc = sealed
		}
	}
	_, err := s.db.Exec("INSERT INTO project_memory (project_id, user_id, entry_type, content, content_encrypted, metadata) VALUES (?, (SELECT user_id FROM projects WHERE id = ?), ?, ?, ?, ?)", projectID, projectID, entryType, content, enc, metadata)
	if err != nil {
		return err
	}
	// Audit — best effort, never fails the write.
	end := len(content)
	if end > 120 {
		end = 120
	}
	_, _ = s.db.Exec("INSERT INTO audit_log (user_id, project_id, action, target_id, detail) VALUES ((SELECT user_id FROM projects WHERE id = ?), ?, 'write_memory', ?, ?)", projectID, projectID, entryType, content[:end])
	return nil
}

func (s *Store) GetMemoryByType(projectID int64, entryType string) ([]ProjectMemory, error) {
	rows, err := s.db.Query("SELECT id, project_id, entry_type, content, content_encrypted, metadata, created_at FROM project_memory WHERE project_id = ? AND entry_type = ? ORDER BY created_at ASC", projectID, entryType)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var memories []ProjectMemory
	for rows.Next() {
		var m ProjectMemory
		var enc string
		if err := rows.Scan(&m.ID, &m.ProjectID, &m.EntryType, &m.Content, &enc, &m.Metadata, &m.CreatedAt); err != nil {
			return nil, err
		}
		m.Content = decryptContent(m.Content, enc)
		memories = append(memories, m)
	}
	return memories, nil
}

func (s *Store) GetAllMemory(projectID int64) ([]ProjectMemory, error) {
	rows, err := s.db.Query("SELECT id, project_id, entry_type, content, content_encrypted, metadata, created_at FROM project_memory WHERE project_id = ? ORDER BY created_at ASC", projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var memories []ProjectMemory
	for rows.Next() {
		var m ProjectMemory
		var enc string
		if err := rows.Scan(&m.ID, &m.ProjectID, &m.EntryType, &m.Content, &enc, &m.Metadata, &m.CreatedAt); err != nil {
			return nil, err
		}
		m.Content = decryptContent(m.Content, enc)
		memories = append(memories, m)
	}
	return memories, nil
}

func (s *Store) SearchMemoryFTS(projectID int64, query, entryType string, limit int) ([]ProjectMemory, error) {
	if limit <= 0 || limit > 50 {
		limit = 50
	}
	// FTS5 query: escape double quotes and append * for prefix matching on last token.
	q := `"` + strings.ReplaceAll(query, `"`, `""`) + `"`
	if entryType != "" {
		rows, err := s.db.Query(
			`SELECT id, project_id, entry_type, content, content_encrypted, metadata, created_at FROM project_memory WHERE project_id = ? AND entry_type = ? AND id IN (SELECT rowid FROM project_memory_fts WHERE project_memory_fts MATCH ?) ORDER BY created_at DESC LIMIT ?`,
			projectID, entryType, q, limit)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []ProjectMemory
		for rows.Next() {
			var m ProjectMemory
			var enc string
			if err := rows.Scan(&m.ID, &m.ProjectID, &m.EntryType, &m.Content, &enc, &m.Metadata, &m.CreatedAt); err != nil {
				return nil, err
			}
			m.Content = decryptContent(m.Content, enc)
			out = append(out, m)
		}
		return out, rows.Err()
	}
	rows, err := s.db.Query(
		`SELECT id, project_id, entry_type, content, content_encrypted, metadata, created_at FROM project_memory WHERE project_id = ? AND id IN (SELECT rowid FROM project_memory_fts WHERE project_memory_fts MATCH ?) ORDER BY created_at DESC LIMIT ?`,
		projectID, q, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProjectMemory
	for rows.Next() {
		var m ProjectMemory
		var enc string
		if err := rows.Scan(&m.ID, &m.ProjectID, &m.EntryType, &m.Content, &enc, &m.Metadata, &m.CreatedAt); err != nil {
			return nil, err
		}
		m.Content = decryptContent(m.Content, enc)
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) UpdateMemory(id int64, content, metadata string) error {
	var projectID, userID int64
	_ = s.db.QueryRow("SELECT project_id, user_id FROM project_memory WHERE id = ?", id).Scan(&projectID, &userID)
	enc := ""
	if c := memoryCipher(); c != nil {
		if sealed, err := c.Encrypt([]byte(content)); err == nil {
			enc = sealed
		}
	}
	_, err := s.db.Exec("UPDATE project_memory SET content = ?, content_encrypted = ?, metadata = ? WHERE id = ?", content, enc, metadata, id)
	if err == nil && projectID != 0 {
		_, _ = s.db.Exec("INSERT INTO audit_log (user_id, project_id, action, target_id, detail) VALUES (?, ?, 'update_memory', ?, ?)", userID, projectID, strconv.FormatInt(id, 10), content[:minInt(len(content), 120)])
	}
	return err
}

func (s *Store) DeleteMemory(id int64) error {
	var projectID, userID int64
	_ = s.db.QueryRow("SELECT project_id, user_id FROM project_memory WHERE id = ?", id).Scan(&projectID, &userID)
	_, err := s.db.Exec("DELETE FROM project_memory WHERE id = ?", id)
	if err == nil && projectID != 0 {
		_, _ = s.db.Exec("INSERT INTO audit_log (user_id, project_id, action, target_id, detail) VALUES (?, ?, 'delete_memory', ?, '')", userID, projectID, strconv.FormatInt(id, 10))
	}
	return err
}

func (s *Store) ClearMemory(projectID int64) error {
	var userID int64
	_ = s.db.QueryRow("SELECT user_id FROM projects WHERE id = ?", projectID).Scan(&userID)
	_, err := s.db.Exec("DELETE FROM project_memory WHERE project_id = ?", projectID)
	if err == nil {
		_, _ = s.db.Exec("INSERT INTO audit_log (user_id, project_id, action, target_id, detail) VALUES (?, ?, 'clear_memory', ?, '')", userID, projectID, strconv.FormatInt(projectID, 10))
	}
	return err
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

type AuditEntry struct {
	ID        int64     `json:"id"`
	UserID    int64     `json:"user_id"`
	ProjectID int64     `json:"project_id"`
	Action    string    `json:"action"`
	TargetID  string    `json:"target_id"`
	Detail    string    `json:"detail"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *Store) AuditLog(userID, projectID int64, action, targetID, detail string) error {
	_, err := s.db.Exec("INSERT INTO audit_log (user_id, project_id, action, target_id, detail) VALUES (?, ?, ?, ?, ?)", userID, projectID, action, targetID, detail)
	return err
}

func (s *Store) ListAuditLog(projectID int64, limit int) ([]AuditEntry, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := s.db.Query("SELECT id, user_id, project_id, action, target_id, detail, created_at FROM audit_log WHERE project_id = ? ORDER BY id DESC LIMIT ?", projectID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditEntry
	for rows.Next() {
		var r AuditEntry
		if err := rows.Scan(&r.ID, &r.UserID, &r.ProjectID, &r.Action, &r.TargetID, &r.Detail, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SlugExists checks if a slug is already in use.
func (s *Store) SlugExists(slug string) bool {
	var count int
	s.db.QueryRow("SELECT COUNT(*) FROM projects WHERE slug = ?", slug).Scan(&count)
	return count > 0
}

// AndroidPackageExists checks if an Android package name is already in use.
func (s *Store) AndroidPackageExists(pkg string) bool {
	var count int
	s.db.QueryRow("SELECT COUNT(*) FROM projects WHERE android_package = ?", pkg).Scan(&count)
	return count > 0
}

// Architecture Decision Record methods

func (s *Store) CreateADR(projectID int64, decision, context, options, outcome string) (*ArchitectureDecision, error) {
	res, err := s.db.Exec(
		"INSERT INTO architecture_decisions (project_id, decision, context, options, outcome) VALUES (?, ?, ?, ?, ?)",
		projectID, decision, context, options, outcome,
	)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetADR(id)
}

func (s *Store) GetADR(id int64) (*ArchitectureDecision, error) {
	adr := &ArchitectureDecision{}
	err := s.db.QueryRow(
		"SELECT id, project_id, decision, context, options, outcome, status, created_at, updated_at FROM architecture_decisions WHERE id = ?", id,
	).Scan(&adr.ID, &adr.ProjectID, &adr.Decision, &adr.Context, &adr.Options, &adr.Outcome, &adr.Status, &adr.CreatedAt, &adr.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return adr, nil
}

func (s *Store) GetADRsByProject(projectID int64) ([]ArchitectureDecision, error) {
	rows, err := s.db.Query(
		"SELECT id, project_id, decision, context, options, outcome, status, created_at, updated_at FROM architecture_decisions WHERE project_id = ? ORDER BY created_at ASC", projectID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var adrs []ArchitectureDecision
	for rows.Next() {
		var adr ArchitectureDecision
		if err := rows.Scan(&adr.ID, &adr.ProjectID, &adr.Decision, &adr.Context, &adr.Options, &adr.Outcome, &adr.Status, &adr.CreatedAt, &adr.UpdatedAt); err != nil {
			return nil, err
		}
		adrs = append(adrs, adr)
	}
	return adrs, nil
}

func (s *Store) GetActiveADRs(projectID int64) ([]ArchitectureDecision, error) {
	rows, err := s.db.Query(
		"SELECT id, project_id, decision, context, options, outcome, status, created_at, updated_at FROM architecture_decisions WHERE project_id = ? AND status = 'accepted' ORDER BY created_at ASC", projectID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var adrs []ArchitectureDecision
	for rows.Next() {
		var adr ArchitectureDecision
		if err := rows.Scan(&adr.ID, &adr.ProjectID, &adr.Decision, &adr.Context, &adr.Options, &adr.Outcome, &adr.Status, &adr.CreatedAt, &adr.UpdatedAt); err != nil {
			return nil, err
		}
		adrs = append(adrs, adr)
	}
	return adrs, nil
}

func (s *Store) UpdateADR(id int64, outcome, status string) error {
	_, err := s.db.Exec(
		"UPDATE architecture_decisions SET outcome = ?, status = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?",
		outcome, status, id,
	)
	return err
}

func (s *Store) SupersedeADR(id int64) error {
	_, err := s.db.Exec(
		"UPDATE architecture_decisions SET status = 'superseded', updated_at = CURRENT_TIMESTAMP WHERE id = ?",
		id,
	)
	return err
}

func (s *Store) DeleteADR(id int64) error {
	_, err := s.db.Exec("DELETE FROM architecture_decisions WHERE id = ?", id)
	return err
}

// SaveTriageReport upserts the Play Billing cutover triage for a project.
// One row per project: revising the triage rewrites it, it never forks.
func (s *Store) SaveTriageReport(projectID int64, decision, cutoverDate string, exposureCents int64, notes string) (*TriageReport, error) {
	_, err := s.db.Exec(
		`INSERT INTO triage_reports (project_id, decision, cutover_date, exposure_cents, notes)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(project_id) DO UPDATE SET decision = ?, cutover_date = ?, exposure_cents = ?, notes = ?, updated_at = CURRENT_TIMESTAMP`,
		projectID, decision, cutoverDate, exposureCents, notes,
		decision, cutoverDate, exposureCents, notes,
	)
	if err != nil {
		return nil, err
	}
	return s.LoadTriageReport(projectID)
}

// LoadTriageReport returns the triage report for a project, or (nil, nil)
// when none has been recorded yet.
func (s *Store) LoadTriageReport(projectID int64) (*TriageReport, error) {
	r := &TriageReport{}
	err := s.db.QueryRow(
		"SELECT id, project_id, decision, cutover_date, exposure_cents, notes, created_at, updated_at FROM triage_reports WHERE project_id = ?", projectID,
	).Scan(&r.ID, &r.ProjectID, &r.Decision, &r.CutoverDate, &r.ExposureCents, &r.Notes, &r.CreatedAt, &r.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return r, nil
}

// FormatADRsForPrompt returns all active ADRs formatted for injection into the AI context.
func (s *Store) FormatADRsForPrompt(projectID int64) string {
	adrs, err := s.GetActiveADRs(projectID)
	if err != nil || len(adrs) == 0 {
		return ""
	}

	result := "Architecture Decision Record (established decisions):\n\n"
	for _, adr := range adrs {
		result += fmt.Sprintf("## %s\n", adr.Decision)
		result += fmt.Sprintf("Context: %s\n", adr.Context)
		if adr.Options != "" {
			result += fmt.Sprintf("Options considered: %s\n", adr.Options)
		}
		result += fmt.Sprintf("Decision: %s\n", adr.Outcome)
		result += fmt.Sprintf("Status: %s\n\n", adr.Status)
	}
	return result
}

// CountUsers is the honest row count for tests that assert "one account,
// not two".
func (s *Store) CountUsers() (int, error) {
	var n int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM users").Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// ── Notifications (E) ───────────────────────────────────────────────────────

// RecordNotification writes the in-app history row. It is called before any
// push attempt so the surface is honest even when OneSignal is dark.
func (s *Store) RecordNotification(userID int64, kind, title, body string) error {
	_, err := s.db.Exec(
		"INSERT INTO notifications (user_id, kind, title, body) VALUES (?, ?, ?, ?)",
		userID, kind, title, body)
	return err
}

// Notification is one in-app history row.
type Notification struct {
	ID     int64   `json:"id"`
	Kind   string  `json:"kind"`
	Title  string  `json:"title"`
	Body   string  `json:"body"`
	SentAt string  `json:"sent_at"`
	ReadAt *string `json:"read_at"`
}

// ListNotifications returns the user's most recent notifications, newest
// first, bounded.
func (s *Store) ListNotifications(userID int64, limit int) ([]Notification, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := s.db.Query(
		`SELECT id, kind, title, body, sent_at, read_at FROM notifications
		 WHERE user_id = ? ORDER BY id DESC LIMIT ?`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Notification
	for rows.Next() {
		var n Notification
		if err := rows.Scan(&n.ID, &n.Kind, &n.Title, &n.Body, &n.SentAt, &n.ReadAt); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// MarkNotificationRead stamps read_at once.
func (s *Store) MarkNotificationRead(userID, id int64) error {
	_, err := s.db.Exec(
		"UPDATE notifications SET read_at = CURRENT_TIMESTAMP WHERE id = ? AND user_id = ? AND read_at IS NULL",
		id, userID)
	return err
}

// GetOneSignalID resolves the push target for a user (” when the user has
// not registered a device).
func (s *Store) GetOneSignalID(userID int64) (string, error) {
	var id string
	err := s.db.QueryRow("SELECT onesignal_id FROM users WHERE id = ?", userID).Scan(&id)
	if err != nil {
		return "", err
	}
	return id, nil
}

// SetOneSignalID stores the device subscription id for a user.
func (s *Store) SetOneSignalID(userID int64, extID string) error {
	_, err := s.db.Exec("UPDATE users SET onesignal_id = ? WHERE id = ?", extID, userID)
	return err
}
