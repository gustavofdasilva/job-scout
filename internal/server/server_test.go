package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"job-scout/internal/models"
	"job-scout/internal/storage"
)

func newTestServer(t *testing.T) (*Server, *storage.DB) {
	t.Helper()
	db, err := storage.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return New(db, nil, nil, nil, nil, nil), db
}

func TestListJobsPagination(t *testing.T) {
	s, db := newTestServer(t)
	for _, id := range []string{"a", "b", "c"} {
		if err := db.InsertJob(models.Job{ID: id, Source: "lever", Title: id, Company: "c", URL: "u" + id}); err != nil {
			t.Fatal(err)
		}
	}

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/jobs?limit=2&offset=0&source=lever", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var jobs []storage.JobWithAnalysis
	if err := json.Unmarshal(rec.Body.Bytes(), &jobs); err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 2 || rec.Header().Get("X-Total-Count") != "3" {
		t.Fatalf("len=%d total=%q", len(jobs), rec.Header().Get("X-Total-Count"))
	}

	for _, bad := range []string{"limit=0", "limit=1000", "offset=-1", "min_score=x", "status=nope"} {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/jobs?"+bad, nil))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: esperava 400, veio %d", bad, rec.Code)
		}
	}

	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/jobs/counts", nil))
	var counts struct {
		ByStatus map[string]int `json:"by_status"`
		Sources  []string       `json:"sources"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &counts); err != nil {
		t.Fatal(err)
	}
	if counts.ByStatus["new"] != 3 || len(counts.Sources) != 1 {
		t.Fatalf("contagens: %+v", counts)
	}
}
