package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeYAML(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDefaults(t *testing.T) {
	cfg, err := Load(writeYAML(t, "schedule: \"0 8 * * *\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.JobFilter.Enabled || len(cfg.PrioritySources) == 0 || !cfg.JSONMode() {
		t.Fatalf("padrões inesperados: %+v", cfg)
	}
}

func TestLegacyTitleFilterRejected(t *testing.T) {
	_, err := Load(writeYAML(t, "title_exclude: [\"sales\"]\n"))
	if err == nil || !strings.Contains(err.Error(), "job_filter") {
		t.Fatalf("esperava erro apontando para job_filter, veio %v", err)
	}
}

func TestExampleConfig(t *testing.T) {
	cfg, err := Load(filepath.Join("..", "..", "example.config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.JobFilter.Enabled || len(cfg.JobFilter.RequirePatterns) == 0 {
		t.Fatalf("example.config.yaml deveria trazer job_filter ativo: %+v", cfg.JobFilter)
	}
	if !cfg.JSONMode() {
		t.Fatal("omniroute_json_mode deveria estar ligado no exemplo")
	}
	for _, c := range cfg.Crawlers {
		if c.Name == "indeed" && c.Enabled {
			t.Fatal("indeed deveria estar desativado no exemplo")
		}
	}
}
