// Package scheduler agenda e executa o pipeline completo do job-scout
// (crawl -> dedup -> análise de fit -> notificação) em um horário
// configurável. Geração de currículo completo e de checklist são ações
// manuais, disparadas pelo dashboard (ver internal/server).
package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"job-scout/internal/analyzer"
	"job-scout/internal/config"
	"job-scout/internal/crawler"
	"job-scout/internal/jobfilter"
	"job-scout/internal/models"
	"job-scout/internal/notifier"
	"job-scout/internal/storage"
)

// Scheduler agenda a execução periódica do pipeline de acordo com a
// expressão cron configurada.
type Scheduler struct {
	cfg          *config.Config
	db           *storage.DB
	orchestrator *crawler.Orchestrator
	analyzer     *analyzer.Analyzer
	telegram     *notifier.Telegram
	jobFilter    *jobfilter.Filter

	cron *cron.Cron
	wg   sync.WaitGroup

	mu      sync.Mutex
	running bool
	lastRun time.Time
}

// NewScheduler cria um Scheduler com as dependências do pipeline já
// resolvidas.
func NewScheduler(cfg *config.Config, db *storage.DB, orchestrator *crawler.Orchestrator, az *analyzer.Analyzer, telegram *notifier.Telegram, jobFilter *jobfilter.Filter) *Scheduler {
	return &Scheduler{
		cfg:          cfg,
		db:           db,
		orchestrator: orchestrator,
		analyzer:     az,
		telegram:     telegram,
		jobFilter:    jobFilter,
		cron:         cron.New(),
	}
}

// FilterStats conta, por fonte, quantas vagas passaram no job_filter e
// quantas foram filtradas.
type FilterStats struct {
	Passed   map[string]int
	Filtered map[string]int
}

func newFilterStats() FilterStats {
	return FilterStats{Passed: map[string]int{}, Filtered: map[string]int{}}
}

// Total devolve o total de vagas que passaram e que foram filtradas.
func (st FilterStats) Total() (passed, filtered int) {
	for _, n := range st.Passed {
		passed += n
	}
	for _, n := range st.Filtered {
		filtered += n
	}
	return passed, filtered
}

// applyFilter aplica o job_filter à vaga: se ela for rejeitada, muda o
// status para "filtrada" e grava o motivo. Devolve se ela passou.
func (s *Scheduler) applyFilter(j *models.Job, st FilterStats) bool {
	reason, ok := s.jobFilter.Check(j.Title, j.Description)
	if ok {
		st.Passed[j.Source]++
		return true
	}
	j.Status = models.JobStatusFiltered
	j.FilterReason = reason
	st.Filtered[j.Source]++
	slog.Debug("pipeline: vaga filtrada", "source", j.Source, "title", j.Title, "motivo", reason)
	return false
}

// FilterPendingJobs aplica o job_filter às vagas que já estavam no banco
// com status "new" e sem análise, marcando como "filtrada" as que não
// passam. Roda na subida do servidor, o que cobre as vagas gravadas antes
// do filtro existir ou de uma mudança nas regex; as vagas novas de cada
// ciclo são filtradas na inserção. Devolve as contagens por fonte e
// quantas vagas continuam pendentes de análise.
func (s *Scheduler) FilterPendingJobs() (FilterStats, int, error) {
	st := newFilterStats()
	if s.jobFilter.Enabled() {
		candidates, err := s.db.GetUnanalyzedNewJobs()
		if err != nil {
			return st, 0, err
		}
		reasons := map[string]string{}
		for i := range candidates {
			if !s.applyFilter(&candidates[i], st) {
				reasons[candidates[i].ID] = candidates[i].FilterReason
			}
		}
		if _, err := s.db.MarkJobsFiltered(reasons); err != nil {
			return st, 0, err
		}
	}
	pending, err := s.db.CountPendingAnalysisJobs(s.cfg.MaxAnalysisAttempts)
	if err != nil {
		return st, 0, err
	}
	return st, pending, nil
}

