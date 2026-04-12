package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/surfbot-io/surfbot-agent/internal/model"

	_ "modernc.org/sqlite"
)

var domainRegex = regexp.MustCompile(`^([a-zA-Z0-9-]+\.)+[a-zA-Z]{2,}$`)

const timeFormat = time.RFC3339

// AssetListOptions configures asset listing queries.
type AssetListOptions struct {
	TargetID    string
	Type        model.AssetType
	Status      model.AssetStatus
	Limit       int
	Offset      int
	NewOnly     bool
	Disappeared bool
}

// AssetChangeListOptions configures asset change listing queries.
type AssetChangeListOptions struct {
	TargetID     string
	ScanID       string
	ChangeType   string // appeared|disappeared|modified
	Significance string // critical|high|medium|low|info
	AssetType    string
	Limit        int
}

// FindingListOptions configures finding listing queries.
type FindingListOptions struct {
	AssetID    string
	ScanID     string
	TargetID   string
	Severity   model.Severity
	Status     model.FindingStatus
	SourceTool string
	Limit      int
	Offset     int
}

// Store defines the storage interface for all surfbot-agent entities.
type Store interface {
	CreateTarget(ctx context.Context, t *model.Target) error
	GetTarget(ctx context.Context, id string) (*model.Target, error)
	GetTargetByValue(ctx context.Context, value string) (*model.Target, error)
	ListTargets(ctx context.Context) ([]model.Target, error)
	DeleteTarget(ctx context.Context, id string) error

	CreateScan(ctx context.Context, s *model.Scan) error
	GetScan(ctx context.Context, id string) (*model.Scan, error)
	UpdateScan(ctx context.Context, s *model.Scan) error
	ListScans(ctx context.Context, targetID string, limit int) ([]model.Scan, error)

	UpsertAsset(ctx context.Context, a *model.Asset) error
	GetAsset(ctx context.Context, id string) (*model.Asset, error)
	ListAssets(ctx context.Context, opts AssetListOptions) ([]model.Asset, error)

	UpsertFinding(ctx context.Context, f *model.Finding) error
	GetFinding(ctx context.Context, id string) (*model.Finding, error)
	ListFindings(ctx context.Context, opts FindingListOptions) ([]model.Finding, error)
	UpdateFindingStatus(ctx context.Context, id string, status model.FindingStatus) error

	CreateToolRun(ctx context.Context, tr *model.ToolRun) error
	UpdateToolRun(ctx context.Context, tr *model.ToolRun) error
	ListToolRuns(ctx context.Context, scanID string) ([]model.ToolRun, error)

	CreateAssetChange(ctx context.Context, ac *model.AssetChange) error
	ListAssetChanges(ctx context.Context, opts AssetChangeListOptions) ([]model.AssetChange, error)
	UpdateAssetStatus(ctx context.Context, id string, status model.AssetStatus) error
	NormalizeAssetStatuses(ctx context.Context, targetID string) error
	UpdateFindingResolvedAt(ctx context.Context, id string, resolvedAt *time.Time) error

	GetMeta(ctx context.Context, key string) (string, error)
	SetMeta(ctx context.Context, key, value string) error

	CountTargets(ctx context.Context) (int, error)
	CountScans(ctx context.Context) (int, error)
	CountFindings(ctx context.Context) (int, error)
	CountAssets(ctx context.Context) (int, error)
	CountFindingsFiltered(ctx context.Context, opts FindingListOptions) (int, error)
	CountAssetsFiltered(ctx context.Context, opts AssetListOptions) (int, error)
	CountFindingsBySeverity(ctx context.Context) (map[model.Severity]int, error)
	CountAssetsByType(ctx context.Context) (map[model.AssetType]int, error)
	CountFindingsByTargetID(ctx context.Context, targetID string) (int, error)
	CountAssetsByTargetID(ctx context.Context, targetID string) (int, error)
	CountScansByTargetID(ctx context.Context, targetID string) (int, error)
	CountFindingsByAssetIDs(ctx context.Context) (map[string]int, error)
	LastScan(ctx context.Context) (*model.Scan, error)

	Close() error
}

// SQLiteStore implements Store using modernc.org/sqlite.
type SQLiteStore struct {
	db     *sql.DB
	dbPath string
}

// NewSQLiteStore creates or opens the SQLite database at the given path and runs migrations.
func NewSQLiteStore(dbPath string) (*SQLiteStore, error) {
	if dbPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("getting home dir: %w", err)
		}
		dbPath = filepath.Join(home, ".surfbot", "surfbot.db")
	}

	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("creating db directory: %w", err)
	}

	db, err := sql.Open("sqlite", dbPath+"?_pragma=foreign_keys(1)&_pragma=journal_mode(wal)")
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("pinging database: %w", err)
	}

	s := &SQLiteStore{db: db, dbPath: dbPath}
	if err := s.runMigrations(); err != nil {
		db.Close()
		return nil, fmt.Errorf("running migrations: %w", err)
	}

	return s, nil
}

func (s *SQLiteStore) runMigrations() error {
	migration, err := migrationsFS.ReadFile("migrations/001_init.sql")
	if err != nil {
		return fmt.Errorf("reading migration 001: %w", err)
	}
	if _, err = s.db.Exec(string(migration)); err != nil {
		return fmt.Errorf("executing migration 001: %w", err)
	}

	migration002, err := migrationsFS.ReadFile("migrations/002_asset_changes.sql")
	if err != nil {
		return fmt.Errorf("reading migration 002: %w", err)
	}
	if _, err = s.db.Exec(string(migration002)); err != nil {
		return fmt.Errorf("executing migration 002: %w", err)
	}

	return nil
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

// DBPath returns the path to the database file.
func (s *SQLiteStore) DBPath() string {
	return s.dbPath
}

// --- Targets ---

func (s *SQLiteStore) CreateTarget(ctx context.Context, t *model.Target) error {
	if t.ID == "" {
		t.ID = uuid.New().String()
	}

	if t.Type == "" {
		detected, err := detectTargetType(t.Value)
		if err != nil {
			return err
		}
		t.Type = detected
	} else {
		if err := validateTargetValue(t.Value, t.Type); err != nil {
			return err
		}
	}

	if t.Scope == "" {
		t.Scope = model.TargetScopeExternal
	}

	now := time.Now().UTC()
	t.Enabled = true
	t.CreatedAt = now
	t.UpdatedAt = now

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO targets (id, value, type, scope, enabled, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.Value, string(t.Type), string(t.Scope), boolToInt(t.Enabled),
		t.CreatedAt.Format(timeFormat), t.UpdatedAt.Format(timeFormat),
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return fmt.Errorf("%w: %s", ErrAlreadyExists, t.Value)
		}
		return fmt.Errorf("inserting target: %w", err)
	}
	return nil
}

