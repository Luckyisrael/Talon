package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"
)

// ErrDuplicateEmail is returned by CreateUser when the address is already taken,
// so the handler can tell that apart from a database failure.
var ErrDuplicateEmail = errors.New("email already registered")

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
	Title string `json:"title,omitempty"`
	Response  string `json:"response,omitempty"`
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
		`CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			email TEXT UNIQUE NOT NULL,
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
		// DV4: the idle reaper's clock. Fresh sessions default to their
		// start time — a session never touched starts idle.
		`ALTER TABLE sessions ADD COLUMN last_active_at DATETIME DEFAULT CURRENT_TIMESTAMP`,
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
			entry_type TEXT NOT NULL,
			content TEXT NOT NULL,
			metadata TEXT DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (project_id) REFERENCES projects(id)
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
			prompt_id INTEGER DEFAULT 0,
			seq INTEGER NOT NULL,
			role TEXT NOT NULL,
			blocks TEXT NOT NULL,
			input_tokens INTEGER DEFAULT 0,
			output_tokens INTEGER DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (project_id) REFERENCES projects(id)
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

	// Add uniqueness constraints (ignore errors if already exist)
	s.db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_projects_slug ON projects(slug) WHERE slug != ''")
	s.db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_projects_android_package ON projects(android_package) WHERE android_package != ''")
	s.db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_projects_ios_bundle_id ON projects(ios_bundle_id) WHERE ios_bundle_id != ''")

	// Migration: add container tracking columns to sessions
	s.db.Exec("ALTER TABLE sessions ADD COLUMN container_id TEXT DEFAULT ''")
	s.db.Exec("ALTER TABLE sessions ADD COLUMN container_name TEXT DEFAULT ''")
	s.db.Exec("ALTER TABLE sessions ADD COLUMN adb_port INTEGER DEFAULT 0")
	s.db.Exec("ALTER TABLE sessions ADD COLUMN container_state TEXT DEFAULT ''")

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

func (s *Store) GetUserByEmail(email string) (*User, error) {
	u := &User{}
	err := s.db.QueryRow("SELECT id, email, password_hash, tier, display_name, created_at FROM users WHERE email = ?", email).
		Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Tier, &u.DisplayName, &u.CreatedAt)
	if err != nil {
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

func (s *Store) DeleteProject(id int64) error {
	_, err := s.db.Exec("DELETE FROM projects WHERE id = ?", id)
	return err
}

func (s *Store) CreatePrompt(projectID int64, mode, prompt string) (*Prompt, error) {
	return s.CreateTitledPrompt(projectID, mode, prompt, "")
}

// CreateTitledPrompt stores a prompt with its session title. Empty title
// degrades to the truncated prompt — the list line is never blank.
func (s *Store) CreateTitledPrompt(projectID int64, mode, prompt, title string) (*Prompt, error) {
	if strings.TrimSpace(title) == "" {
		title = truncateTitleForList(prompt)
	}
	// New prompts start 'running': the turn is about to drive the agent loop, and
	// the terminal status is written when it ends. This overrides the column's
	// backfill default of 'completed', which only ever applies to rows that
	// predate the status column.
	res, err := s.db.Exec("INSERT INTO prompts (project_id, mode, prompt, status, title) VALUES (?, ?, ?, 'running', ?)", projectID, mode, prompt, title)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
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

const promptColumns = "id, project_id, mode, prompt, response, status, round, skipped_gates, updated_at, created_at, title"

// scanPrompt reads one prompt row in promptColumns order, mapping the nullable
// updated_at (existing rows predate it) onto the optional struct field.
func scanPrompt(row *sql.Row) (*Prompt, error) {
	p := &Prompt{}
	var updatedAt sql.NullTime
	var skipped sql.NullString
	if err := row.Scan(&p.ID, &p.ProjectID, &p.Mode, &p.Prompt, &p.Response, &p.Status, &p.Round, &skipped, &updatedAt, &p.CreatedAt, &p.Title); err != nil {
		return nil, err
	}
	if skipped.Valid && strings.TrimSpace(skipped.String) != "" {
		p.SkippedGatesJSON = skipped.String
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
	for _, id := range ids {
		if _, err := s.db.Exec("UPDATE prompts SET status = 'interrupted', updated_at = CURRENT_TIMESTAMP WHERE id = ?", id); err != nil {
			return ids, err
		}
	}
	return ids, nil
}

// ScopeDeviceBuild is the turn_approvals scope unlocking build_apk.
const ScopeDeviceBuild = "device_build"

// GrantApproval records a mid-turn user approval (scope "device_build").
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

// Project Memory methods

func (s *Store) AddMemory(projectID int64, entryType, content, metadata string) error {
	_, err := s.db.Exec("INSERT INTO project_memory (project_id, entry_type, content, metadata) VALUES (?, ?, ?, ?)", projectID, entryType, content, metadata)
	return err
}

func (s *Store) GetMemoryByType(projectID int64, entryType string) ([]ProjectMemory, error) {
	rows, err := s.db.Query("SELECT id, project_id, entry_type, content, metadata, created_at FROM project_memory WHERE project_id = ? AND entry_type = ? ORDER BY created_at ASC", projectID, entryType)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var memories []ProjectMemory
	for rows.Next() {
		var m ProjectMemory
		if err := rows.Scan(&m.ID, &m.ProjectID, &m.EntryType, &m.Content, &m.Metadata, &m.CreatedAt); err != nil {
			return nil, err
		}
		memories = append(memories, m)
	}
	return memories, nil
}

func (s *Store) GetAllMemory(projectID int64) ([]ProjectMemory, error) {
	rows, err := s.db.Query("SELECT id, project_id, entry_type, content, metadata, created_at FROM project_memory WHERE project_id = ? ORDER BY created_at ASC", projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var memories []ProjectMemory
	for rows.Next() {
		var m ProjectMemory
		if err := rows.Scan(&m.ID, &m.ProjectID, &m.EntryType, &m.Content, &m.Metadata, &m.CreatedAt); err != nil {
			return nil, err
		}
		memories = append(memories, m)
	}
	return memories, nil
}

func (s *Store) UpdateMemory(id int64, content, metadata string) error {
	_, err := s.db.Exec("UPDATE project_memory SET content = ?, metadata = ? WHERE id = ?", content, metadata, id)
	return err
}

func (s *Store) DeleteMemory(id int64) error {
	_, err := s.db.Exec("DELETE FROM project_memory WHERE id = ?", id)
	return err
}

func (s *Store) ClearMemory(projectID int64) error {
	_, err := s.db.Exec("DELETE FROM project_memory WHERE project_id = ?", projectID)
	return err
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
