package crawler

import "testing"

func TestParseAshbyBoard(t *testing.T) {
	jobs, err := parseAshbyBoard(readFixture(t, "ashby.json"), "ramp", []string{"software engineer"})
	if err != nil {
		t.Fatal(err)
	}
	// A fixture tem 3 vagas "Software Engineer, ..." e 2 de vendas.
	if len(jobs) != 3 {
		t.Fatalf("esperava 3 vagas filtradas, veio %d", len(jobs))
	}

	j := jobs[0]
	if j.Title != "Software Engineer, Security, Cloud" {
		t.Errorf("título não foi aparado: %q", j.Title)
	}
	if j.Source != "ashby" || j.Company != "Ramp" || j.URL == "" || j.ID != hashID(j.URL) {
		t.Errorf("campos básicos inesperados: %+v", j)
	}
	if j.LocationRestriction != "Canada, US" {
		t.Errorf("LocationRestriction = %q", j.LocationRestriction)
	}
	if j.SalaryMin != 211400 || j.SalaryMax != 290600 || j.SalaryCurrency != "USD" || j.SalaryPeriod != "yearly" {
		t.Errorf("salário estruturado inesperado: %+v", j)
	}
	if j.Salary == "" || j.Description == "" {
		t.Errorf("salário/descrição vazios: %+v", j)
	}

	all, _ := parseAshbyBoard(readFixture(t, "ashby.json"), "ramp", nil)
	if len(all) != 5 {
		t.Errorf("sem termos deveria trazer as 5 vagas, veio %d", len(all))
	}
}

func TestExtractRemoteRegions(t *testing.T) {
	cases := map[string][]string{
		"Canada, US": {"New York", "Remote (Canada)", "Remote (US)", "Remote (us)"},
		"FR, CH":     {"Paris / Remote (FR; CH)"},
		"Germany":    {"Remote - Germany"},
		"":           {"San Francisco, CA", "Remote"},
	}
	for want, in := range cases {
		if got := extractRemoteRegions(in...); got != want {
			t.Errorf("extractRemoteRegions(%q) = %q, want %q", in, got, want)
		}
	}
}