func (s *SQLiteStore) GetTarget(ctx context.Context, id string) (*model.Target, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, value, type, scope, enabled, last_scan_id, last_scan_at, created_at, updated_at
		 FROM targets WHERE id = ?`, id)
	return scanTarget(row)
}

func (s *SQLiteStore) GetTargetByValue(ctx context.Context, value string) (*model.Target, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, value, type, scope, enabled, last_scan_id, last_scan_at, created_at, updated_at
		 FROM targets WHERE value = ?`, value)
	return scanTarget(row)
}

func (s *SQLiteStore) ListTargets(ctx context.Context) ([]model.Target, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, value, type, scope, enabled, last_scan_id, last_scan_at, created_at, updated_at
		 FROM targets ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("listing targets: %w", err)
	}
	defer rows.Close()

	targets := make([]model.Target, 0)
	for rows.Next() {
		t, err := scanTargetRow(rows)
		if err != nil {
			return nil, err
		}
		targets = append(targets, *t)
	}
	return targets, rows.Err()
}

func (s *SQLiteStore) DeleteTarget(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM targets WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("deleting target: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// --- Scans ---

func (s *SQLiteStore) CreateScan(ctx context.Context, sc *model.Scan) error {
	if sc.ID == "" {
		sc.ID = uuid.New().String()
	}
	now := time.Now().UTC()
	sc.CreatedAt = now
	sc.UpdatedAt = now

	statsJSON, err := json.Marshal(sc.Stats)
	if err != nil {
		return fmt.Errorf("marshaling scan stats: %w", err)
	}

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO scans (id, target_id, type, status, phase, progress, stats, started_at, finished_at, error, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sc.ID, sc.TargetID, string(sc.Type), string(sc.Status), sc.Phase, sc.Progress,
		string(statsJSON), timePtr(sc.StartedAt), timePtr(sc.FinishedAt),
		sc.Error, sc.CreatedAt.Format(timeFormat), sc.UpdatedAt.Format(timeFormat),
	)
	if err != nil {
		return fmt.Errorf("inserting scan: %w", err)
	}
	return nil
}

func (s *SQLiteStore) GetScan(ctx context.Context, id string) (*model.Scan, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, target_id, type, status, phase, progress, stats, started_at, finished_at, error, created_at, updated_at
		 FROM scans WHERE id = ?`, id)

	var sc model.Scan
	var statsJSON string
	var startedAt, finishedAt, createdAt, updatedAt sql.NullString

	err := row.Scan(&sc.ID, &sc.TargetID, &sc.Type, &sc.Status, &sc.Phase, &sc.Progress,
		&statsJSON, &startedAt, &finishedAt, &sc.Error, &createdAt, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scanning scan row: %w", err)
	}

	if err := json.Unmarshal([]byte(statsJSON), &sc.Stats); err != nil {
		return nil, fmt.Errorf("unmarshaling scan stats: %w", err)
	}
	sc.StartedAt = parseTimePtr(startedAt)
	sc.FinishedAt = parseTimePtr(finishedAt)
	sc.CreatedAt = parseTime(createdAt)
	sc.UpdatedAt = parseTime(updatedAt)

	return &sc, nil
}

func (s *SQLiteStore) UpdateScan(ctx context.Context, sc *model.Scan) error {
	sc.UpdatedAt = time.Now().UTC()
	statsJSON, err := json.Marshal(sc.Stats)
	if err != nil {
		return fmt.Errorf("marshaling scan stats: %w", err)
	}

	_, err = s.db.ExecContext(ctx,
		`UPDATE scans SET status=?, phase=?, progress=?, stats=?, started_at=?, finished_at=?, error=?, updated_at=?
		 WHERE id=?`,
		string(sc.Status), sc.Phase, sc.Progress, string(statsJSON),
		timePtr(sc.StartedAt), timePtr(sc.FinishedAt), sc.Error,
		sc.UpdatedAt.Format(timeFormat), sc.ID,
	)
	if err != nil {
		return fmt.Errorf("updating scan: %w", err)
	}
	return nil
}

func (s *SQLiteStore) ListScans(ctx context.Context, targetID string, limit int) ([]model.Scan, error) {
	if limit <= 0 {
		limit = 20
	}

	query := `SELECT id, target_id, type, status, phase, progress, stats, started_at, finished_at, error, created_at, updated_at FROM scans`
	args := []interface{}{}

	if targetID != "" {
		query += ` WHERE target_id = ?`
		args = append(args, targetID)
	}
	query += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing scans: %w", err)
	}
	defer rows.Close()

	scans := make([]model.Scan, 0)
	for rows.Next() {
		var sc model.Scan
		var statsJSON string
		var startedAt, finishedAt, createdAt, updatedAt sql.NullString

		if err := rows.Scan(&sc.ID, &sc.TargetID, &sc.Type, &sc.Status, &sc.Phase, &sc.Progress,
			&statsJSON, &startedAt, &finishedAt, &sc.Error, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scanning scan row: %w", err)
		}
		if err := json.Unmarshal([]byte(statsJSON), &sc.Stats); err != nil {
			return nil, fmt.Errorf("unmarshaling scan stats: %w", err)
		}
		sc.StartedAt = parseTimePtr(startedAt)
		sc.FinishedAt = parseTimePtr(finishedAt)
		sc.CreatedAt = parseTime(createdAt)
		sc.UpdatedAt = parseTime(updatedAt)
		scans = append(scans, sc)
	}
	return scans, rows.Err()
}

// --- Assets ---

func (s *SQLiteStore) UpsertAsset(ctx context.Context, a *model.Asset) error {
	if a.ID == "" {
		a.ID = uuid.New().String()
	}
	now := time.Now().UTC()
	if a.FirstSeen.IsZero() {
		a.FirstSeen = now
	}
	a.LastSeen = now
	if a.CreatedAt.IsZero() {
		a.CreatedAt = now
	}
	a.UpdatedAt = now

	if a.Tags == nil {
		a.Tags = []string{}
	}
	if a.Metadata == nil {
		a.Metadata = map[string]interface{}{}
	}

	tagsJSON, err := json.Marshal(a.Tags)
	if err != nil {
		return fmt.Errorf("marshaling tags: %w", err)
	}
	metaJSON, err := json.Marshal(a.Metadata)
	if err != nil {
		return fmt.Errorf("marshaling metadata: %w", err)
	}

	parentID := sqlNullString(a.ParentID)

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO assets (id, target_id, parent_id, type, value, status, tags, metadata, first_seen, last_seen, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(target_id, value) DO UPDATE SET
		   last_seen=excluded.last_seen, status=excluded.status, metadata=excluded.metadata, updated_at=excluded.updated_at`,
		a.ID, a.TargetID, parentID, string(a.Type), a.Value, string(a.Status),
		string(tagsJSON), string(metaJSON),
		a.FirstSeen.Format(timeFormat), a.LastSeen.Format(timeFormat),
		a.CreatedAt.Format(timeFormat), a.UpdatedAt.Format(timeFormat),
	)
	if err != nil {
		return fmt.Errorf("upserting asset: %w", err)
	}
	return nil
}

func (s *SQLiteStore) ListAssets(ctx context.Context, opts AssetListOptions) ([]model.Asset, error) {
	if opts.Limit <= 0 {
		opts.Limit = 50
	}

	query := `SELECT id, target_id, parent_id, type, value, status, tags, metadata, first_seen, last_seen, created_at, updated_at FROM assets`
	where := []string{}
	args := []interface{}{}

	if opts.TargetID != "" {
		where = append(where, "target_id = ?")
		args = append(args, opts.TargetID)
	}
	if opts.Type != "" {
		where = append(where, "type = ?")
		args = append(args, string(opts.Type))
	}
	if opts.Status != "" {
		where = append(where, "status = ?")
		args = append(args, string(opts.Status))
	}
	if opts.NewOnly {
		where = append(where, "status = 'new'")
	}
	if opts.Disappeared {
		where = append(where, "status = 'disappeared'")
	}

	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY first_seen DESC LIMIT ? OFFSET ?"
	args = append(args, opts.Limit, opts.Offset)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing assets: %w", err)
	}
	defer rows.Close()

	assets := make([]model.Asset, 0)
	for rows.Next() {
		var a model.Asset
		var parentID sql.NullString
		var tagsJSON, metaJSON string
		var firstSeen, lastSeen, createdAt, updatedAt sql.NullString

		if err := rows.Scan(&a.ID, &a.TargetID, &parentID, &a.Type, &a.Value, &a.Status,
			&tagsJSON, &metaJSON, &firstSeen, &lastSeen, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scanning asset row: %w", err)
		}

		a.ParentID = parentID.String
		if err := json.Unmarshal([]byte(tagsJSON), &a.Tags); err != nil {
			return nil, fmt.Errorf("unmarshaling tags: %w", err)
		}
		if err := json.Unmarshal([]byte(metaJSON), &a.Metadata); err != nil {
			return nil, fmt.Errorf("unmarshaling metadata: %w", err)
		}
		a.FirstSeen = parseTime(firstSeen)
		a.LastSeen = parseTime(lastSeen)
		a.CreatedAt = parseTime(createdAt)
		a.UpdatedAt = parseTime(updatedAt)
		assets = append(assets, a)
	}
	return assets, rows.Err()
}

