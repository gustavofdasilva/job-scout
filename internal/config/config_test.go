package config

import (
	"os"
	"path/filepath"
	"slices"
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

// O example.config.yaml documenta os padrões: tem que carregar e trazer as
// mesmas listas usadas quando os campos são omitidos.
func TestExampleConfigMatchesDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join("..", "..", "example.config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cfg.TitleExclude, defaultTitleExclude) ||
		!slices.Equal(cfg.TitleRequire, defaultTitleRequire) ||
		!slices.Equal(cfg.PrioritySources, defaultPrioritySources) {
		t.Fatal("listas do example.config.yaml diferem dos padrões de config.go")
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
