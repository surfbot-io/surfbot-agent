// Package daemon provides the long-running surfbot agent. SeedBuiltinTemplates
// (SPEC-SCHED2.3) wires three baseline scan templates — Default, Fast, Deep —
// into the templates table on first boot so a fresh install offers an
// immediately-usable schedule picker without an empty-state detour.
package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/surfbot-io/surfbot-agent/internal/model"
	"github.com/surfbot-io/surfbot-agent/internal/storage"
)

// SeedReport summarizes what SeedBuiltinTemplates did. Created and
// AlreadyPresent always sum to len(BuiltinTemplates) on success. Names
// reflects the canonical order (the order in BuiltinTemplates).
type SeedReport struct {
	Created        int
	AlreadyPresent int
	Names          []string
	Duration       time.Duration
}

// BuiltinTemplate is the literal definition of one builtin row, free of
// runtime fields like ID/CreatedAt. It is used both by SeedBuiltinTemplates
// (to materialize rows) and by agent-spec (to expose the catalog to
// LLM consumers without a database hit).
type BuiltinTemplate struct {
	Name           string
	Description    string
	RecommendedFor string
	RRule          string
	Timezone       string
	ToolConfig     model.ToolConfig
}

// BuiltinTemplates is the source-of-truth catalog. Order matters: it
// drives the order in agent-spec output and the order in which seed
// inserts emit log lines.
//
// Edits stick: the seed only inserts if a row with the same name is
// absent. Operators who customize a builtin keep their changes across
// reboots. Deletes are blocked by ErrSystemTemplateImmutable in the
// storage layer; recovery requires a SQL DELETE (see docs/scheduling.md).
var BuiltinTemplates = []BuiltinTemplate{
	{
		Name: "Default",
		Description: "Discovery + resolution + port scan (top 100) + HTTP probe + Nuclei (critical/high). " +
			"Recommended starting point for most targets.",
		RecommendedFor: "general-purpose daily scans",
		RRule:          "FREQ=DAILY;BYHOUR=2",
		Timezone:       "UTC",
		ToolConfig:     mustToolConfig(defaultToolConfig),
	},
	{
		Name: "Fast",
		Description: "Quick check: subdomain discovery + DNS resolution + HTTP probe. " +
			"Skips port scan and vulnerability scan. Useful for monitoring asset surface " +
			"without spending time on detection.",
		RecommendedFor: "frequent surface-area monitoring",
		// BYHOUR=0,6,12,18 is the explicit form; teambition/rrule-go does
		// not accept the */6 shorthand SPEC-SCHED2.3 OQ2 flagged.
		RRule:      "FREQ=DAILY;BYHOUR=0,6,12,18",
		Timezone:   "UTC",
		ToolConfig: mustToolConfig(fastToolConfig),
	},
	{
		Name: "Deep",
		Description: "Full pipeline: discovery + resolution + port scan (top 1000) + HTTP probe + " +
			"Nuclei against all severities. Higher coverage, longer runtime. Recommended for " +
			"high-value targets on a weekly cadence.",
		RecommendedFor: "high-value targets, weekly cadence",
		RRule:          "FREQ=WEEKLY;BYDAY=SU;BYHOUR=2",
		Timezone:       "UTC",
		ToolConfig:     mustToolConfig(deepToolConfig),
	},
}

func defaultToolConfig(tc model.ToolConfig) error {
	if err := model.SetTool(tc, "subfinder", model.DefaultSubfinderParams()); err != nil {
		return err
	}
	if err := model.SetTool(tc, "dnsx", model.DefaultDnsxParams()); err != nil {
		return err
	}
	if err := model.SetTool(tc, "naabu", model.DefaultNaabuParams()); err != nil {
		return err
	}
	if err := model.SetTool(tc, "httpx", model.DefaultHttpxParams()); err != nil {
		return err
	}
	nuclei := model.DefaultNucleiParams()
	nuclei.Severity = []string{"critical", "high"}
	return model.SetTool(tc, "nuclei", nuclei)
}

func fastToolConfig(tc model.ToolConfig) error {
	if err := model.SetTool(tc, "subfinder", model.DefaultSubfinderParams()); err != nil {
		return err
	}
	if err := model.SetTool(tc, "dnsx", model.DefaultDnsxParams()); err != nil {
		return err
	}
	return model.SetTool(tc, "httpx", model.DefaultHttpxParams())
}