// --- Findings ---

func (s *SQLiteStore) UpsertFinding(ctx context.Context, f *model.Finding) error {
	if f.ID == "" {
		f.ID = uuid.New().String()
	}
	now := time.Now().UTC()
	if f.FirstSeen.IsZero() {
		f.FirstSeen = now
	}
	f.LastSeen = now
	if f.CreatedAt.IsZero() {
		f.CreatedAt = now
	}
	f.UpdatedAt = now

	if f.References == nil {
		f.References = []string{}
	}
	refsJSON, err := json.Marshal(f.References)
	if err != nil {
		return fmt.Errorf("marshaling references: %w", err)
	}

	scanID := sqlNullString(f.ScanID)

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO findings (id, asset_id, scan_id, template_id, template_name, severity, title, description,
		   "references", remediation, evidence, cvss, cve, status, source_tool, confidence,
		   first_seen, last_seen, resolved_at, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(asset_id, template_id, source_tool) DO UPDATE SET
		   last_seen=excluded.last_seen, severity=excluded.severity, evidence=excluded.evidence, updated_at=excluded.updated_at`,
		f.ID, f.AssetID, scanID, f.TemplateID, f.TemplateName,
		string(f.Severity), f.Title, f.Description, string(refsJSON),
		f.Remediation, f.Evidence, f.CVSS, sqlNullString(f.CVE),
		string(f.Status), f.SourceTool, f.Confidence,
		f.FirstSeen.Format(timeFormat), f.LastSeen.Format(timeFormat),
		timePtrVal(f.ResolvedAt),
		f.CreatedAt.Format(timeFormat), f.UpdatedAt.Format(timeFormat),
	)
	if err != nil {
		return fmt.Errorf("upserting finding: %w", err)
	}
	return nil
}

func (s *SQLiteStore) ListFindings(ctx context.Context, opts FindingListOptions) ([]model.Finding, error) {
	if opts.Limit <= 0 {
		opts.Limit = 50
	}

	query := `SELECT id, asset_id, scan_id, template_id, template_name, severity, title, description,
	   "references", remediation, evidence, cvss, cve, status, source_tool, confidence,
	   first_seen, last_seen, resolved_at, created_at, updated_at
	   FROM findings`
	where := []string{}
	args := []interface{}{}

	if opts.AssetID != "" {
		where = append(where, "asset_id = ?")
		args = append(args, opts.AssetID)
	}
	if opts.TargetID != "" {
		where = append(where, "asset_id IN (SELECT id FROM assets WHERE target_id = ?)")
		args = append(args, opts.TargetID)
	}
	if opts.ScanID != "" {
		where = append(where, "scan_id = ?")
		args = append(args, opts.ScanID)
	}
	if opts.Severity != "" {
		where = append(where, "severity = ?")
		args = append(args, string(opts.Severity))
	}
	if opts.Status != "" {
		where = append(where, "status = ?")
		args = append(args, string(opts.Status))
	}
	if opts.SourceTool != "" {
		where = append(where, "source_tool = ?")
		args = append(args, opts.SourceTool)
	}

	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += ` ORDER BY CASE severity
		WHEN 'critical' THEN 0 WHEN 'high' THEN 1 WHEN 'medium' THEN 2
		WHEN 'low' THEN 3 WHEN 'info' THEN 4 END, last_seen DESC LIMIT ? OFFSET ?`
	args = append(args, opts.Limit, opts.Offset)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing findings: %w", err)
	}
	defer rows.Close()

	findings := make([]model.Finding, 0)
	for rows.Next() {
		var f model.Finding
		var scanID, cve, resolvedAt sql.NullString
		var refsJSON string
		var firstSeen, lastSeen, createdAt, updatedAt sql.NullString
		var cvss sql.NullFloat64

		if err := rows.Scan(&f.ID, &f.AssetID, &scanID, &f.TemplateID, &f.TemplateName,
			&f.Severity, &f.Title, &f.Description, &refsJSON, &f.Remediation,
			&f.Evidence, &cvss, &cve, &f.Status, &f.SourceTool, &f.Confidence,
			&firstSeen, &lastSeen, &resolvedAt, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scanning finding row: %w", err)
		}

		f.ScanID = scanID.String
		f.CVE = cve.String
		if cvss.Valid {
			f.CVSS = cvss.Float64
		}
		if err := json.Unmarshal([]byte(refsJSON), &f.References); err != nil {
			return nil, fmt.Errorf("unmarshaling references: %w", err)
		}
		f.FirstSeen = parseTime(firstSeen)
		f.LastSeen = parseTime(lastSeen)
		f.ResolvedAt = parseTimePtr(resolvedAt)
		f.CreatedAt = parseTime(createdAt)
		f.UpdatedAt = parseTime(updatedAt)
		findings = append(findings, f)
	}
	return findings, rows.Err()
}

func (s *SQLiteStore) UpdateFindingStatus(ctx context.Context, id string, status model.FindingStatus) error {
	now := time.Now().UTC().Format(timeFormat)
	var resolvedAt interface{}
	if status == model.FindingStatusResolved {
		resolvedAt = now
	}

	res, err := s.db.ExecContext(ctx,
		`UPDATE findings SET status=?, resolved_at=COALESCE(?, resolved_at), updated_at=? WHERE id=?`,
		string(status), resolvedAt, now, id)
	if err != nil {
		return fmt.Errorf("updating finding status: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// --- Tool Runs ---

func (s *SQLiteStore) CreateToolRun(ctx context.Context, tr *model.ToolRun) error {
	if tr.ID == "" {
		tr.ID = uuid.New().String()
	}
	now := time.Now().UTC()
	tr.CreatedAt = now
	tr.UpdatedAt = now

	if tr.Config == nil {
		tr.Config = map[string]interface{}{}
	}
	configJSON, err := json.Marshal(tr.Config)
	if err != nil {
		return fmt.Errorf("marshaling tool run config: %w", err)
	}

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO tool_runs (id, scan_id, tool_name, phase, status, started_at, finished_at,
		   duration_ms, targets_count, findings_count, output_summary, error_message, exit_code, config, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		tr.ID, tr.ScanID, tr.ToolName, tr.Phase, string(tr.Status),
		tr.StartedAt.Format(timeFormat), timePtrVal(tr.FinishedAt),
		tr.DurationMs, tr.TargetsCount, tr.FindingsCount, tr.OutputSummary, tr.ErrorMessage, tr.ExitCode,
		string(configJSON), tr.CreatedAt.Format(timeFormat), tr.UpdatedAt.Format(timeFormat),
	)
	if err != nil {
		return fmt.Errorf("inserting tool run: %w", err)
	}
	return nil
}

func (s *SQLiteStore) UpdateToolRun(ctx context.Context, tr *model.ToolRun) error {
	tr.UpdatedAt = time.Now().UTC()

	configJSON, err := json.Marshal(tr.Config)
	if err != nil {
		return fmt.Errorf("marshaling tool run config: %w", err)
	}

	_, err = s.db.ExecContext(ctx,
		`UPDATE tool_runs SET status=?, finished_at=?, duration_ms=?, targets_count=?, findings_count=?,
		   output_summary=?, error_message=?, exit_code=?, config=?, updated_at=?
		 WHERE id=?`,
		string(tr.Status), timePtrVal(tr.FinishedAt), tr.DurationMs,
		tr.TargetsCount, tr.FindingsCount, tr.OutputSummary, tr.ErrorMessage, tr.ExitCode,
		string(configJSON), tr.UpdatedAt.Format(timeFormat), tr.ID,
	)
	if err != nil {
		return fmt.Errorf("updating tool run: %w", err)
	}
	return nil
}

// --- Asset Changes ---

func (s *SQLiteStore) CreateAssetChange(ctx context.Context, ac *model.AssetChange) error {
	if ac.ID == "" {
		ac.ID = uuid.New().String()
	}
	if ac.CreatedAt.IsZero() {
		ac.CreatedAt = time.Now().UTC()
	}
	if ac.PreviousMeta == nil {
		ac.PreviousMeta = map[string]any{}
	}
	if ac.CurrentMeta == nil {
		ac.CurrentMeta = map[string]any{}
	}

	prevJSON, err := json.Marshal(ac.PreviousMeta)
	if err != nil {
		return fmt.Errorf("marshaling previous_meta: %w", err)
	}
	currJSON, err := json.Marshal(ac.CurrentMeta)
	if err != nil {
		return fmt.Errorf("marshaling current_meta: %w", err)
	}

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO asset_changes (id, target_id, scan_id, asset_id, change_type, significance,
		   asset_type, asset_value, previous_meta, current_meta, summary, baseline, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		ac.ID, ac.TargetID, ac.ScanID, sqlNullString(ac.AssetID),
		string(ac.ChangeType), string(ac.Significance),
		ac.AssetType, ac.AssetValue,
		string(prevJSON), string(currJSON),
		ac.Summary, boolToInt(ac.Baseline),
		ac.CreatedAt.Format(timeFormat),
	)
	if err != nil {
		return fmt.Errorf("inserting asset change: %w", err)
	}
	return nil
}

func (s *SQLiteStore) ListAssetChanges(ctx context.Context, opts AssetChangeListOptions) ([]model.AssetChange, error) {
	if opts.Limit <= 0 {
		opts.Limit = 100
	}

	query := `SELECT id, target_id, scan_id, asset_id, change_type, significance,
	   asset_type, asset_value, previous_meta, current_meta, summary, baseline, created_at
	   FROM asset_changes`
	where := []string{}
	args := []interface{}{}

	if opts.TargetID != "" {
		where = append(where, "target_id = ?")
		args = append(args, opts.TargetID)
	}
	if opts.ScanID != "" {
		where = append(where, "scan_id = ?")
		args = append(args, opts.ScanID)
	}
	if opts.ChangeType != "" {
		where = append(where, "change_type = ?")
		args = append(args, opts.ChangeType)
	}
	if opts.Significance != "" {
		where = append(where, "significance = ?")
		args = append(args, opts.Significance)
	}
	if opts.AssetType != "" {
		where = append(where, "asset_type = ?")
		args = append(args, opts.AssetType)
	}

	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += ` ORDER BY CASE significance
		WHEN 'critical' THEN 0 WHEN 'high' THEN 1 WHEN 'medium' THEN 2
		WHEN 'low' THEN 3 WHEN 'info' THEN 4 WHEN 'noise' THEN 5 END, created_at DESC LIMIT ?`
	args = append(args, opts.Limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing asset changes: %w", err)
	}
	defer rows.Close()

	changes := make([]model.AssetChange, 0)
	for rows.Next() {
		var ac model.AssetChange
		var assetID sql.NullString
		var prevJSON, currJSON string
		var baseline int
		var createdAt sql.NullString

		if err := rows.Scan(&ac.ID, &ac.TargetID, &ac.ScanID, &assetID,
			&ac.ChangeType, &ac.Significance,
			&ac.AssetType, &ac.AssetValue,
			&prevJSON, &currJSON,
			&ac.Summary, &baseline, &createdAt); err != nil {
			return nil, fmt.Errorf("scanning asset change row: %w", err)
		}

		ac.AssetID = assetID.String
		ac.Baseline = baseline != 0
		ac.CreatedAt = parseTime(createdAt)

		if err := json.Unmarshal([]byte(prevJSON), &ac.PreviousMeta); err != nil {
			return nil, fmt.Errorf("unmarshaling previous_meta: %w", err)
		}
		if err := json.Unmarshal([]byte(currJSON), &ac.CurrentMeta); err != nil {
			return nil, fmt.Errorf("unmarshaling current_meta: %w", err)
		}
		changes = append(changes, ac)
	}
	return changes, rows.Err()
}

func (s *SQLiteStore) UpdateAssetStatus(ctx context.Context, id string, status model.AssetStatus) error {
	now := time.Now().UTC().Format(timeFormat)
	res, err := s.db.ExecContext(ctx,
		`UPDATE assets SET status=?, updated_at=? WHERE id=?`,
		string(status), now, id)
	if err != nil {
		return fmt.Errorf("updating asset status: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *SQLiteStore) NormalizeAssetStatuses(ctx context.Context, targetID string) error {
	now := time.Now().UTC().Format(timeFormat)
	_, err := s.db.ExecContext(ctx,
		`UPDATE assets SET status = 'active', updated_at = ? WHERE target_id = ? AND status = 'new'`,
		now, targetID)
	if err != nil {
		return fmt.Errorf("normalizing asset statuses: %w", err)
	}
	return nil
}

func (s *SQLiteStore) UpdateFindingResolvedAt(ctx context.Context, id string, resolvedAt *time.Time) error {
	now := time.Now().UTC().Format(timeFormat)
	res, err := s.db.ExecContext(ctx,
		`UPDATE findings SET resolved_at=?, updated_at=? WHERE id=?`,
		timePtrVal(resolvedAt), now, id)
	if err != nil {
		return fmt.Errorf("updating finding resolved_at: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// --- Meta ---

func (s *SQLiteStore) GetMeta(ctx context.Context, key string) (string, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM agent_meta WHERE key = ?`, key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("getting meta: %w", err)
	}
	return value, nil
}

