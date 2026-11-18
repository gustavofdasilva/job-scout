package crawler

import (
	"strings"
	"testing"
)

func TestParseJobicy(t *testing.T) {
	jobs, next, err := parseJobicy(readFixture(t, "jobicy.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 4 || next != "" {
		t.Fatalf("esperava 4 vagas e sem próxima página, veio %d (next=%q)", len(jobs), next)
	}

	j := jobs[0]
	if j.Title != "Senior SRE, Managed Gateways" || j.Company != "Kong Inc." || j.Source != "jobicy" {
		t.Errorf("campos básicos inesperados: %+v", j)
	}
	if j.LocationRestriction != "USA" {
		t.Errorf("LocationRestriction = %q", j.LocationRestriction)
	}
	if j.SalaryMin != 165000 || j.SalaryMax != 185000 || j.SalaryCurrency != "USD" || j.SalaryPeriod != "yearly" {
		t.Errorf("salário inesperado: %+v", j)
	}
	if strings.Contains(j.Description, "<p>") || j.Description == "" {
		t.Errorf("descrição não foi normalizada: %q", j.Description[:min(100, len(j.Description))])
	}
	if jobs[2].SalaryPeriod != "hourly" {
		t.Errorf("período da vaga por hora = %q", jobs[2].SalaryPeriod)
	}
}
