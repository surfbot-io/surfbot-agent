package v1

// SPEC-SCHED1.5 R3: serve the hand-written JSON Schemas from docs/schemas/
// at /api/v1/schemas/tools/{tool}. The schemas are embedded through the
// small docsschemas package (docs/schemas/embed.go) so the canonical
// authoring location stays under docs/ without falling afoul of Go's
// embed "no parent traversal" rule.

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"sort"
	"strings"

	docsschemas "github.com/surfbot-io/surfbot-agent/docs/schemas"
)

// toolSchemaContentType is the media type advertised on schema responses.
// The 1.5 OQ3 decision: application/schema+json is spec-correct but not
// universally supported by raw-JSON consumers (jq, some proxies). We
// therefore advertise application/json — strict JSON-Schema ecosystems
// can refetch with Accept: application/schema+json once we add content
// negotiation in a future spec.
const toolSchemaContentType = "application/json"

// ToolSchemaIndex is the response shape of GET /api/v1/schemas/tools.
type ToolSchemaIndex struct {
	Tools []string `json:"tools"`
}

// routeSchemas serves GET /api/v1/schemas/tools (index) and forwards
// /api/v1/schemas/tools/{tool} to routeSchemaByName. Kept in this file
// so the embed import lives next to its only consumer.
func (h *handlers) routeSchemas(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, "GET")
		return
	}
	writeJSON(w, http.StatusOK, ToolSchemaIndex{Tools: schemaToolNames()})
}

func (h *handlers) routeSchemaByName(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, "GET")
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/api/v1/schemas/tools/")
	if name == "" || strings.Contains(name, "/") {
		writeProblem(w, http.StatusNotFound, "/problems/not-found",
			"Unknown schema subresource", "", nil)
		return
	}
	body, err := docsschemas.Tools.ReadFile("tools/" + name + ".json")
	if err != nil {
		writeProblem(w, http.StatusNotFound, "/problems/not-found",
			"Schema not found",
			"no tool schema for "+name+"; see /api/v1/schemas/tools for the available set", nil)
		return
	}
	w.Header().Set("Content-Type", toolSchemaContentType)
	_, _ = w.Write(body)
}

// schemaToolNames returns the sorted list of tool names that have a
// shipped schema. Derived once at server boot from the embed FS so
// adding a new tool schema is one-file (drop the JSON into
// docs/schemas/tools/, done).
func schemaToolNames() []string {
	entries, err := fs.ReadDir(docsschemas.Tools, "tools")
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		n := e.Name()
		if !strings.HasSuffix(n, ".json") {
			continue
		}
		out = append(out, strings.TrimSuffix(n, ".json"))
	}
	sort.Strings(out)
	return out
}

// sanityCheckSchemas is called from tests to ensure the embed set
// parses as JSON. Exposed via a package-level helper rather than
// running at boot — a bad schema at runtime would be caught here and
// in routeSchemaByName's error path anyway.
func sanityCheckSchemas() error {
	for _, name := range schemaToolNames() {
		body, err := docsschemas.Tools.ReadFile("tools/" + name + ".json")
		if err != nil {
			return err
		}
		var obj map[string]any
		if err := json.Unmarshal(body, &obj); err != nil {
			return err
		}
	}
	return nil
}