func (s *SQLiteStore) SetMeta(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO agent_meta (key, value) VALUES (?, ?)`, key, value)
	if err != nil {
		return fmt.Errorf("setting meta: %w", err)
	}
	return nil
}

// --- Counts ---

func (s *SQLiteStore) CountTargets(ctx context.Context) (int, error) {
	return s.count(ctx, "targets")
}

func (s *SQLiteStore) CountScans(ctx context.Context) (int, error) {
	return s.count(ctx, "scans")
}

func (s *SQLiteStore) CountFindings(ctx context.Context) (int, error) {
	return s.count(ctx, "findings")
}

func (s *SQLiteStore) CountAssets(ctx context.Context) (int, error) {
	return s.count(ctx, "assets")
}

func (s *SQLiteStore) count(ctx context.Context, table string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&n)
	return n, err
}

func (s *SQLiteStore) CountFindingsFiltered(ctx context.Context, opts FindingListOptions) (int, error) {
	query := `SELECT COUNT(*) FROM findings`
	where := []string{}
	args := []interface{}{}

	if opts.AssetID != "" {
		where = append(where, "asset_id = ?")
		args = append(args, opts.AssetID)
	}
	if opts.TargetID != "" {
		where = append(where, "asset_id IN (SELECT id FROM assets WHERE target_id = ?)")
		args = append(args, opts.TargetID)
	}
	if opts.ScanID != "" {
		where = append(where, "scan_id = ?")
		args = append(args, opts.ScanID)
	}
	if opts.Severity != "" {
		where = append(where, "severity = ?")
		args = append(args, string(opts.Severity))
	}
	if opts.Status != "" {
		where = append(where, "status = ?")
		args = append(args, string(opts.Status))
	}
	if opts.SourceTool != "" {
		where = append(where, "source_tool = ?")
		args = append(args, opts.SourceTool)
	}
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}

	var n int
	err := s.db.QueryRowContext(ctx, query, args...).Scan(&n)
	return n, err
}

func (s *SQLiteStore) CountAssetsFiltered(ctx context.Context, opts AssetListOptions) (int, error) {
	query := `SELECT COUNT(*) FROM assets`
	where := []string{}
	args := []interface{}{}

	if opts.TargetID != "" {
		where = append(where, "target_id = ?")
		args = append(args, opts.TargetID)
	}
	if opts.Type != "" {
		where = append(where, "type = ?")
		args = append(args, string(opts.Type))
	}
	if opts.Status != "" {
		where = append(where, "status = ?")
		args = append(args, string(opts.Status))
	}
	if opts.NewOnly {
		where = append(where, "status = 'new'")
	}
	if opts.Disappeared {
		where = append(where, "status = 'disappeared'")
	}
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}

	var n int
	err := s.db.QueryRowContext(ctx, query, args...).Scan(&n)
	return n, err
}

func (s *SQLiteStore) CountFindingsByTargetID(ctx context.Context, targetID string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM findings WHERE asset_id IN (SELECT id FROM assets WHERE target_id = ?)`,
		targetID).Scan(&n)
	return n, err
}

