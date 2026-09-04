package store

// Release export persistence: per-project signing keystores and async export
// jobs. Keystore FILES live on disk; only paths, aliases, fingerprints, and
// encrypted passwords live here. Passwords arrive already encrypted — this
// layer never sees plaintext, mirroring the git-credential pattern.

type KeystoreRecord struct {
	ID          int64  `json:"id"`
	ProjectID   int64  `json:"project_id"`
	Path        string `json:"-"`
	Alias       string `json:"alias"`
	Fingerprint string `json:"fingerprint"`
	EncPassword string `json:"-"`
}

type ExportJob struct {
	ID           int64  `json:"id"`
	ProjectID    int64  `json:"project_id"`
	Status       string `json:"status"`
	ArtifactPath string `json:"artifact_path,omitempty"`
	ManifestPath string `json:"manifest_path,omitempty"`
	Error        string `json:"error,omitempty"`
}

func (s *Store) GetKeystore(projectID int64) (*KeystoreRecord, error) {
	var k KeystoreRecord
	err := s.db.QueryRow(
		"SELECT id, project_id, path, alias, fingerprint, enc_password FROM project_keystores WHERE project_id = ?",
		projectID,
	).Scan(&k.ID, &k.ProjectID, &k.Path, &k.Alias, &k.Fingerprint, &k.EncPassword)
	if err != nil {
		return nil, err
	}
	return &k, nil
}

func (s *Store) SaveKeystore(projectID int64, path, alias, fingerprint, encPassword string) (*KeystoreRecord, error) {
	res, err := s.db.Exec(
		`INSERT INTO project_keystores (project_id, path, alias, fingerprint, enc_password)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(project_id) DO UPDATE SET path=excluded.path, alias=excluded.alias,
		 fingerprint=excluded.fingerprint, enc_password=excluded.enc_password`,
		projectID, path, alias, fingerprint, encPassword,
	)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &KeystoreRecord{ID: id, ProjectID: projectID, Path: path, Alias: alias, Fingerprint: fingerprint, EncPassword: encPassword}, nil
}

func (s *Store) CreateExportJob(projectID int64) (*ExportJob, error) {
	res, err := s.db.Exec(
		"INSERT INTO export_jobs (project_id, status) VALUES (?, 'queued')",
		projectID,
	)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &ExportJob{ID: id, ProjectID: projectID, Status: "queued"}, nil
}

func (s *Store) GetExportJob(id int64) (*ExportJob, error) {
	var j ExportJob
	err := s.db.QueryRow(
		"SELECT id, project_id, status, artifact_path, manifest_path, error FROM export_jobs WHERE id = ?",
		id,
	).Scan(&j.ID, &j.ProjectID, &j.Status, &j.ArtifactPath, &j.ManifestPath, &j.Error)
	if err != nil {
		return nil, err
	}
	return &j, nil
}

func (s *Store) UpdateExportJob(id int64, status, artifactPath, manifestPath, errMsg string) error {
	_, err := s.db.Exec(
		"UPDATE export_jobs SET status = ?, artifact_path = ?, manifest_path = ?, error = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?",
		status, artifactPath, manifestPath, errMsg, id,
	)
	return err
}

// FailStaleExportJobs marks jobs left queued/running by a dead process as
// failed. Called once at boot; a job can only finish inside the process that
// started its gradle run.
func (s *Store) FailStaleExportJobs() (int64, error) {
	res, err := s.db.Exec(
		"UPDATE export_jobs SET status = 'failed', error = 'server restarted while this export was running; start a new export to retry', updated_at = CURRENT_TIMESTAMP WHERE status IN ('queued', 'running')",
	)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

type Asset struct {
	ID        int64  `json:"id"`
	ProjectID int64  `json:"project_id"`
	Kind      string `json:"kind"`
	Path      string `json:"path"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
}

func (s *Store) AddAsset(projectID int64, kind, path string, width, height int) (*Asset, error) {
	res, err := s.db.Exec(
		"INSERT INTO project_assets (project_id, kind, path, width, height) VALUES (?, ?, ?, ?, ?)",
		projectID, kind, path, width, height,
	)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &Asset{ID: id, ProjectID: projectID, Kind: kind, Path: path, Width: width, Height: height}, nil
}

func (s *Store) ListAssets(projectID int64) ([]Asset, error) {
	rows, err := s.db.Query(
		"SELECT id, project_id, kind, path, width, height FROM project_assets WHERE project_id = ? ORDER BY id ASC",
		projectID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Asset
	for rows.Next() {
		var a Asset
		if err := rows.Scan(&a.ID, &a.ProjectID, &a.Kind, &a.Path, &a.Width, &a.Height); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}
