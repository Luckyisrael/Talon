package store

import (
	"database/sql"
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
	ID        int64     `json:"id"`
	UserID    int64     `json:"user_id"`
	Name      string    `json:"name"`
	PlanJSON  string    `json:"plan_json,omitempty"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
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
	ID        int64      `json:"id"`
	ProjectID int64      `json:"project_id"`
	State     string     `json:"state"`
	StartedAt time.Time  `json:"started_at"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
}

type Usage struct {
	ID            int64     `json:"id"`
	UserID        int64     `json:"user_id"`
	PromptsUsed   int       `json:"prompts_used"`
	DeviceMinutes float64   `json:"device_minutes"`
	PeriodStart   time.Time `json:"period_start"`
}

type Store struct {
	db *sql.DB
}

func New(dbPath string) (*Store, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}

	if err := db.Ping(); err != nil {
		return nil, err
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
	}

	for _, q := range queries {
		if _, err := s.db.Exec(q); err != nil {
			return err
		}
	}

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

func (s *Store) CreateProject(userID int64, name string) (*Project, error) {
	res, err := s.db.Exec("INSERT INTO projects (user_id, name) VALUES (?, ?)", userID, name)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetProject(id)
}

func (s *Store) GetProject(id int64) (*Project, error) {
	p := &Project{}
	err := s.db.QueryRow("SELECT id, user_id, name, plan_json, status, created_at FROM projects WHERE id = ?", id).
		Scan(&p.ID, &p.UserID, &p.Name, &p.PlanJSON, &p.Status, &p.CreatedAt)
	if err != nil {
		return nil, err
	}
	return p, nil
}

func (s *Store) ListProjects(userID int64) ([]Project, error) {
	rows, err := s.db.Query("SELECT id, user_id, name, plan_json, status, created_at FROM projects WHERE user_id = ? ORDER BY created_at DESC", userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var projects []Project
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.ID, &p.UserID, &p.Name, &p.PlanJSON, &p.Status, &p.CreatedAt); err != nil {
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
	err := s.db.QueryRow("SELECT id, project_id, state, started_at, ended_at FROM sessions WHERE id = ?", id).
		Scan(&sess.ID, &sess.ProjectID, &sess.State, &sess.StartedAt, &sess.EndedAt)
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
