package crawler

import (
	"strings"
	"testing"
)

func TestParseWorkingNomads(t *testing.T) {
	jobs, err := parseWorkingNomads(readFixture(t, "workingnomads.json"), []string{"developer", "backend"})
	if err != nil {
		t.Fatal(err)
	}
	// "Senior React Full-stack Developer" e "Senior .NET Full-stack Developer".
	if len(jobs) != 2 {
		t.Fatalf("esperava 2 vagas filtradas, veio %d", len(jobs))
	}

	j := jobs[0]
	if j.Source != "workingnomads" || j.Company != "Lemon.io" || j.Title != "Senior React Full-stack Developer" {
		t.Errorf("campos básicos inesperados: %+v", j)
	}
	if j.LocationRestriction != "Europe, LATAM, APAC, the U.S., Canada" {
		t.Errorf("LocationRestriction = %q", j.LocationRestriction)
	}
	if j.Description == "" || strings.Contains(j.Description, "<p>") {
		t.Errorf("descrição não normalizada")
	}

	all, _ := parseWorkingNomads(readFixture(t, "workingnomads.json"), nil)
	if len(all) != 5 {
		t.Errorf("sem termos deveria trazer 5 vagas, veio %d", len(all))
	}
}
