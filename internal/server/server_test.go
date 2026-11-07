package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestHealthz(t *testing.T) {
	rec := httptest.NewRecorder()
	Handler(Config{}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["status"] != "ok" {
		t.Fatalf("status field = %q, want ok", body["status"])
	}
}

func TestUIFallsBackToIndex(t *testing.T) {
	ui := fstest.MapFS{
		"index.html":  {Data: []byte("<html>app</html>")},
		"_app/app.js": {Data: []byte("console.log(1)")},
	}
	h := Handler(Config{UI: ui})

	cases := map[string]string{
		"/":            "<html>app</html>",
		"/lanes/abc":   "<html>app</html>",
		"/_app/app.js": "console.log(1)",
	}
	for p, want := range cases {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), want) {
			t.Errorf("GET %s = %d %q, want body containing %q", p, rec.Code, rec.Body.String(), want)
		}
	}
}

func TestUIPlaceholderWhenNotBuilt(t *testing.T) {
	rec := httptest.NewRecorder()
	Handler(Config{}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if !strings.Contains(rec.Body.String(), "has not been built") {
		t.Fatalf("expected placeholder page, got %q", rec.Body.String())
	}
}
