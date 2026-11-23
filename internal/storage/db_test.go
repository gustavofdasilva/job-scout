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
	pending, err := db.GetPendingAnalysisJobs(50, 3, nil)
	if err != nil || len(pending) != 1 || pending[0].ID != "b" {
		t.Fatalf("pendentes com limite: %+v err=%v", pending, err)
	}
	pending, _ = db.GetPendingAnalysisJobs(50, -1, nil)
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

	jobs, _, err := db.GetJobsWithAnalysis(JobFilter{})
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

	jobs, _, err := db.GetJobsWithAnalysis(JobFilter{})
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

func TestJobsWithAnalysisPaginationAndFilters(t *testing.T) {
	db := openTestDB(t)
	base := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	// id, fonte, status, score (-1 = sem análise)
	seed := []struct {
		id, source, status string
		score              int
	}{
		{"a", "lever", "new", 90},
		{"b", "lever", "new", 70},
		{"c", "ashby", "applied", 70},
		{"d", "ashby", "new", 30},
		{"e", "lever", "new", -1},
	}
	for i, s := range seed {
		if err := db.InsertJob(models.Job{ID: s.id, Source: s.source, Title: s.id, Company: "c", URL: "u" + s.id, Status: s.status, FoundAt: base.Add(time.Duration(i) * time.Hour)}); err != nil {
			t.Fatal(err)
		}
		if s.score >= 0 {
			if _, err := db.InsertAnalysis(models.Analysis{JobID: s.id, FitScore: s.score}); err != nil {
				t.Fatal(err)
			}
		}
	}

	ids := func(jobs []JobWithAnalysis) string {
		var out []string
		for _, j := range jobs {
			out = append(out, j.ID)
		}
		return strings.Join(out, ",")
	}

	// Ordem: score desc, found_at desc (c é mais recente que b).
	page1, total, err := db.GetJobsWithAnalysis(JobFilter{MinScore: 50, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	page2, _, err := db.GetJobsWithAnalysis(JobFilter{MinScore: 50, Limit: 2, Offset: 2})
	if err != nil {
		t.Fatal(err)
	}
	if total != 4 || ids(page1) != "a,c" || ids(page2) != "b,e" {
		t.Fatalf("total=%d página1=%s página2=%s", total, ids(page1), ids(page2))
	}

	lever, total, _ := db.GetJobsWithAnalysis(JobFilter{Source: "lever", Status: "new"})
	if total != 3 || ids(lever) != "a,b,e" {
		t.Fatalf("filtro fonte+status: total=%d %s", total, ids(lever))
	}

	counts, err := db.CountJobsWithAnalysisByStatus(JobFilter{MinScore: 50, Status: "ignorado"})
	if err != nil {
		t.Fatal(err)
	}
	if counts["new"] != 3 || counts["applied"] != 1 || len(counts) != 2 {
		t.Fatalf("contagem por status: %v", counts)
	}
}

func TestPendingAnalysisPriorityAndFiltered(t *testing.T) {
	db := openTestDB(t)
	base := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	seed := []struct {
		id, source string
		age        time.Duration
	}{
		{"gupy-new", "gupy", 0},
		{"lever-old", "lever", 48 * time.Hour},
		{"ashby-mid", "ashby", 24 * time.Hour},
		{"sales", "lever", 0},
	}
	for _, s := range seed {
		if err := db.InsertJob(models.Job{ID: s.id, Source: s.source, Title: s.id, Company: "c", URL: "u" + s.id, FoundAt: base.Add(-s.age)}); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := db.MarkJobsFiltered([]string{"sales"}); err != nil || n != 1 {
		t.Fatalf("MarkJobsFiltered: n=%d err=%v", n, err)
	}

	pending, err := db.GetPendingAnalysisJobs(0, 3, []string{"lever", "ashby"})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, j := range pending {
		got = append(got, j.ID)
	}
	if strings.Join(got, ",") != "ashby-mid,lever-old,gupy-new" {
		t.Fatalf("ordem das pendentes: %v", got)
	}
	if n, _ := db.CountPendingAnalysisJobs(3); n != 3 {
		t.Fatalf("CountPendingAnalysisJobs = %d, esperava 3", n)
	}

	// Filtradas não aparecem em "todas", só pedindo o status.
	if _, total, _ := db.GetJobsWithAnalysis(JobFilter{}); total != 3 {
		t.Fatalf("todas: total=%d, esperava 3", total)
	}
	if _, total, _ := db.GetJobsWithAnalysis(JobFilter{Status: models.JobStatusFiltered}); total != 1 {
		t.Fatalf("filtradas: total=%d, esperava 1", total)
	}
}