func (s *SQLiteStore) CountAssetsByTargetID(ctx context.Context, targetID string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM assets WHERE target_id = ?`, targetID).Scan(&n)
	return n, err
}

func (s *SQLiteStore) CountScansByTargetID(ctx context.Context, targetID string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM scans WHERE target_id = ?`, targetID).Scan(&n)
	return n, err
}

func (s *SQLiteStore) CountFindingsByAssetIDs(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT asset_id, COUNT(*) FROM findings GROUP BY asset_id`)
	if err != nil {
		return nil, fmt.Errorf("counting findings by asset: %w", err)
	}
	defer rows.Close()

	counts := make(map[string]int)
	for rows.Next() {
		var assetID string
		var n int
		if err := rows.Scan(&assetID, &n); err != nil {
			return nil, fmt.Errorf("scanning finding count: %w", err)
		}
		counts[assetID] = n
	}
	return counts, rows.Err()
}

func (s *SQLiteStore) LastScan(ctx context.Context) (*model.Scan, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, target_id, type, status, phase, progress, stats, started_at, finished_at, error, created_at, updated_at
		 FROM scans ORDER BY created_at DESC LIMIT 1`)

	var sc model.Scan
	var statsJSON string
	var startedAt, finishedAt, createdAt, updatedAt sql.NullString

	err := row.Scan(&sc.ID, &sc.TargetID, &sc.Type, &sc.Status, &sc.Phase, &sc.Progress,
		&statsJSON, &startedAt, &finishedAt, &sc.Error, &createdAt, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scanning last scan: %w", err)
	}

	if err := json.Unmarshal([]byte(statsJSON), &sc.Stats); err != nil {
		return nil, fmt.Errorf("unmarshaling scan stats: %w", err)
	}
	sc.StartedAt = parseTimePtr(startedAt)
	sc.FinishedAt = parseTimePtr(finishedAt)
	sc.CreatedAt = parseTime(createdAt)
	sc.UpdatedAt = parseTime(updatedAt)
	return &sc, nil
}

