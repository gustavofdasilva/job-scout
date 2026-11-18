package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"job-scout/internal/analyzer"
	"job-scout/internal/checklist"
	"job-scout/internal/config"
	"job-scout/internal/crawler"
	"job-scout/internal/notifier"
	"job-scout/internal/resume"
	"job-scout/internal/scheduler"
	"job-scout/internal/server"
	"job-scout/internal/storage"
)

const resumeOutputDir = "data/resumes"

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	cfg, err := config.Load("config.yaml")
	if err != nil {
		slog.Error("falha ao carregar configuração", "error", err)
		os.Exit(1)
	}

	db, err := storage.Open("data/job-scout.db")
	if err != nil {
		slog.Error("falha ao abrir banco de dados", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Error("erro ao fechar banco de dados", "error", err)
		}
	}()

	slog.Info("job-scout inicializado com sucesso",
		"server_port", cfg.ServerPort,
		"min_fit_score", cfg.MinFitScore,
		"crawlers", len(cfg.Crawlers),
	)

	entries := buildCrawlerEntries(cfg.Crawlers, db)
	orchestrator := crawler.NewOrchestrator(entries)
	az := analyzer.NewAnalyzer(cfg.OmniRouteBaseURL, cfg.OmniRouteAPIKey, cfg.OmniRouteModel)
	resumeGen := resume.NewGenerator(cfg.OmniRouteBaseURL, cfg.OmniRouteAPIKey, cfg.OmniRouteModel, resumeOutputDir)
	checklistGen := checklist.NewGenerator(cfg.OmniRouteBaseURL, cfg.OmniRouteAPIKey, cfg.OmniRouteModel)
	telegram := notifier.NewTelegram(cfg.TelegramBotToken, cfg.TelegramChatID, cfg.TelegramMinScore)
	if !telegram.Enabled() {
		slog.Warn("telegram: bot_token ou chat_id não configurados — notificações desabilitadas")
	}

	sched := scheduler.NewScheduler(cfg, db, orchestrator, az, telegram)
	if err := sched.Run(); err != nil {
		slog.Error("falha ao iniciar scheduler", "error", err)
		os.Exit(1)
	}
	if next, err := sched.NextRun(); err != nil {
		slog.Warn("não foi possível calcular o próximo horário agendado", "error", err)
	} else {
		slog.Info("scheduler iniciado", "schedule", cfg.Schedule, "proxima_execucao", next)
	}

	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.ServerPort),
		Handler: server.New(db, sched, az, resumeGen, checklistGen, telegram).Handler(),
	}

	go func() {
		slog.Info("servidor http iniciado", "port", cfg.ServerPort)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("erro no servidor http", "error", err)
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh
	slog.Info("sinal de encerramento recebido, iniciando graceful shutdown")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("erro ao encerrar servidor http", "error", err)
	}

	<-sched.Stop().Done()

	slog.Info("aguardando eventuais ciclos do pipeline em execução terminarem")
	sched.Wait()

	slog.Info("job-scout encerrado")
}

// buildCrawlerEntries instancia os crawlers correspondentes às entradas
// habilitadas em config.yaml. db é usado só pelo crawler "hackernews", que
// consulta o banco pra evitar rodar mais de uma vez por semana.
func buildCrawlerEntries(crawlers []config.Crawler, db *storage.DB) []crawler.Entry {
	factories := map[string]func(c config.Crawler) crawler.Crawler{
		"gupy":           func(c config.Crawler) crawler.Crawler { return crawler.NewGupyCrawler() },
		"indeed":         func(c config.Crawler) crawler.Crawler { return crawler.NewIndeedCrawler() },
		"remoteok":       func(c config.Crawler) crawler.Crawler { return crawler.NewRemoteOKCrawler() },
		"programathor":   func(c config.Crawler) crawler.Crawler { return crawler.NewProgramaThorCrawler() },
		"trampos":        func(c config.Crawler) crawler.Crawler { return crawler.NewTramposCrawler() },
		"linkedin":       func(c config.Crawler) crawler.Crawler { return crawler.NewLinkedInCrawler() },
		"weworkremotely": func(c config.Crawler) crawler.Crawler { return crawler.NewWeWorkRemotelyCrawler(c.SearchTerms) },
		"lever":          func(c config.Crawler) crawler.Crawler { return crawler.NewLeverCrawler(c.Companies, c.SearchTerms) },
		"hackernews":     func(c config.Crawler) crawler.Crawler { return crawler.NewHackerNewsCrawler(db, c.SearchTerms) },
		"ashby":          func(c config.Crawler) crawler.Crawler { return crawler.NewAshbyCrawler(c.Companies, c.SearchTerms) },
		"greenhouse":     func(c config.Crawler) crawler.Crawler { return crawler.NewGreenhouseCrawler(c.Companies, c.SearchTerms) },
		"himalayas":      func(c config.Crawler) crawler.Crawler { return crawler.NewHimalayasCrawler() },
		"jobicy":         func(c config.Crawler) crawler.Crawler { return crawler.NewJobicyCrawler() },
		"getonbrd":       func(c config.Crawler) crawler.Crawler { return crawler.NewGetOnBoardCrawler() },
		"workable":       func(c config.Crawler) crawler.Crawler { return crawler.NewWorkableCrawler(c.Companies, c.SearchTerms) },
		"workingnomads":  func(c config.Crawler) crawler.Crawler { return crawler.NewWorkingNomadsCrawler(c.SearchTerms) },
		"arbeitnow":      func(c config.Crawler) crawler.Crawler { return crawler.NewArbeitnowCrawler(c.SearchTerms) },
		"ycombinator":    func(c config.Crawler) crawler.Crawler { return crawler.NewYCombinatorCrawler(c.SearchTerms) },
		"smartrecruiters": func(c config.Crawler) crawler.Crawler {
			return crawler.NewSmartRecruitersCrawler(c.Companies, c.SearchTerms)
		},
		// TODO: Wellfound (ex-AngelList) — não implementado: o site fica atrás
		// de Cloudflare pesado e não tem API pública.
	}

	var entries []crawler.Entry
	for _, c := range crawlers {
		if !c.Enabled {
			continue
		}
		factory, ok := factories[c.Name]
		if !ok {
			slog.Warn("crawler desconhecido na configuração, ignorando", "name", c.Name)
			continue
		}
		entries = append(entries, crawler.Entry{
			Crawler:     factory(c),
			SearchTerms: c.SearchTerms,
			MaxPages:    c.MaxPages,
			Companies:   c.Companies,
		})
	}
	return entries
}
