package crawler

import (
	"strings"
	"testing"
)

func TestParseRemotive(t *testing.T) {
	jobs, err := parseRemotive(readFixture(t, "remotive.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 4 {
		t.Fatalf("esperava 4 vagas, veio %d", len(jobs))
	}

	j := jobs[0]
	if j.Source != "remotive" || j.Title != "Tech Lead Full-Stack Rails Engineer" || j.Company != "Mitre Media" {
		t.Errorf("campos básicos inesperados: %+v", j)
	}
	if j.LocationRestriction != "USA, Canada, USA timezones" || j.Salary != "$170k - $200k" {
		t.Errorf("restrição/salário inesperados: %q / %q", j.LocationRestriction, j.Salary)
	}
	if j.FoundAt.Format("2006-01-02") != "2026-11-14" {
		t.Errorf("FoundAt = %v", j.FoundAt)
	}
	if j.Description == "" || strings.Contains(j.Description, "<p") {
		t.Errorf("descrição não normalizada")
	}
	if jobs[1].LocationRestriction != "LATAM, Europe, USA, Canada, APAC" {
		t.Errorf("LocationRestriction = %q", jobs[1].LocationRestriction)
	}
}
