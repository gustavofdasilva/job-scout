package crawler

import (
	"strings"
	"testing"
)

func TestParseWorkableAccount(t *testing.T) {
	jobs, err := parseWorkableAccount(readFixture(t, "workable.json"), "skroutz", []string{"engineer"})
	if err != nil {
		t.Fatal(err)
	}
	// Só a vaga de "Business Intelligence Engineer" casa com o termo.
	if len(jobs) != 1 {
		t.Fatalf("esperava 1 vaga filtrada, veio %d", len(jobs))
	}

	j := jobs[0]
	if j.Source != "workable" || j.Company != "Skroutz S.A" || j.URL != "https://apply.workable.com/j/00C85BAC46" {
		t.Errorf("campos básicos inesperados: %+v", j)
	}
	if j.Location != "Athens, Attica, Greece" || j.LocationRestriction != "" {
		t.Errorf("localização inesperada: %q / %q", j.Location, j.LocationRestriction)
	}
	if j.FoundAt.Format("2006-01-02") != "2026-10-21" {
		t.Errorf("FoundAt = %v", j.FoundAt)
	}
	if j.Description == "" || strings.Contains(j.Description, "<p>") {
		t.Errorf("descrição não normalizada")
	}

	all, _ := parseWorkableAccount(readFixture(t, "workable.json"), "skroutz", nil)
	if len(all) != 5 {
		t.Errorf("sem termos deveria trazer 5 vagas, veio %d", len(all))
	}
}
