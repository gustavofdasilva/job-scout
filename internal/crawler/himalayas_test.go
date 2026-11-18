package crawler

import "testing"

func TestParseHimalayas(t *testing.T) {
	jobs, total, err := parseHimalayas(readFixture(t, "himalayas.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 4 || total != 178 {
		t.Fatalf("esperava 4 vagas (total 178), veio %d (total %d)", len(jobs), total)
	}

	j := jobs[1]
	if j.Title != "Golang Developer" || j.Company != "Pontoonglobal" || j.Source != "himalayas" {
		t.Errorf("campos básicos inesperados: %+v", j)
	}
	if j.URL != "https://himalayas.app/companies/pontoonglobal/jobs/golang-developer" {
		t.Errorf("URL deveria ser o guid sem utm: %q", j.URL)
	}
	if j.LocationRestriction != "United States" {
		t.Errorf("LocationRestriction = %q", j.LocationRestriction)
	}
	if j.SalaryMin != 600000 || j.SalaryMax != 1500000 || j.SalaryCurrency != "INR" || j.SalaryPeriod != "annual" {
		t.Errorf("salário inesperado: %+v", j)
	}

	if jobs[0].SalaryMin != 0 || jobs[0].Salary != "" {
		t.Errorf("vaga sem salário não deveria ter salário: %+v", jobs[0])
	}
	if jobs[3].LocationRestriction != "Worldwide" {
		t.Errorf("restrição vazia deveria virar Worldwide, veio %q", jobs[3].LocationRestriction)
	}
}
