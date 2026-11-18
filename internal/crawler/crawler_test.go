package crawler

import (
	"os"
	"path/filepath"
	"testing"

	"job-scout/internal/models"
)

// readFixture lê um arquivo de testdata/ — as fixtures são respostas reais
// das fontes, capturadas uma vez, pra que os testes de parsing não usem rede.
func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("erro ao ler fixture %q: %v", name, err)
	}
	return b
}

func TestNormalizeURL(t *testing.T) {
	cases := map[string]string{
		"https://Jobs.Example.com/a/b/?utm_source=x&utm_medium=y": "https://jobs.example.com/a/b",
		"https://example.com/job/1?id=2&ref=hn#apply":             "https://example.com/job/1?id=2",
		"https://example.com/job/1":                               "https://example.com/job/1",
		"not a url":                                               "not a url",
	}
	for in, want := range cases {
		if got := normalizeURL(in); got != want {
			t.Errorf("normalizeURL(%q) = %q, want %q", in, got, want)
		}
	}
}

type fakeCrawler struct{ jobs []models.Job }

func (f fakeCrawler) Name() string                                { return "fake" }
func (f fakeCrawler) IgnoresSearchTerms() bool                    { return true }
func (f fakeCrawler) Fetch(_ string, _ int) ([]models.Job, error) { return f.jobs, nil }

func TestOrchestratorDedup(t *testing.T) {
	jobs := []models.Job{
		{URL: "https://a.com/1", Company: "Acme", Title: "Backend Engineer"},
		{URL: "https://a.com/1/?utm_source=x", Company: "Acme", Title: "Other"},    // mesma URL normalizada
		{URL: "https://board.com/99", Company: "ACME", Title: "Backend Engineer!"}, // mesma empresa+título
		{URL: "https://a.com/2", Company: "Acme", Title: "Frontend Engineer"},      // vaga diferente
		{URL: "", Company: "Acme", Title: "Sem URL"},                               // descartada
	}
	got, err := NewOrchestrator([]Entry{{Crawler: fakeCrawler{jobs: jobs}}}).Run()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].URL != "https://a.com/1" || got[1].URL != "https://a.com/2" {
		t.Fatalf("dedup inesperado: %+v", got)
	}
}
