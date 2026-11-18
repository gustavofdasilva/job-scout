package crawler

import (
	"fmt"
	"strings"
	"testing"
)

func TestParseGetOnBoard(t *testing.T) {
	jobs, totalPages, err := parseGetOnBoard(readFixture(t, "getonbrd.json"))
	if err != nil {
		t.Fatal(err)
	}
	// Fixture: 2 fully_remote, 1 remote_local e 1 híbrida no Chile (descartada).
	if len(jobs) != 3 {
		t.Fatalf("esperava 3 vagas remotas, veio %d", len(jobs))
	}
	if totalPages < 1 {
		t.Errorf("total_pages = %d", totalPages)
	}

	j := jobs[0]
	if j.Source != "getonbrd" || j.Company == "" || !strings.HasPrefix(j.URL, "https://www.getonbrd.com/jobs/") {
		t.Errorf("campos básicos inesperados: %+v", j)
	}
	if j.LocationRestriction != "Worldwide" {
		t.Errorf("fully_remote deveria virar Worldwide, veio %q", j.LocationRestriction)
	}
	if j.Description == "" || strings.Contains(j.Description, "<li>") {
		t.Errorf("descrição não normalizada: %q", j.Description[:min(100, len(j.Description))])
	}
	if s := jobs[1]; s.SalaryMin != 3000 || s.SalaryMax != 5000 || s.SalaryCurrency != "USD" || s.SalaryPeriod != "monthly" {
		t.Errorf("salário inesperado: %+v", s)
	}
	if !strings.Contains(jobs[2].LocationRestriction, "residentes") {
		t.Errorf("remote_local deveria indicar restrição de país, veio %q", jobs[2].LocationRestriction)
	}
}

func TestGetOnBoardURLEncodesExpand(t *testing.T) {
	u := fmt.Sprintf(getOnBoardSearchURLTemplate, "backend", 1)
	if !strings.HasSuffix(u, "&expand=%5B%22company%22%5D") {
		t.Errorf("expand precisa ir codificado (a API responde 400 sem isso): %s", u)
	}
}
