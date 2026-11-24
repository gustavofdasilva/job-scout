package jobfilter_test

import (
	"path/filepath"
	"strings"
	"testing"

	"job-scout/internal/config"
	"job-scout/internal/jobfilter"
)

// exampleFilter usa o job_filter do example.config.yaml: os testes valem
// para as regex que de fato vão para produção.
func exampleFilter(t *testing.T) *jobfilter.Filter {
	t.Helper()
	cfg, err := config.Load(filepath.Join("..", "..", "example.config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	f, err := jobfilter.New(cfg.JobFilter)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestCheckPhrases(t *testing.T) {
	f := exampleFilter(t)
	cases := []struct {
		text   string
		pass   bool
		reason string
	}{
		{"Senior Golang Engineer", true, ""},
		{"Backend Engineer (Go)", true, ""},
		{"written in Go and Kafka", true, ""},
		{"Experience with Go, Rust or Java", true, ""},
		{"Stack: Python/Go", true, ""},
		{"experiência com Go", true, ""},

		{"Google Cloud engineer", false, jobfilter.ReasonNoMatch},
		{"Django backend", false, jobfilter.ReasonNoMatch},
		{"go-to-market strategy", false, jobfilter.ReasonNoMatch},
		{"Chicago office", false, jobfilter.ReasonNoMatch},
		{"we want a go-getter", false, jobfilter.ReasonNoMatch},
		{"5 years ago", false, jobfilter.ReasonNoMatch},
		{"let's go live in May", false, jobfilter.ReasonNoMatch},
		{"Go to our careers page", false, jobfilter.ReasonNoMatch},
	}
	for _, c := range cases {
		// A mesma frase tem que dar o mesmo resultado no título e na descrição.
		for _, in := range []struct{ where, title, desc string }{
			{"título", c.text, ""},
			{"descrição", "Software Engineer", c.text},
		} {
			reason, ok := f.Check(in.title, in.desc)
			if ok != c.pass || reason != c.reason {
				t.Errorf("%q no %s: ok=%v motivo=%q, esperava ok=%v motivo=%q", c.text, in.where, ok, reason, c.pass, c.reason)
			}
		}
	}
}

func TestCheckTitleExclude(t *testing.T) {
	f := exampleFilter(t)
	cases := []struct{ title, desc string }{
		{"Sales Engineer", "You will demo our platform, written in Go."},
		{"Engineering Manager, Platform", "Our stack is Golang."},
		{"Senior iOS Engineer", "Backend in Go."},
		{"Solutions Engineer", "Experience with Go."},
	}
	for _, c := range cases {
		if reason, ok := f.Check(c.title, c.desc); ok || reason != jobfilter.ReasonTitleExclude {
			t.Errorf("%q: ok=%v motivo=%q, esperava rejeição por title_exclude", c.title, ok, reason)
		}
	}
}

func TestCheckStripsHTML(t *testing.T) {
	f := exampleFilter(t)
	if _, ok := f.Check("Backend Engineer", "<p>Stack: Python&#47;Go</p>"); !ok {
		t.Error("entidade HTML deveria ser decodificada antes do teste")
	}
	if _, ok := f.Check("Backend Engineer", "<p>Services written&nbsp;in <b>Go</b>.</p>"); !ok {
		t.Error("descrição em HTML com \"in Go\" deveria passar")
	}
}

func TestNew(t *testing.T) {
	_, err := jobfilter.New(jobfilter.Config{Enabled: true, RequirePatterns: []string{`(?i)\bgo\b`}, IgnorePatterns: []string{`(unclosed`}})
	if err == nil || !strings.Contains(err.Error(), "ignore_patterns[0]") {
		t.Fatalf("regex inválida deveria apontar o campo e o índice, veio %v", err)
	}
	if _, err := jobfilter.New(jobfilter.Config{Enabled: true}); err == nil {
		t.Fatal("enabled sem require_patterns deveria ser erro")
	}

	off, err := jobfilter.New(jobfilter.Config{Enabled: false, RequirePatterns: []string{`golang`}})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := off.Check("Sales Director", ""); !ok || off.Enabled() {
		t.Fatal("filtro desativado deveria aprovar tudo")
	}
	var nilFilter *jobfilter.Filter
	if _, ok := nilFilter.Check("x", ""); !ok {
		t.Fatal("filtro nil deveria aprovar tudo")
	}
}
