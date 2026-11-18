package crawler

import (
	"strings"
	"testing"
)

func TestParseGreenhouseBoard(t *testing.T) {
	jobs, err := parseGreenhouseBoard(readFixture(t, "greenhouse.json"), "vercel", []string{"software engineer"})
	if err != nil {
		t.Fatal(err)
	}
	// A fixture tem 3 vagas de "Software Engineer" e 2 de vendas.
	if len(jobs) != 3 {
		t.Fatalf("esperava 3 vagas filtradas, veio %d", len(jobs))
	}

	j := jobs[0]
	if j.Source != "greenhouse" || j.Company != "Vercel" || j.Title != "Security Software Engineer, Detection & Response" {
		t.Errorf("campos básicos inesperados: %+v", j)
	}
	if j.LocationRestriction != "United States" {
		t.Errorf("LocationRestriction = %q (deveria vir de offices)", j.LocationRestriction)
	}
	if strings.ContainsAny(j.Description, "<>") || strings.Contains(j.Description, "&lt;") {
		t.Errorf("descrição ainda tem HTML: %q", j.Description[:min(200, len(j.Description))])
	}
	if !strings.Contains(j.Description, "About Vercel") {
		t.Errorf("descrição inesperada: %q", j.Description[:min(200, len(j.Description))])
	}

	all, _ := parseGreenhouseBoard(readFixture(t, "greenhouse.json"), "vercel", nil)
	if len(all) != 5 {
		t.Fatalf("sem termos deveria trazer 5 vagas, veio %d", len(all))
	}
	if all[3].LocationRestriction != "Germany" {
		t.Errorf("'Remote - Germany' deveria virar restrição Germany, veio %q", all[3].LocationRestriction)
	}
}
