package config

import (
	"os"
	"path/filepath"
	"testing"
)

func loadYAML(t *testing.T, body string) *Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestTitleFilterDefaults(t *testing.T) {
	cfg := loadYAML(t, "schedule: \"0 8 * * *\"\n")
	if len(cfg.TitleExclude) == 0 || len(cfg.TitleRequire) == 0 || len(cfg.PrioritySources) == 0 {
		t.Fatalf("omitidos deveriam usar os padrões: %+v", cfg)
	}

	cfg = loadYAML(t, "title_exclude: []\ntitle_require: []\ntitle_require_unused: 1\n")
	if len(cfg.TitleExclude) != 0 || len(cfg.TitleRequire) != 0 {
		t.Fatalf("lista vazia deveria desativar: exclude=%v require=%v", cfg.TitleExclude, cfg.TitleRequire)
	}

	cfg = loadYAML(t, "title_exclude: [\"sales\"]\n")
	if len(cfg.TitleExclude) != 1 || cfg.TitleExclude[0] != "sales" {
		t.Fatalf("lista explícita: %v", cfg.TitleExclude)
	}
}
