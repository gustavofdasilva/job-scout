package crawler

import (
	"strings"
	"testing"
)

func TestParseSmartRecruiters(t *testing.T) {
	postings, total, err := parseSmartRecruitersList(readFixture(t, "smartrecruiters_list.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(postings) != 5 || total != 5 {
		t.Fatalf("esperava 5 vagas, veio %d (total %d)", len(postings), total)
	}

	// 3 vagas de engenharia e 2 de social media.
	filtered := filterSmartRecruiters(postings, []string{"engineer"})
	if len(filtered) != 3 {
		t.Fatalf("esperava 3 vagas filtradas, veio %d", len(filtered))
	}

	detail, err := parseSmartRecruitersDetail(readFixture(t, "smartrecruiters_detail.json"))
	if err != nil {
		t.Fatal(err)
	}
	j := smartRecruitersToJob(filtered[0], "Wix2", &detail)
	if j.Source != "smartrecruiters" || j.Company != "Wix" || j.Title != "xEngineer (Backend Core)" {
		t.Errorf("campos básicos inesperados: %+v", j)
	}
	if j.URL != "https://jobs.smartrecruiters.com/Wix2/744000161753345-xengineer-backend-core-" {
		t.Errorf("URL deveria vir do postingUrl do detalhe: %q", j.URL)
	}
	if j.Location != "Kyiv, Ukraine · Hybrid · Full-time" {
		t.Errorf("Location = %q", j.Location)
	}
	if !strings.Contains(j.Description, "Job Description: As a backend-oriented xEngineer") || strings.Contains(j.Description, "<p>") {
		t.Errorf("descrição inesperada: %q", j.Description[:min(120, len(j.Description))])
	}

	// Sem detalhe: URL montada a partir do id e descrição vazia.
	noDetail := smartRecruitersToJob(filtered[1], "Wix2", nil)
	if noDetail.URL != "https://jobs.smartrecruiters.com/Wix2/744000160612109" || noDetail.Description != "" {
		t.Errorf("fallback sem detalhe inesperado: %+v", noDetail)
	}
}