// Run registra a expressão cron do config e inicia o agendador em segundo
// plano. Não bloqueia.
func (s *Scheduler) Run() error {
	if _, err := s.cron.AddFunc(s.cfg.Schedule, s.runPipeline); err != nil {
		return fmt.Errorf("erro ao registrar expressão cron %q: %w", s.cfg.Schedule, err)
	}
	s.cron.Start()
	return nil
}

// Stop impede novos disparos agendados pelo cron. Não espera por ciclos em
// execução — use Wait para isso. O context retornado é encerrado quando o
// próprio cron termina de parar.
func (s *Scheduler) Stop() context.Context {
	return s.cron.Stop()
}

// Wait bloqueia até que qualquer ciclo do pipeline em execução (disparado
// pelo cron ou via RunNow) termine. Deve ser chamado durante o graceful
// shutdown, antes de fechar o banco de dados, para garantir que nenhuma
// goroutine de pipeline continue rodando (e escrevendo no banco) depois que
// o processo começa a encerrar.
func (s *Scheduler) Wait() {
	s.wg.Wait()
}

// RunNow dispara o pipeline imediatamente, fora do horário agendado — usado
// pelo disparo manual via API. Se um ciclo já estiver em execução (agendado
// ou manual), o disparo é ignorado.
func (s *Scheduler) RunNow() {
	s.runPipeline()
}

// NextRun calcula o próximo horário em que o pipeline será executado de
// acordo com a expressão cron configurada.
func (s *Scheduler) NextRun() (time.Time, error) {
	schedule, err := cron.ParseStandard(s.cfg.Schedule)
	if err != nil {
		return time.Time{}, fmt.Errorf("erro ao parsear expressão cron %q: %w", s.cfg.Schedule, err)
	}
	return schedule.Next(time.Now()), nil
}

// LastRun retorna o horário de início do último ciclo concluído do pipeline.
// O segundo valor é false se o pipeline ainda não rodou nenhuma vez.
func (s *Scheduler) LastRun() (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastRun, !s.lastRun.IsZero()
}

// registerAnalysisFailure contabiliza uma falha de análise da vaga e avisa
// quando ela atinge o limite de tentativas — a partir daí o pipeline deixa
// de retentá-la (ainda dá pra analisar manualmente pelo dashboard).
func (s *Scheduler) registerAnalysisFailure(j models.Job, analyzeErr error) {
	attempts, err := s.db.IncrementAnalysisAttempts(j.ID)
	if err != nil {
		slog.Warn("pipeline: falha ao analisar vaga", "title", j.Title, "error", analyzeErr)
		slog.Error("pipeline: falha ao registrar tentativa de análise", "title", j.Title, "error", err)
		return
	}

	slog.Warn("pipeline: falha ao analisar vaga",
		"title", j.Title,
		"tentativa", attempts,
		"max_tentativas", s.cfg.MaxAnalysisAttempts,
		"error", analyzeErr,
	)
	if s.cfg.MaxAnalysisAttempts > 0 && attempts >= s.cfg.MaxAnalysisAttempts {
		slog.Warn("pipeline: limite de tentativas atingido, vaga não será mais reanalisada automaticamente",
			"title", j.Title, "url", j.URL, "tentativas", attempts)
	}
}

