package scheduler

import (
	"path/filepath"
	"testing"

	"job-scout/internal/config"
	"job-scout/internal/models"
	"job-scout/internal/storage"
	"job-scout/internal/titlefilter"
)

func TestFilterPendingByTitle(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	jobs := []models.Job{
		{ID: "1", Title: "Senior Backend Engineer"},
		{ID: "2", Title: "Sales Director"},
		{ID: "3", Title: "Product Manager"},
		{ID: "4", Title: "Office Coordinator"},           // sem termo obrigatório
		{ID: "5", Title: "Recruiter", Status: "applied"}, // status escolhido pelo usuário: não mexe
	}
	for _, j := range jobs {
		j.Source, j.Company, j.URL = "lever", "c", "u"+j.ID
		if err := db.InsertJob(j); err != nil {
			t.Fatal(err)
		}
	}

	f, err := titlefilter.New([]string{"sales", "product manager", "recruiter"}, []string{"engineer"})
	if err != nil {
		t.Fatal(err)
	}
	s := NewScheduler(&config.Config{MaxAnalysisAttempts: 3}, db, nil, nil, nil, f)

	filtered, pending, err := s.FilterPendingByTitle()
	if err != nil {
		t.Fatal(err)
	}
	if filtered != 3 || pending != 2 {
		t.Fatalf("filtradas=%d pendentes=%d, esperava 3 e 2", filtered, pending)
	}
	// Idempotente: um segundo passe não refiltra nada.
	if filtered, _, _ := s.FilterPendingByTitle(); filtered != 0 {
		t.Fatalf("segundo passe filtrou %d", filtered)
	}
	job, _, _ := db.GetJobByID("5")
	if job.Status != "applied" {
		t.Fatalf("status de vaga não-nova alterado: %s", job.Status)
	}
}
