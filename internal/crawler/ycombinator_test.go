package crawler

import (
	"strings"
	"testing"
)

func TestParseYCList(t *testing.T) {
	postings, err := parseYCList(readFixture(t, "yc_list.html"))
	if err != nil {
		t.Fatal(err)
	}
	if len(postings) != 5 {
		t.Fatalf("esperava 5 vagas na lista, veio %d", len(postings))
	}

	// "backend" casa com o título/roleSpecificType da 1ª vaga; "node.js"
	// casa com as skills da 1ª e da 3ª.
	filtered := filterYCPostings(postings, []string{"backend", "node.js"})
	if len(filtered) != 2 {
		t.Fatalf("esperava 2 vagas filtradas, veio %d", len(filtered))
	}

	j := ycToJob(filtered[0])
	if j.Source != "ycombinator" || j.Title != "Backend Engineer" || j.Location != "Remote" {
		t.Errorf("campos básicos inesperados: %+v", j)
	}
	if j.URL != "https://www.ycombinator.com/companies/gogograndparent/jobs/2vbzAw8-backend-engineer" {
		t.Errorf("URL = %q", j.URL)
	}
	if j.LocationRestriction != "" {
		t.Errorf("'Remote' sem região não deveria gerar restrição: %q", j.LocationRestriction)
	}
	if !strings.Contains(j.Description, "Skills: Node.js") {
		t.Errorf("descrição deveria trazer os campos da lista: %q", j.Description)
	}

	if r := ycToJob(postings[3]).LocationRestriction; r != "FR, CH" {
		t.Errorf("LocationRestriction de 'Remote (FR; CH)' = %q", r)
	}
	if s := ycToJob(postings[1]); s.LocationRestriction != "US" {
		t.Errorf("LocationRestriction de 'Remote (US)' = %q", s.LocationRestriction)
	}
}

func TestParseYCDetail(t *testing.T) {
	job, err := parseYCDetail(readFixture(t, "yc_detail.html"))
	if err != nil {
		t.Fatal(err)
	}
	if job.Title != "Senior Software Engineer (Hybrid/San Francisco area)" || !strings.Contains(job.Description, "Pine Park Health") {
		t.Errorf("detalhe inesperado: título %q, descrição %q", job.Title, job.Description[:min(80, len(job.Description))])
	}

	if _, err := parseYCDetail([]byte("<html><body>sem dados</body></html>")); err == nil {
		t.Error("esperava erro sem data-page")
	}
}
