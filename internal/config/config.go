package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Crawler representa a configuração de um crawler individual.
type Crawler struct {
	Name        string   `yaml:"name"`
	Enabled     bool     `yaml:"enabled"`
	SearchTerms []string `yaml:"search_terms"`
	MaxPages    int      `yaml:"max_pages"`
	// Companies é usado só pelo crawler "lever" — lista de slugs de
	// empresas (ex: "stripe") cujas boards públicas do Lever serão
	// consultadas, já que o Lever não tem busca global.
	Companies []string `yaml:"companies"`
}

// Config representa a configuração completa do job-scout.
type Config struct {
	OmniRouteBaseURL string    `yaml:"omniroute_base_url"`
	OmniRouteAPIKey  string    `yaml:"omniroute_api_key"`
	OmniRouteModel   string    `yaml:"omniroute_model"`
	Schedule         string    `yaml:"schedule"`
	MinFitScore      int       `yaml:"min_fit_score"`
	ServerPort       int       `yaml:"server_port"`
	Crawlers         []Crawler `yaml:"crawlers"`

	TelegramBotToken string `yaml:"telegram_bot_token"`
	TelegramChatID   string `yaml:"telegram_chat_id"`
	// TelegramMinScore é o fit_score mínimo (0-100) pra disparar uma
	// notificação no Telegram. Se omitido (zero), usa o mesmo valor de
	// MinFitScore.
	TelegramMinScore int `yaml:"telegram_min_score"`

	// MaxAnalysesPerCycle limita quantas vagas pendentes de análise (novas
	// ou que falharam em ciclos anteriores) são enviadas ao LLM por ciclo.
	// Se omitido (zero), usa defaultMaxAnalysesPerCycle; negativo desativa
	// o limite.
	MaxAnalysesPerCycle int `yaml:"max_analyses_per_cycle"`

	// MaxAnalysisAttempts é quantas vezes a análise de uma mesma vaga pode
	// falhar antes de o pipeline desistir dela. Se omitido (zero), usa
	// defaultMaxAnalysisAttempts; negativo desativa o limite.
	MaxAnalysisAttempts int `yaml:"max_analysis_attempts"`

	// TitleExclude e TitleRequire filtram vagas pelo título antes da
	// análise (ver internal/titlefilter): casar com algum padrão de
	// TitleExclude, ou não casar com nenhum de TitleRequire, marca a vaga
	// como "filtered" sem chamar o LLM. Omitidos, usam
	// defaultTitleExclude/defaultTitleRequire; uma lista vazia ([])
	// desativa a regra.
	TitleExclude []string `yaml:"title_exclude"`
	TitleRequire []string `yaml:"title_require"`

	// PrioritySources são as fontes (ATS e boards remotos) cujas vagas
	// pendentes vão primeiro para o LLM; dentro de cada grupo, as mais
	// recentes primeiro. Omitido, usa defaultPrioritySources.
	PrioritySources []string `yaml:"priority_sources"`
}

var (
	defaultTitleExclude = []string{
		"sales", "account executive", "account manager", "recruiter", "recruiting", "talent acquisition",
		"product manager", "marketing", "designer", "support specialist", "business development",
		"director", "head of", "android", "ios", "mobile",
	}
	defaultTitleRequire = []string{
		"engineer", "developer", "desenvolvedor", "desenvolvedora", "engenheiro", "engenheira",
		"programador", "programadora", "sre", "devops", "backend", "back-end", "platform",
	}
	defaultPrioritySources = []string{
		"ashby", "greenhouse", "lever", "workable", "smartrecruiters",
		"himalayas", "jobicy", "workingnomads", "arbeitnow", "remotive",
		"remoteok", "weworkremotely", "getonbrd", "ycombinator",
	}
)

const (
	defaultOmniRouteBaseURL = "http://localhost:20128/v1"
	defaultOmniRouteModel   = "claude-haiku-4-5"

	defaultMaxAnalysesPerCycle = 50
	defaultMaxAnalysisAttempts = 3
)

// Load lê e faz o parse do arquivo de configuração YAML no caminho informado.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("erro ao ler arquivo de config %q: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("erro ao fazer parse do config %q: %w", path, err)
	}

	if cfg.OmniRouteBaseURL == "" {
		cfg.OmniRouteBaseURL = defaultOmniRouteBaseURL
	}
	if cfg.OmniRouteModel == "" {
		cfg.OmniRouteModel = defaultOmniRouteModel
	}
	if cfg.TelegramMinScore == 0 {
		cfg.TelegramMinScore = cfg.MinFitScore
	}
	if cfg.MaxAnalysesPerCycle == 0 {
		cfg.MaxAnalysesPerCycle = defaultMaxAnalysesPerCycle
	}
	if cfg.MaxAnalysisAttempts == 0 {
		cfg.MaxAnalysisAttempts = defaultMaxAnalysisAttempts
	}
	// nil = campo omitido no YAML; "[]" chega como slice vazio e desativa.
	if cfg.TitleExclude == nil {
		cfg.TitleExclude = defaultTitleExclude
	}
	if cfg.TitleRequire == nil {
		cfg.TitleRequire = defaultTitleRequire
	}
	if cfg.PrioritySources == nil {
		cfg.PrioritySources = defaultPrioritySources
	}

	return &cfg, nil
}
