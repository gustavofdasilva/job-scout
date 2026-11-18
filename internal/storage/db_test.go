package storage

import (
	"path/filepath"
	"testing"

	"job-scout/internal/models"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestJobExtraFieldsRoundTrip(t *testing.T) {
	db := openTestDB(t)
	in := models.Job{
		ID: "j1", Source: "himalayas", Title: "Backend Engineer", Company: "Acme", URL: "https://x/1",
		LocationRestriction: "Brazil, LATAM", SalaryMin: 100, SalaryMax: 200, SalaryCurrency: "USD", SalaryPeriod: "annual",
	}
	if err := db.InsertJob(in); err != nil {
		t.Fatal(err)
	}
	got, _, err := db.GetJobByID("j1")
	if err != nil {
		t.Fatal(err)
	}
	if got.LocationRestriction != in.LocationRestriction || got.SalaryMin != 100 || got.SalaryMax != 200 ||
		got.SalaryCurrency != "USD" || got.SalaryPeriod != "annual" {
		t.Fatalf("campos novos não persistidos: %+v", got)
	}

	if !db.JobExistsByDedupKey(models.Job{Company: "ACME", Title: "backend engineer"}.DedupKey()) {
		t.Fatal("esperava duplicata por empresa+título")
	}
	if db.JobExistsByDedupKey("") {
		t.Fatal("chave vazia não deveria ser duplicata")
	}
}

func TestPendingAnalysisAttempts(t *testing.T) {
	db := openTestDB(t)
	for _, id := range []string{"a", "b"} {
		if err := db.InsertJob(models.Job{ID: id, Source: "x", Title: id, Company: "c", URL: "u" + id}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 1; i <= 3; i++ {
		if n, err := db.IncrementAnalysisAttempts("a"); err != nil || n != i {
			t.Fatalf("tentativa %d: n=%d err=%v", i, n, err)
		}
	}
	pending, err := db.GetPendingAnalysisJobs(50, 3)
	if err != nil || len(pending) != 1 || pending[0].ID != "b" {
		t.Fatalf("pendentes com limite: %+v err=%v", pending, err)
	}
	pending, _ = db.GetPendingAnalysisJobs(50, -1)
	if len(pending) != 2 {
		t.Fatalf("pendentes sem limite: %d", len(pending))
	}
}