// runPipeline executa um ciclo completo: crawl de todas as fontes
// habilitadas, dedup e persistência das vagas novas, análise de fit via LLM
// e notificação no Telegram das vagas que atingirem o fit_score mínimo
// configurado. Currículo completo e checklist são gerados manualmente,
// depois, pelo dashboard.
func (s *Scheduler) runPipeline() {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		slog.Warn("pipeline: ciclo já em execução, ignorando novo disparo")
		return
	}
	s.running = true
	s.mu.Unlock()

	s.wg.Add(1)
	defer func() {
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
		s.wg.Done()
	}()

	start := time.Now()
	slog.Info("pipeline: iniciando ciclo")

	jobs, err := s.orchestrator.Run()
	if err != nil {
		slog.Error("pipeline: falha ao executar crawlers", "error", err)
		return
	}

	bySource := make(map[string]int)
	filterStats := newFilterStats()
	var novas int
	for _, j := range jobs {
		bySource[j.Source]++
		// Dedup por URL e por empresa+título: a mesma vaga pode aparecer no
		// ATS da empresa e em um ou mais boards remotos, com URLs diferentes.
		if j.URL == "" || s.db.JobExistsByURL(j.URL) || s.db.JobExistsByDedupKey(j.DedupKey()) {
			continue
		}
		// Vagas que não passam no job_filter são gravadas mesmo assim, já
		// como "filtrada": ficam visíveis na aba Filtradas, contam pro dedup
		// e nunca vão pro LLM.
		if s.jobFilter.Enabled() {
			s.applyFilter(&j, filterStats)
		}
		if err := s.db.InsertJob(j); err != nil {
			slog.Warn("pipeline: falha ao salvar vaga", "url", j.URL, "error", err)
			continue
		}
		novas++
	}
	slog.Info("pipeline: crawlers concluídos", "total_vagas", len(jobs), "novas_vagas", novas, "por_fonte", bySource)

	var aprovadas, filtradas int
	if s.jobFilter.Enabled() {
		aprovadas, filtradas = filterStats.Total()
		slog.Info("pipeline: job_filter aplicado às vagas novas",
			"aprovadas", aprovadas,
			"filtradas", filtradas,
			"aprovadas_por_fonte", filterStats.Passed,
			"filtradas_por_fonte", filterStats.Filtered,
		)
	}

	var pendentes, analisadas, comFitAlto int

	if s.cfg.OmniRouteAPIKey == "" {
		slog.Warn("pipeline: omniroute_api_key não configurada — pulando análise")
	} else {
		// Analisa toda vaga sem análise no banco, não só as inseridas neste
		// ciclo: assim vagas cuja análise falhou antes (erro do LLM, API key
		// ausente etc.) são retentadas em vez de ficarem órfãs para sempre.
		pending, err := s.db.GetPendingAnalysisJobs(s.cfg.MaxAnalysesPerCycle, s.cfg.MaxAnalysisAttempts, s.cfg.PrioritySources)
		if err != nil {
			slog.Error("pipeline: falha ao buscar vagas pendentes de análise", "error", err)
		}
		pendentes = len(pending)
		slog.Info("pipeline: analisando vagas pendentes",
			"pendentes", pendentes,
			"limite_por_ciclo", s.cfg.MaxAnalysesPerCycle,
			"max_tentativas", s.cfg.MaxAnalysisAttempts,
		)

		for _, j := range pending {
			analysis, err := s.analyzer.AnalyzeJob("profile.md", j)
			if err != nil {
				s.registerAnalysisFailure(j, err)
				continue
			}
			id, err := s.db.InsertAnalysis(analysis)
			if err != nil {
				slog.Warn("pipeline: falha ao salvar análise", "title", j.Title, "error", err)
				continue
			}
			analysis.ID = id

			analisadas++
			if analysis.FitScore >= s.cfg.MinFitScore {
				comFitAlto++
			}

			s.telegram.NotifyJobAnalyzed(j, analysis)
		}
	}

	s.mu.Lock()
	s.lastRun = start
	s.mu.Unlock()

	slog.Info("pipeline: ciclo concluído",
		"duracao", time.Since(start),
		"vagas_encontradas", len(jobs),
		"vagas_novas", novas,
		"vagas_filtradas", filtradas,
		"vagas_pendentes", pendentes,
		"vagas_analisadas", analisadas,
		"vagas_com_fit_alto", comFitAlto,
	)
}
