package store

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"time"
)

type User struct {
	ID           int64     `json:"id"`
	Email        string    `json:"email"`
	PasswordHash string    `json:"-"`
	Tier         string    `json:"tier"`
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
	ID        int64     `json:"id"`
	ProjectID int64     `json:"project_id"`
	Mode      string    `json:"mode"`
	Prompt    string    `json:"prompt"`
	Response  string    `json:"response,omitempty"`
	CreatedAt time.Time `json:"created_at"`
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

type Store struct {
	db *sql.DB
}

func New(dbPath string) (*Store, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}

	// Configure for concurrent access
	db.SetMaxOpenConns(1) // SQLite only supports one writer
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	if err := db.Ping(); err != nil {
		return nil, err
	}

	// Enable WAL mode for better concurrent read performance
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		log.Printf("WARNING: could not enable WAL mode: %v", err)
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
	}

	for _, q := range queries {
		if _, err := s.db.Exec(q); err != nil {
			return err
		}
	}

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

	return nil
}

func (s *Store) CreateUser(email, passwordHash string) (*User, error) {
	res, err := s.db.Exec("INSERT INTO users (email, password_hash) VALUES (?, ?)", email, passwordHash)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetUserByID(id)
}

func (s *Store) GetUserByEmail(email string) (*User, error) {
	u := &User{}
	err := s.db.QueryRow("SELECT id, email, password_hash, tier, created_at FROM users WHERE email = ?", email).
		Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Tier, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	return u, nil
}

func (s *Store) GetUserByID(id int64) (*User, error) {
	u := &User{}
	err := s.db.QueryRow("SELECT id, email, password_hash, tier, created_at FROM users WHERE id = ?", id).
		Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Tier, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	return u, nil
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
	res, err := s.db.Exec("INSERT INTO prompts (project_id, mode, prompt) VALUES (?, ?, ?)", projectID, mode, prompt)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetPrompt(id)
}

func (s *Store) GetPrompt(id int64) (*Prompt, error) {
	p := &Prompt{}
	err := s.db.QueryRow("SELECT id, project_id, mode, prompt, response, created_at FROM prompts WHERE id = ?", id).
		Scan(&p.ID, &p.ProjectID, &p.Mode, &p.Prompt, &p.Response, &p.CreatedAt)
	if err != nil {
		return nil, err
	}
	return p, nil
}

func (s *Store) UpdatePromptResponse(id int64, response string) error {
	_, err := s.db.Exec("UPDATE prompts SET response = ? WHERE id = ?", response, id)
	return err
}

func (s *Store) CreateSession(projectID int64) (*Session, error) {
	res, err := s.db.Exec("INSERT INTO sessions (project_id) VALUES (?)", projectID)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetSession(id)
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
	err := s.db.QueryRow("SELECT id, user_id, prompts_used, device_minutes, period_start FROM usage WHERE user_id = ? ORDER BY period_start DESC LIMIT 1", userID).
		Scan(&u.ID, &u.UserID, &u.PromptsUsed, &u.DeviceMinutes, &u.PeriodStart)
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