// --- Single entity lookups ---

func (s *SQLiteStore) GetAsset(ctx context.Context, id string) (*model.Asset, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, target_id, parent_id, type, value, status, tags, metadata, first_seen, last_seen, created_at, updated_at
		 FROM assets WHERE id = ?`, id)

	var a model.Asset
	var parentID sql.NullString
	var tagsJSON, metaJSON string
	var firstSeen, lastSeen, createdAt, updatedAt sql.NullString

	err := row.Scan(&a.ID, &a.TargetID, &parentID, &a.Type, &a.Value, &a.Status,
		&tagsJSON, &metaJSON, &firstSeen, &lastSeen, &createdAt, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scanning asset row: %w", err)
	}

	a.ParentID = parentID.String
	if err := json.Unmarshal([]byte(tagsJSON), &a.Tags); err != nil {
		return nil, fmt.Errorf("unmarshaling tags: %w", err)
	}
	if err := json.Unmarshal([]byte(metaJSON), &a.Metadata); err != nil {
		return nil, fmt.Errorf("unmarshaling metadata: %w", err)
	}
	a.FirstSeen = parseTime(firstSeen)
	a.LastSeen = parseTime(lastSeen)
	a.CreatedAt = parseTime(createdAt)
	a.UpdatedAt = parseTime(updatedAt)
	return &a, nil
}

func (s *SQLiteStore) GetFinding(ctx context.Context, id string) (*model.Finding, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, asset_id, scan_id, template_id, template_name, severity, title, description,
		   "references", remediation, evidence, cvss, cve, status, source_tool, confidence,
		   first_seen, last_seen, resolved_at, created_at, updated_at
		 FROM findings WHERE id = ?`, id)

	var f model.Finding
	var scanID, cve, resolvedAt sql.NullString
	var refsJSON string
	var firstSeen, lastSeen, createdAt, updatedAt sql.NullString
	var cvss sql.NullFloat64

	err := row.Scan(&f.ID, &f.AssetID, &scanID, &f.TemplateID, &f.TemplateName,
		&f.Severity, &f.Title, &f.Description, &refsJSON, &f.Remediation,
		&f.Evidence, &cvss, &cve, &f.Status, &f.SourceTool, &f.Confidence,
		&firstSeen, &lastSeen, &resolvedAt, &createdAt, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scanning finding row: %w", err)
	}

	f.ScanID = scanID.String
	f.CVE = cve.String
	if cvss.Valid {
		f.CVSS = cvss.Float64
	}
	if err := json.Unmarshal([]byte(refsJSON), &f.References); err != nil {
		return nil, fmt.Errorf("unmarshaling references: %w", err)
	}
	f.FirstSeen = parseTime(firstSeen)
	f.LastSeen = parseTime(lastSeen)
	f.ResolvedAt = parseTimePtr(resolvedAt)
	f.CreatedAt = parseTime(createdAt)
	f.UpdatedAt = parseTime(updatedAt)
	return &f, nil
}

