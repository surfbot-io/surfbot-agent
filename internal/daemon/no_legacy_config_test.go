package daemon_test

// SCHED1.2b grep pin test: every reference to "ScheduleConfig" must live
// in an allowlisted file. The legacy ScheduleConfig type was deleted in
// SCHED1.2b; only the migration function (MigrateLegacyScheduleConfig)
// and its private parsing helper (legacyScheduleConfig in migrate_legacy.go)
// are permitted to mention the name. A new accidental usage breaks this
// test, surfacing the regression at PR time rather than after merge.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNoScheduleConfigReferences(t *testing.T) {
	allowed := map[string]bool{
		filepath.FromSlash("internal/daemon/intervalsched/migrate_legacy.go"):      true,
		filepath.FromSlash("internal/daemon/intervalsched/migrate_legacy_test.go"): true,
		// Self: this file mentions the symbol in comments and the
		// allowlist literal.
		filepath.FromSlash("internal/daemon/no_legacy_config_test.go"): true,
	}
	// MigrateLegacyScheduleConfig is the public entry point for the
	// one-shot migration; references to that exact identifier are
	// permitted everywhere because they are not references to the deleted
	// legacy type.
	exemptIdentifiers := []string{
		"MigrateLegacyScheduleConfig",
	}

	repoRoot, err := repoRoot()
	if err != nil {
		t.Fatalf("locate repo root: %v", err)
	}

	var offenders []string
	err = filepath.Walk(repoRoot, func(path string, info os.FileInfo, werr error) error {
		if werr != nil {
			return werr
		}
		if info.IsDir() {
			// Skip vendored / generated / VCS dirs.
			switch info.Name() {
			case ".git", "vendor", "node_modules", "static":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		rel, err := filepath.Rel(repoRoot, path)
		if err != nil {
			return err
		}
		if allowed[rel] {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := string(data)
		// Strip the exempt identifiers before matching so a line like
		// `intervalsched.MigrateLegacyScheduleConfig(...)` does not
		// false-positive.
		for _, ex := range exemptIdentifiers {
			text = strings.ReplaceAll(text, ex, "")
		}
		if strings.Contains(text, "ScheduleConfig") {
			offenders = append(offenders, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(offenders) > 0 {
		t.Fatalf("ScheduleConfig referenced from non-allowlisted files:\n  %s",
			strings.Join(offenders, "\n  "))
	}
}

// repoRoot walks up from the test's working directory until it finds a
// go.mod, returning that directory. Test packages are run from inside
// their package, so the test's CWD is internal/daemon — we walk up.
func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", os.ErrNotExist
		}
		dir = parent
	}
}