func deepToolConfig(tc model.ToolConfig) error {
	if err := model.SetTool(tc, "subfinder", model.DefaultSubfinderParams()); err != nil {
		return err
	}
	if err := model.SetTool(tc, "dnsx", model.DefaultDnsxParams()); err != nil {
		return err
	}
	naabu := model.DefaultNaabuParams()
	naabu.Ports = "top1000"
	if err := model.SetTool(tc, "naabu", naabu); err != nil {
		return err
	}
	if err := model.SetTool(tc, "httpx", model.DefaultHttpxParams()); err != nil {
		return err
	}
	nuclei := model.DefaultNucleiParams()
	nuclei.Severity = []string{"critical", "high", "medium", "low", "info"}
	return model.SetTool(tc, "nuclei", nuclei)
}

// mustToolConfig builds a ToolConfig via the supplied populator. The
// populators only fail if json.Marshal of a Default*Params struct fails,
// which is impossible for the registered shapes — a panic here would
// indicate a code-level regression caught at init time.
func mustToolConfig(populate func(model.ToolConfig) error) model.ToolConfig {
	tc := model.ToolConfig{}
	if err := populate(tc); err != nil {
		panic(fmt.Sprintf("seed_templates: building builtin tool_config: %v", err))
	}
	return tc
}

// SeedBuiltinTemplates ensures every entry in BuiltinTemplates exists in
// scan_templates. Idempotent: rows are matched by name, and an existing
// row (system or operator-created) is left untouched. Missing rows are
// inserted with is_system=1 in a single transaction so partial seeds are
// never persisted.
//
// Intended to be called exactly once at scheduler bootstrap, between the
// legacy schedule migration and the master ticker.
func SeedBuiltinTemplates(
	ctx context.Context,
	store *storage.SQLiteStore,
	logger *slog.Logger,
) (SeedReport, error) {
	if logger == nil {
		logger = slog.Default()
	}
	start := time.Now()

	names := make([]string, 0, len(BuiltinTemplates))
	for _, b := range BuiltinTemplates {
		names = append(names, b.Name)
	}

	report := SeedReport{Names: names}

	err := store.Transact(ctx, func(ctx context.Context, ts storage.TxStores) error {
		for _, b := range BuiltinTemplates {
			existing, err := ts.Templates.GetByName(ctx, b.Name)
			if err == nil && existing != nil {
				report.AlreadyPresent++
				continue
			}
			if !errors.Is(err, storage.ErrNotFound) {
				return fmt.Errorf("looking up builtin %q: %w", b.Name, err)
			}

			tmpl := &model.Template{
				Name:        b.Name,
				Description: b.Description,
				RRule:       b.RRule,
				Timezone:    b.Timezone,
				ToolConfig:  cloneToolConfig(b.ToolConfig),
				IsSystem:    true,
			}
			if err := ts.Templates.Create(ctx, tmpl); err != nil {
				// Race: another process inserted the same name between
				// GetByName and Create. Treat as already-present rather
				// than failing the boot.
				if errors.Is(err, storage.ErrAlreadyExists) {
					report.AlreadyPresent++
					continue
				}
				return fmt.Errorf("creating builtin %q: %w", b.Name, err)
			}
			report.Created++
			logger.Info("template seeded", "name", b.Name, "id", tmpl.ID)
		}
		return nil
	})
	if err != nil {
		return SeedReport{}, err
	}

	report.Duration = time.Since(start)
	if report.Created > 0 {
		logger.Info("template seed complete",
			"created", report.Created,
			"already_present", report.AlreadyPresent,
			"duration_ms", report.Duration.Milliseconds(),
		)
	}
	return report, nil
}

// cloneToolConfig returns a deep copy so the package-level
// BuiltinTemplates entry is not aliased into the database row (Create
// mutates the *model.Template it receives).
func cloneToolConfig(src model.ToolConfig) model.ToolConfig {
	out := make(model.ToolConfig, len(src))
	for k, v := range src {
		buf := make(json.RawMessage, len(v))
		copy(buf, v)
		out[k] = buf
	}
	return out
}