func (s *SQLiteStore) ListToolRuns(ctx context.Context, scanID string) ([]model.ToolRun, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, scan_id, tool_name, phase, status, started_at, finished_at,
		   duration_ms, targets_count, findings_count, output_summary, error_message, exit_code, config, created_at, updated_at
		 FROM tool_runs WHERE scan_id = ? ORDER BY started_at ASC`, scanID)
	if err != nil {
		return nil, fmt.Errorf("listing tool runs: %w", err)
	}
	defer rows.Close()

	runs := make([]model.ToolRun, 0)
	for rows.Next() {
		var tr model.ToolRun
		var finishedAt, startedAt, createdAt, updatedAt sql.NullString
		var configJSON string

		if err := rows.Scan(&tr.ID, &tr.ScanID, &tr.ToolName, &tr.Phase, &tr.Status,
			&startedAt, &finishedAt, &tr.DurationMs, &tr.TargetsCount, &tr.FindingsCount,
			&tr.OutputSummary, &tr.ErrorMessage, &tr.ExitCode, &configJSON,
			&createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scanning tool run row: %w", err)
		}

		if err := json.Unmarshal([]byte(configJSON), &tr.Config); err != nil {
			return nil, fmt.Errorf("unmarshaling config: %w", err)
		}
		tr.StartedAt = parseTime(startedAt)
		tr.FinishedAt = parseTimePtr(finishedAt)
		tr.CreatedAt = parseTime(createdAt)
		tr.UpdatedAt = parseTime(updatedAt)
		runs = append(runs, tr)
	}
	return runs, rows.Err()
}

func (s *SQLiteStore) CountFindingsBySeverity(ctx context.Context) (map[model.Severity]int, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT severity, COUNT(*) FROM findings GROUP BY severity`)
	if err != nil {
		return nil, fmt.Errorf("counting findings by severity: %w", err)
	}
	defer rows.Close()

	counts := make(map[model.Severity]int)
	for rows.Next() {
		var sev model.Severity
		var n int
		if err := rows.Scan(&sev, &n); err != nil {
			return nil, fmt.Errorf("scanning severity count: %w", err)
		}
		counts[sev] = n
	}
	return counts, rows.Err()
}

