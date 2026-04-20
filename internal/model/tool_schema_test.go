package model

// SPEC-SCHED1.5 R2: structural round-trip test for the hand-written JSON
// Schemas in docs/schemas/tools/. Each DefaultXxxParams() struct is
// marshaled to JSON, the matching schema is parsed, and every serialized
// key is asserted to (a) appear among schema.properties, and (b) have a
// compatible declared type. This is NOT full JSON-Schema validation —
// that would require a vendored validator; instead we do a minimal
// structural check sufficient to catch the common drift cases: a struct
// field renamed, a schema property typo, or a missing property entirely.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// schemaFile is the on-disk JSON Schema decoded into Go maps for
// ad-hoc inspection. Schemas live under docs/schemas/tools/ at the
// repo root; walk up from the test's working directory (internal/model)
// to reach them.
type schemaFile struct {
	Title      string                 `json:"title"`
	Type       string                 `json:"type"`
	Properties map[string]schemaField `json:"properties"`
}

type schemaField struct {
	Type  string          `json:"type"`
	Items map[string]any  `json:"items,omitempty"`
	Enum  []string        `json:"enum,omitempty"`
	Raw   json.RawMessage `json:"-"`
}

func loadSchema(t *testing.T, tool string) schemaFile {
	t.Helper()
	path := filepath.Join("..", "..", "docs", "schemas", "tools", tool+".json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var s schemaFile
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	if s.Title == "" {
		t.Fatalf("%s: schema missing title", path)
	}
	if s.Type != "object" {
		t.Fatalf("%s: expected type=object, got %q", path, s.Type)
	}
	if len(s.Properties) == 0 {
		t.Fatalf("%s: no properties", path)
	}
	return s
}

// assertRoundTrip marshals `params`, decodes it as a map, and asserts
// every key in the marshaled JSON exists in schema.properties with a
// compatible scalar-vs-array-vs-object type. omitempty fields that were
// zero in the default are simply absent from the JSON and therefore
// skipped here — the schema declares them but they don't round-trip.
func assertRoundTrip(t *testing.T, tool string, params any) {
	t.Helper()
	s := loadSchema(t, tool)

	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("%s: marshal default params: %v", tool, err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("%s: decode marshaled params: %v", tool, err)
	}

	for key, val := range payload {
		prop, ok := s.Properties[key]
		if !ok {
			t.Errorf("%s: key %q in default params is not in schema.properties", tool, key)
			continue
		}
		gotKind := kindOfJSONValue(val)
		if prop.Type != "" && !kindCompatible(gotKind, prop.Type) {
			t.Errorf("%s: key %q type mismatch — schema says %q, marshaled value kind is %q", tool, key, prop.Type, gotKind)
		}
	}
}

func kindOfJSONValue(v any) string {
	if v == nil {
		return "null"
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Bool:
		return "boolean"
	case reflect.Float64, reflect.Int, reflect.Int64:
		return "number"
	case reflect.String:
		return "string"
	case reflect.Slice, reflect.Array:
		return "array"
	case reflect.Map:
		return "object"
	default:
		return rv.Kind().String()
	}
}

func kindCompatible(got, declared string) bool {
	if got == declared {
		return true
	}
	// JSON schema "integer" is numerically a "number" once marshaled —
	// Go's encoding/json always emits float64 on decode, so we accept
	// the numeric family here.
	if declared == "integer" && got == "number" {
		return true
	}
	return false
}

func TestToolSchemas_RoundTripDefaults(t *testing.T) {
	assertRoundTrip(t, "nuclei", DefaultNucleiParams())
	assertRoundTrip(t, "naabu", DefaultNaabuParams())
	assertRoundTrip(t, "httpx", DefaultHttpxParams())
	assertRoundTrip(t, "subfinder", DefaultSubfinderParams())
	assertRoundTrip(t, "dnsx", DefaultDnsxParams())
}

func TestToolSchemas_EveryRegisteredToolHasSchema(t *testing.T) {
	for tool := range RegisteredToolParams {
		path := filepath.Join("..", "..", "docs", "schemas", "tools", tool+".json")
		if _, err := os.Stat(path); err != nil {
			t.Errorf("registered tool %q has no schema at %s: %v", tool, path, err)
		}
	}
}
