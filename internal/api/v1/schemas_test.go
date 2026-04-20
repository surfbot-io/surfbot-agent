package v1

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSchemas_SanityCheck(t *testing.T) {
	if err := sanityCheckSchemas(); err != nil {
		t.Fatalf("sanityCheckSchemas: %v", err)
	}
}

func TestSchemas_Index_ListsAllTools(t *testing.T) {
	mux := http.NewServeMux()
	RegisterRoutes(mux, APIDeps{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/schemas/tools", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200", rec.Code)
	}
	var body ToolSchemaIndex
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := map[string]bool{"nuclei": true, "naabu": true, "httpx": true, "subfinder": true, "dnsx": true}
	if len(body.Tools) != len(want) {
		t.Fatalf("tools=%v, want keys %v", body.Tools, want)
	}
	for _, n := range body.Tools {
		if !want[n] {
			t.Errorf("unexpected tool %q", n)
		}
	}
}

func TestSchemas_Get_ReturnsEmbeddedBytes(t *testing.T) {
	mux := http.NewServeMux()
	RegisterRoutes(mux, APIDeps{})

	for _, tool := range []string{"nuclei", "naabu", "httpx", "subfinder", "dnsx"} {
		t.Run(tool, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/schemas/tools/"+tool, nil)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status=%d want 200", rec.Code)
			}
			if ct := rec.Header().Get("Content-Type"); ct != toolSchemaContentType {
				t.Errorf("Content-Type=%q want %q", ct, toolSchemaContentType)
			}
			body, _ := io.ReadAll(rec.Body)
			var obj map[string]any
			if err := json.Unmarshal(body, &obj); err != nil {
				t.Fatalf("schema not valid JSON: %v", err)
			}
			if _, ok := obj["title"]; !ok {
				t.Errorf("schema missing `title`")
			}
			if _, ok := obj["properties"]; !ok {
				t.Errorf("schema missing `properties`")
			}
		})
	}
}

func TestSchemas_Get_UnknownTool_404(t *testing.T) {
	mux := http.NewServeMux()
	RegisterRoutes(mux, APIDeps{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/schemas/tools/nonexistent", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 404", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != ProblemContentType {
		t.Errorf("Content-Type=%q want problem+json", ct)
	}
}

func TestSchemas_WrongMethod_405(t *testing.T) {
	mux := http.NewServeMux()
	RegisterRoutes(mux, APIDeps{})

	for _, path := range []string{"/api/v1/schemas/tools", "/api/v1/schemas/tools/nuclei"} {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("POST %s: status=%d want 405", path, rec.Code)
		}
	}
}