func (s *SQLiteStore) CountAssetsByType(ctx context.Context) (map[model.AssetType]int, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT type, COUNT(*) FROM assets GROUP BY type`)
	if err != nil {
		return nil, fmt.Errorf("counting assets by type: %w", err)
	}
	defer rows.Close()

	counts := make(map[model.AssetType]int)
	for rows.Next() {
		var typ model.AssetType
		var n int
		if err := rows.Scan(&typ, &n); err != nil {
			return nil, fmt.Errorf("scanning type count: %w", err)
		}
		counts[typ] = n
	}
	return counts, rows.Err()
}

// GroupedFindingOptions configures the host-grouped findings query.
type GroupedFindingOptions struct {
	Severity   model.Severity
	SourceTool string
	Host       string
	SortBy     string // "last_seen", "severity", "affected_assets_count"
	Limit      int
	Offset     int
}

// GroupedFinding is one row of the host-aggregated findings view.
type GroupedFinding struct {
	Host                string   `json:"host"`
	TemplateID          string   `json:"template_id"`
	SourceTool          string   `json:"source_tool"`
	Title               string   `json:"title"`
	Severity            string   `json:"severity"`
	FirstSeen           string   `json:"first_seen"`
	LastSeen            string   `json:"last_seen"`
	AffectedAssetsCount int      `json:"affected_assets_count"`
	FindingIDs          []string `json:"finding_ids"`
}

// ListGroupedFindings returns findings grouped by (host, template_id, source_tool).
// Host is derived from the asset value by stripping the ":port/tcp" suffix.
func (s *SQLiteStore) ListGroupedFindings(ctx context.Context, opts GroupedFindingOptions) ([]GroupedFinding, error) {
	if opts.Limit <= 0 {
		opts.Limit = 50
	}

	// host = everything before the first ":" in asset.value, falling back
	// to the full value when there's no colon (e.g. bare domains).
	const hostExpr = `CASE WHEN instr(a.value,':')>0 THEN substr(a.value,1,instr(a.value,':')-1) ELSE a.value END`

	query := `SELECT ` + hostExpr + ` AS host,
		f.template_id, f.source_tool,
		MAX(f.title) AS title, MAX(f.severity) AS severity,
		MIN(f.first_seen) AS first_seen, MAX(f.last_seen) AS last_seen,
		COUNT(DISTINCT f.asset_id) AS affected_assets_count,
		GROUP_CONCAT(f.id) AS finding_ids
		FROM findings f
		JOIN assets a ON f.asset_id = a.id`

	where := []string{}
	args := []interface{}{}

	if opts.Severity != "" {
		where = append(where, "f.severity = ?")
		args = append(args, string(opts.Severity))
	}
	if opts.SourceTool != "" {
		where = append(where, "f.source_tool = ?")
		args = append(args, opts.SourceTool)
	}
	if opts.Host != "" {
		where = append(where, hostExpr+" LIKE ?")
		args = append(args, "%"+opts.Host+"%")
	}

	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}

	query += ` GROUP BY host, f.template_id, f.source_tool`

	switch opts.SortBy {
	case "severity":
		query += ` ORDER BY CASE MAX(f.severity)
			WHEN 'critical' THEN 0 WHEN 'high' THEN 1 WHEN 'medium' THEN 2
			WHEN 'low' THEN 3 WHEN 'info' THEN 4 END, MAX(f.last_seen) DESC`
	case "affected_assets_count":
		query += ` ORDER BY affected_assets_count DESC, MAX(f.last_seen) DESC`
	default:
		query += ` ORDER BY MAX(f.last_seen) DESC`
	}

	query += ` LIMIT ? OFFSET ?`
	args = append(args, opts.Limit, opts.Offset)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing grouped findings: %w", err)
	}
	defer rows.Close()

	results := make([]GroupedFinding, 0)
	for rows.Next() {
		var g GroupedFinding
		var idsConcat string
		if err := rows.Scan(&g.Host, &g.TemplateID, &g.SourceTool,
			&g.Title, &g.Severity, &g.FirstSeen, &g.LastSeen,
			&g.AffectedAssetsCount, &idsConcat); err != nil {
			return nil, fmt.Errorf("scanning grouped finding row: %w", err)
		}
		g.FindingIDs = strings.Split(idsConcat, ",")
		results = append(results, g)
	}
	return results, rows.Err()
}

// CountGroupedFindings returns the total number of grouped rows for pagination.
func (s *SQLiteStore) CountGroupedFindings(ctx context.Context, opts GroupedFindingOptions) (int, error) {
	const hostExpr = `CASE WHEN instr(a.value,':')>0 THEN substr(a.value,1,instr(a.value,':')-1) ELSE a.value END`

	query := `SELECT COUNT(*) FROM (
		SELECT 1 FROM findings f JOIN assets a ON f.asset_id = a.id`

	where := []string{}
	args := []interface{}{}

	if opts.Severity != "" {
		where = append(where, "f.severity = ?")
		args = append(args, string(opts.Severity))
	}
	if opts.SourceTool != "" {
		where = append(where, "f.source_tool = ?")
		args = append(args, opts.SourceTool)
	}
	if opts.Host != "" {
		where = append(where, hostExpr+" LIKE ?")
		args = append(args, "%"+opts.Host+"%")
	}

	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}

	query += ` GROUP BY ` + hostExpr + `, f.template_id, f.source_tool)`

	var n int
	err := s.db.QueryRowContext(ctx, query, args...).Scan(&n)
	return n, err
}

// CountUniqueFindingsByHost returns the number of distinct (host, template_id, source_tool)
// tuples across all findings. Used by the dashboard to show de-duplicated count.
func (s *SQLiteStore) CountUniqueFindingsByHost(ctx context.Context) (int, error) {
	query := `SELECT COUNT(*) FROM (
		SELECT 1 FROM findings f JOIN assets a ON f.asset_id = a.id
		GROUP BY CASE WHEN instr(a.value,':')>0 THEN substr(a.value,1,instr(a.value,':')-1) ELSE a.value END,
		f.template_id, f.source_tool)`
	var n int
	err := s.db.QueryRowContext(ctx, query).Scan(&n)
	return n, err
}

// --- Helpers ---

func detectTargetType(value string) (model.TargetType, error) {
	if strings.Contains(value, "/") {
		_, _, err := net.ParseCIDR(value)
		if err != nil {
			return "", fmt.Errorf("%w: invalid CIDR %q", ErrInvalidTarget, value)
		}
		return model.TargetTypeCIDR, nil
	}
	if ip := net.ParseIP(value); ip != nil {
		return model.TargetTypeIP, nil
	}
	if domainRegex.MatchString(value) {
		return model.TargetTypeDomain, nil
	}
	return "", fmt.Errorf("%w: %q is not a valid domain, IP, or CIDR", ErrInvalidTarget, value)
}

func validateTargetValue(value string, typ model.TargetType) error {
	switch typ {
	case model.TargetTypeDomain:
		if !domainRegex.MatchString(value) {
			return fmt.Errorf("%w: %q is not a valid domain", ErrInvalidTarget, value)
		}
	case model.TargetTypeCIDR:
		if _, _, err := net.ParseCIDR(value); err != nil {
			return fmt.Errorf("%w: %q is not a valid CIDR", ErrInvalidTarget, value)
		}
	case model.TargetTypeIP:
		if net.ParseIP(value) == nil {
			return fmt.Errorf("%w: %q is not a valid IP", ErrInvalidTarget, value)
		}
	default:
		return fmt.Errorf("%w: unknown type %q", ErrInvalidTarget, typ)
	}
	return nil
}

type scannable interface {
	Scan(dest ...interface{}) error
}

func scanTarget(row scannable) (*model.Target, error) {
	var t model.Target
	var enabled int
	var lastScanID, lastScanAt, createdAt, updatedAt sql.NullString

	err := row.Scan(&t.ID, &t.Value, &t.Type, &t.Scope, &enabled,
		&lastScanID, &lastScanAt, &createdAt, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scanning target row: %w", err)
	}

	t.Enabled = enabled != 0
	t.LastScanID = lastScanID.String
	t.LastScanAt = parseTimePtr(lastScanAt)
	t.CreatedAt = parseTime(createdAt)
	t.UpdatedAt = parseTime(updatedAt)
	return &t, nil
}

func scanTargetRow(rows *sql.Rows) (*model.Target, error) {
	return scanTarget(rows)
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func sqlNullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

func timePtr(t *time.Time) interface{} {
	if t == nil {
		return nil
	}
	return t.Format(timeFormat)
}

func timePtrVal(t *time.Time) interface{} {
	if t == nil {
		return nil
	}
	return t.Format(timeFormat)
}

func parseTime(ns sql.NullString) time.Time {
	if !ns.Valid || ns.String == "" {
		return time.Time{}
	}
	t, _ := time.Parse(timeFormat, ns.String)
	return t
}

func parseTimePtr(ns sql.NullString) *time.Time {
	if !ns.Valid || ns.String == "" {
		return nil
	}
	t, err := time.Parse(timeFormat, ns.String)
	if err != nil {
		return nil
	}
	return &t
}
