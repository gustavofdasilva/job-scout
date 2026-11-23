package storage

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

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

// greenhouseFoundAt reproduz o FoundAt que crawlers como Greenhouse/Ashby
// geram: time.Parse(RFC3339) de uma data com offset fixo diferente do fuso
// local, que t.String() formata como "... -0500 -0500".
func greenhouseFoundAt(t *testing.T) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, "2026-11-20T10:00:00-05:00")
	if err != nil {
		t.Fatal(err)
	}
	return ts
}

// Regressão: /api/jobs quebrava com "unsupported Scan, storing
// driver.Value type string into type *time.Time" em found_at.
func TestJobsWithAnalysisReadsOffsetFoundAt(t *testing.T) {
	db := openTestDB(t)
	foundAt := greenhouseFoundAt(t)
	if err := db.InsertJob(models.Job{ID: "gh", Source: "greenhouse", Title: "Backend Engineer", Company: "Acme", URL: "https://x/gh", FoundAt: foundAt}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.InsertAnalysis(models.Analysis{JobID: "gh", FitScore: 80, CreatedAt: foundAt}); err != nil {
		t.Fatal(err)
	}

	jobs, err := db.GetJobsWithAnalysis("", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || !jobs[0].FoundAt.Equal(foundAt) || jobs[0].FitScore != 80 {
		t.Fatalf("vaga lida errada: %+v", jobs)
	}
}

// Datas gravadas antes do _time_format (formato t.String()) precisam ser
// reescritas pela migração pra voltarem a ser lidas.
func TestNormalizeLegacyTimestamps(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()

	// Conexão sem dsnParams = comportamento antigo do driver.
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	offset := greenhouseFoundAt(t)
	now := time.Now() // com leitura monotônica: " m=+0.001"
	for i, ts := range []time.Time{offset, now} {
		id := fmt.Sprintf("j%d", i)
		if _, err := raw.Exec(`INSERT INTO jobs (id, source, title, company, location, url, description, salary, found_at) VALUES (?, 'x', ?, 'c', '', ?, '', '', ?)`, id, id, "u"+id, ts); err != nil {
			t.Fatal(err)
		}
		if _, err := raw.Exec(`INSERT INTO analyses (id, job_id, fit_score, summary, benefits, fit_reasoning, resume_md, resume_pdf_path, created_at) VALUES (?, ?, 50, '', '', '', '', '', ?)`, "a"+id, id, ts); err != nil {
			t.Fatal(err)
		}
	}
	var stored string
	if err := raw.QueryRow(`SELECT CAST(found_at AS TEXT) FROM jobs WHERE id = 'j0'`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(stored, "-0500 -0500") {
		t.Fatalf("pré-condição: esperava formato t.String() com offset fixo, veio %q", stored)
	}
	raw.Close()

	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	jobs, err := db.GetJobsWithAnalysis("", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 2 {
		t.Fatalf("esperava 2 vagas, veio %d", len(jobs))
	}
	// Ordenação por found_at DESC (fit_score empata): now vem antes.
	if jobs[0].ID != "j1" || !jobs[1].FoundAt.Equal(offset) || !jobs[0].FoundAt.Equal(now.Round(0)) {
		t.Fatalf("datas normalizadas erradas: %v / %v", jobs[0].FoundAt, jobs[1].FoundAt)
	}
	_, analysis, err := db.GetJobByID("j0")
	if err != nil || !analysis.CreatedAt.Equal(offset) {
		t.Fatalf("created_at normalizado errado: %v err=%v", analysis.CreatedAt, err)
	}
}
