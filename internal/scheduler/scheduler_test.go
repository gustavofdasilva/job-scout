package scheduler

import (
	"path/filepath"
	"testing"

	"job-scout/internal/config"
	"job-scout/internal/jobfilter"
	"job-scout/internal/models"
	"job-scout/internal/storage"
)

func TestFilterPendingJobs(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	jobs := []models.Job{
		{ID: "1", Source: "lever", Title: "Senior Backend Engineer", Description: "Services written in Go."},
		{ID: "2", Source: "lever", Title: "Sales Engineer", Description: "Our platform is written in Go."},
		{ID: "3", Source: "gupy", Title: "Desenvolvedor Python", Description: "Django e go-to-market."},
		{ID: "4", Source: "gupy", Title: "Desenvolvedor Golang"},
		{ID: "5", Source: "gupy", Title: "Recruiter", Status: "applied"}, // status escolhido pelo usuário: não mexe
	}
	for _, j := range jobs {
		j.Company, j.URL = "c", "u"+j.ID
		if err := db.InsertJob(j); err != nil {
			t.Fatal(err)
		}
	}

	cfg, err := config.Load(filepath.Join("..", "..", "example.config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	f, err := jobfilter.New(cfg.JobFilter)
	if err != nil {
		t.Fatal(err)
	}
	s := NewScheduler(&config.Config{MaxAnalysisAttempts: 3}, db, nil, nil, nil, f)

	stats, pending, err := s.FilterPendingJobs()
	if err != nil {
		t.Fatal(err)
	}
	passed, filtered := stats.Total()
	// pending inclui a vaga "applied" (sem análise, não filtrada): status do
	// dashboard não impede a análise.
	if passed != 2 || filtered != 2 || pending != 3 {
		t.Fatalf("aprovadas=%d filtradas=%d pendentes=%d, esperava 2, 2 e 3", passed, filtered, pending)
	}
	if stats.Filtered["lever"] != 1 || stats.Filtered["gupy"] != 1 || stats.Passed["gupy"] != 1 {
		t.Fatalf("contagem por fonte: %+v", stats)
	}

	for id, want := range map[string]string{"2": jobfilter.ReasonTitleExclude, "3": jobfilter.ReasonNoMatch} {
		got, err := db.GetJobWithAnalysisByID(id)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != models.JobStatusFiltered || got.FilterReason != want {
			t.Errorf("vaga %s: status=%q motivo=%q, esperava %q/%q", id, got.Status, got.FilterReason, models.JobStatusFiltered, want)
		}
	}
	if job, _, _ := db.GetJobByID("5"); job.Status != "applied" {
		t.Fatalf("status de vaga não-nova alterado: %s", job.Status)
	}

	// Idempotente: um segundo passe não refiltra nada.
	if stats, _, _ := s.FilterPendingJobs(); len(stats.Filtered) != 0 {
		t.Fatalf("segundo passe filtrou %v", stats.Filtered)
	}

	// Voltar a vaga para "new" pelo dashboard limpa o motivo.
	if err := db.UpdateJobStatus("2", models.JobStatusNew); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.GetJobWithAnalysisByID("2"); got.FilterReason != "" {
		t.Fatalf("motivo deveria ser limpo, veio %q", got.FilterReason)
	}
}
