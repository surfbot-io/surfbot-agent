package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/surfbot-io/surfbot-agent/internal/model"
	"github.com/surfbot-io/surfbot-agent/internal/rrule"
)

// TemplateStore is the persistence interface for reusable scan
// templates. Consumed by the daemon (cascade resolver), handlers, and
// the CLI, so it lives at a package boundary.
type TemplateStore interface {
	Create(ctx context.Context, t *model.Template) error
	Get(ctx context.Context, id string) (*model.Template, error)
	GetByName(ctx context.Context, name string) (*model.Template, error)
	List(ctx context.Context) ([]model.Template, error)
	Update(ctx context.Context, t *model.Template) error
	Delete(ctx context.Context, id string) error
}

// Templates returns a TemplateStore backed by this SQLiteStore.
func (s *SQLiteStore) Templates() TemplateStore {
	return &sqliteTemplateStore{db: s.db}
}

type sqliteTemplateStore struct {
	db *sql.DB
}

const templateColumns = `id, name, description, rrule, timezone, tool_config,
  maintenance_window, is_system, created_at, updated_at`

func (st *sqliteTemplateStore) Create(ctx context.Context, t *model.Template) error {
	if err := validateTemplateInput(t); err != nil {
		return err
	}
	if t.ID == "" {
		t.ID = uuid.New().String()
	}
	now := time.Now().UTC()
	t.CreatedAt = now
	t.UpdatedAt = now
	if t.ToolConfig == nil {
		t.ToolConfig = model.ToolConfig{}
	}

	toolJSON, mwJSON, err := marshalTemplateFields(t)
	if err != nil {
		return err
	}

	_, err = st.db.ExecContext(ctx,
		`INSERT INTO scan_templates (`+templateColumns+`)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.Name, t.Description, t.RRule, t.Timezone, toolJSON, mwJSON,
		boolToInt(t.IsSystem),
		t.CreatedAt.Format(timeFormat), t.UpdatedAt.Format(timeFormat),
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return fmt.Errorf("%w: template %q", ErrAlreadyExists, t.Name)
		}
		return fmt.Errorf("store.templates.Create: %w", err)
	}
	return nil
}

func (st *sqliteTemplateStore) Get(ctx context.Context, id string) (*model.Template, error) {
	row := st.db.QueryRowContext(ctx,
		`SELECT `+templateColumns+` FROM scan_templates WHERE id = ?`, id)
	return scanTemplate(row)
}

func (st *sqliteTemplateStore) GetByName(ctx context.Context, name string) (*model.Template, error) {
	row := st.db.QueryRowContext(ctx,
		`SELECT `+templateColumns+` FROM scan_templates WHERE name = ?`, name)
	return scanTemplate(row)
}

func (st *sqliteTemplateStore) List(ctx context.Context) ([]model.Template, error) {
	rows, err := st.db.QueryContext(ctx,
		`SELECT `+templateColumns+` FROM scan_templates ORDER BY name ASC`)
	if err != nil {
		return nil, fmt.Errorf("store.templates.List: %w", err)
	}
	defer rows.Close()
	out := make([]model.Template, 0)
	for rows.Next() {
		t, err := scanTemplate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

func (st *sqliteTemplateStore) Update(ctx context.Context, t *model.Template) error {
	if err := validateTemplateInput(t); err != nil {
		return err
	}
	t.UpdatedAt = time.Now().UTC()
	if t.ToolConfig == nil {
		t.ToolConfig = model.ToolConfig{}
	}

	toolJSON, mwJSON, err := marshalTemplateFields(t)
	if err != nil {
		return err
	}

	res, err := st.db.ExecContext(ctx,
		`UPDATE scan_templates SET
		   name = ?, description = ?, rrule = ?, timezone = ?,
		   tool_config = ?, maintenance_window = ?, is_system = ?,
		   updated_at = ?
		 WHERE id = ?`,
		t.Name, t.Description, t.RRule, t.Timezone, toolJSON, mwJSON,
		boolToInt(t.IsSystem), t.UpdatedAt.Format(timeFormat), t.ID,
	)
	if err != nil {
		return fmt.Errorf("store.templates.Update: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (st *sqliteTemplateStore) Delete(ctx context.Context, id string) error {
	res, err := st.db.ExecContext(ctx, `DELETE FROM scan_templates WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("store.templates.Delete: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func validateTemplateInput(t *model.Template) error {
	if t == nil {
		return fmt.Errorf("template is nil")
	}
	if strings.TrimSpace(t.Name) == "" {
		return fmt.Errorf("template.name is required")
	}
	if strings.TrimSpace(t.Timezone) == "" {
		return fmt.Errorf("template.timezone is required")
	}
	if _, err := rrule.ValidateRRule(t.RRule); err != nil {
		return err
	}
	if err := model.ValidateToolConfig(t.ToolConfig); err != nil {
		return err
	}
	return nil
}

func marshalTemplateFields(t *model.Template) (string, any, error) {
	toolJSON, err := json.Marshal(t.ToolConfig)
	if err != nil {
		return "", nil, fmt.Errorf("marshaling tool_config: %w", err)
	}
	var mwJSON any
	if t.MaintenanceWindow != nil {
		b, err := json.Marshal(t.MaintenanceWindow)
		if err != nil {
			return "", nil, fmt.Errorf("marshaling maintenance_window: %w", err)
		}
		mwJSON = string(b)
	}
	return string(toolJSON), mwJSON, nil
}

func scanTemplate(r rowScanner) (*model.Template, error) {
	var (
		t         model.Template
		toolJSON  string
		mwJSON    sql.NullString
		isSystem  int
		createdAt sql.NullString
		updatedAt sql.NullString
	)
	if err := r.Scan(
		&t.ID, &t.Name, &t.Description, &t.RRule, &t.Timezone, &toolJSON, &mwJSON,
		&isSystem, &createdAt, &updatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("scanning template: %w", err)
	}
	t.IsSystem = isSystem != 0
	t.CreatedAt = parseTime(createdAt)
	t.UpdatedAt = parseTime(updatedAt)
	if toolJSON == "" {
		t.ToolConfig = model.ToolConfig{}
	} else {
		if err := json.Unmarshal([]byte(toolJSON), &t.ToolConfig); err != nil {
			return nil, fmt.Errorf("unmarshaling tool_config: %w", err)
		}
	}
	if mwJSON.Valid && mwJSON.String != "" {
		var mw model.MaintenanceWindow
		if err := json.Unmarshal([]byte(mwJSON.String), &mw); err != nil {
			return nil, fmt.Errorf("unmarshaling maintenance_window: %w", err)
		}
		t.MaintenanceWindow = &mw
	}
	return &t, nil
}
