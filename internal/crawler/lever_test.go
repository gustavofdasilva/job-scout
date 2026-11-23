package crawler

import "testing"

func TestParseLeverDecimalSalary(t *testing.T) {
	jobs, err := parseLever(readFixture(t, "lever.json"), "zoox", []string{"backend"})
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 3 {
		t.Fatalf("esperava 3 vagas com \"backend\", veio %d", len(jobs))
	}

	yearly := jobs[0]
	if yearly.Company != "Zoox" || yearly.SalaryMin != 189000 || yearly.SalaryMax != 264500 ||
		yearly.SalaryPeriod != "yearly" || yearly.Salary != "$189k–$264.5k/yr USD" {
		t.Errorf("salário anual inesperado: %q %+v", yearly.Salary, yearly)
	}

	hourly := jobs[1]
	if hourly.SalaryMin != 25 || hourly.SalaryMax != 30 || hourly.SalaryCurrency != "USD" ||
		hourly.SalaryPeriod != "hourly" || hourly.Salary != "USD 24.5–30.25 / hourly" {
		t.Errorf("salário por hora inesperado: %q %+v", hourly.Salary, hourly)
	}

	if jobs[2].Salary != "" || jobs[2].SalaryMin != 0 {
		t.Errorf("vaga sem salaryRange não deveria ter salário: %+v", jobs[2])
	}
}

func TestSalaryHelpers(t *testing.T) {
	if got := salaryText("USD", 120000, 150000, "yearly"); got != "USD 120000–150000 / yearly" {
		t.Errorf("salaryText inteiro: %q", got)
	}
	if got := salaryText("EUR", 24.5, 30, ""); got != "EUR 24.5–30" {
		t.Errorf("salaryText decimal sem período: %q", got)
	}
	if salaryAmount(24.5) != 25 || salaryAmount(24.4) != 24 {
		t.Error("salaryAmount deveria arredondar")
	}
	if leverInterval("bi-week-salary") != "biweekly" || leverInterval("per-month-salary") != "monthly" {
		t.Error("leverInterval")
	}
}
