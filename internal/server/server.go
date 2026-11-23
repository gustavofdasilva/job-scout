// Package server expõe a API HTTP e o dashboard estático do job-scout.
package server

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"job-scout/internal/analyzer"
	"job-scout/internal/checklist"
	"job-scout/internal/notifier"
	"job-scout/internal/resume"
	"job-scout/internal/scheduler"
	"job-scout/internal/storage"
)

const profilePath = "profile.md"

//go:embed static
var staticFiles embed.FS

const resumeDir = "data/resumes"

var validStatuses = map[string]bool{
	"new":      true,
	"reviewed": true,
	"applied":  true,
	"ignored":  true,
	// Descartada pelo filtro de título (title_exclude/title_require).
	"filtered": true,
}

// Server expõe a API HTTP e o dashboard do job-scout.
type Server struct {
	db           *storage.DB
	scheduler    *scheduler.Scheduler
	analyzer     *analyzer.Analyzer
	resumeGen    *resume.Generator
	checklistGen *checklist.Generator
	telegram     *notifier.Telegram
	mux          *http.ServeMux
}

// New cria um Server com as rotas da API e os arquivos estáticos do
// dashboard já registrados.
func New(db *storage.DB, sched *scheduler.Scheduler, az *analyzer.Analyzer, resumeGen *resume.Generator, checklistGen *checklist.Generator, telegram *notifier.Telegram) *Server {
	s := &Server{db: db, scheduler: sched, analyzer: az, resumeGen: resumeGen, checklistGen: checklistGen, telegram: telegram, mux: http.NewServeMux()}
	s.routes()
	return s
}

// Handler retorna o http.Handler completo do servidor.
func (s *Server) Handler() http.Handler {
	return s.mux
}

func (s *Server) routes() {
	static, err := fs.Sub(staticFiles, "static")
	if err != nil {
		panic(err)
	}
	s.mux.Handle("/", http.FileServer(http.FS(static)))

	s.mux.HandleFunc("GET /api/jobs", s.handleListJobs)
	s.mux.HandleFunc("GET /api/jobs/counts", s.handleJobCounts)
	s.mux.HandleFunc("GET /api/jobs/{id}", s.handleGetJob)
	s.mux.HandleFunc("POST /api/jobs/{id}/analyze", s.handleAnalyzeJob)
	s.mux.HandleFunc("GET /api/jobs/{id}/resume", s.handleGetResume)
	s.mux.HandleFunc("POST /api/jobs/{id}/resume", s.handleGenerateResume)
	s.mux.HandleFunc("POST /api/jobs/{id}/checklist", s.handleGenerateChecklist)
	s.mux.HandleFunc("POST /api/jobs/{id}/status", s.handleUpdateStatus)
	s.mux.HandleFunc("POST /api/trigger", s.handleTrigger)
	s.mux.HandleFunc("GET /api/status", s.handleStatus)
	s.mux.HandleFunc("GET /api/ping", s.handlePing)
}

// handlePing responde a checagens simples de disponibilidade do servidor.
func (s *Server) handlePing(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"message": "pong"})
}

const (
	defaultJobsPageSize = 50
	maxJobsPageSize     = 200
)

// parseJobFilter lê os filtros e a paginação de /api/jobs e
// /api/jobs/counts: status, min_score, source, limit e offset.
func parseJobFilter(r *http.Request) (storage.JobFilter, error) {
	q := r.URL.Query()
	f := storage.JobFilter{
		Status: q.Get("status"),
		Source: q.Get("source"),
		Limit:  defaultJobsPageSize,
	}
	if f.Status != "" && !validStatuses[f.Status] {
		return f, errors.New("status inválido")
	}

	ints := []struct {
		name     string
		dst      *int
		min, max int
	}{
		{"min_score", &f.MinScore, 0, 100},
		{"limit", &f.Limit, 1, maxJobsPageSize},
		{"offset", &f.Offset, 0, math.MaxInt32},
	}
	for _, p := range ints {
		raw := q.Get(p.name)
		if raw == "" {
			continue
		}
		v, err := strconv.Atoi(raw)
		if err != nil || v < p.min || v > p.max {
			return f, fmt.Errorf("%s inválido", p.name)
		}
		*p.dst = v
	}
	return f, nil
}

// handleListJobs devolve uma página de vagas (array JSON) ordenada por
// fit_score e found_at decrescentes. O total de vagas que passam pelos
// filtros vai no header X-Total-Count.
func (s *Server) handleListJobs(w http.ResponseWriter, r *http.Request) {
	filter, err := parseJobFilter(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	jobs, total, err := s.db.GetJobsWithAnalysis(filter)
	if err != nil {
		slog.Error("server: erro ao buscar vagas", "error", err)
		writeError(w, http.StatusInternalServerError, "erro ao buscar vagas")
		return
	}

	w.Header().Set("X-Total-Count", strconv.Itoa(total))
	writeJSON(w, http.StatusOK, jobs)
}

// handleJobCounts devolve a contagem de vagas por status com os mesmos
// filtros de score/fonte da listagem (os números das abas) e as fontes
// existentes (opções do filtro de fonte).
func (s *Server) handleJobCounts(w http.ResponseWriter, r *http.Request) {
	filter, err := parseJobFilter(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	counts, err := s.db.CountJobsWithAnalysisByStatus(filter)
	if err != nil {
		slog.Error("server: erro ao contar vagas", "error", err)
		writeError(w, http.StatusInternalServerError, "erro ao contar vagas")
		return
	}
	sources, err := s.db.ListSources()
	if err != nil {
		slog.Error("server: erro ao listar fontes", "error", err)
		writeError(w, http.StatusInternalServerError, "erro ao listar fontes")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"by_status": counts,
		"sources":   sources,
	})
}

func (s *Server) handleGetJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	job, analysis, err := s.db.GetJobByID(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "vaga não encontrada")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"job":      job,
		"analysis": analysis,
	})
}

// handleAnalyzeJob dispara manualmente a análise de fit de uma vaga —
// primeira análise (se ela nunca foi analisada, por exemplo por falha na
// etapa automática) ou reanálise (atualiza fit_score/summary/benefits/
// fit_reasoning de uma análise já existente, preservando currículo/checklist
// já gerados pra ela).
func (s *Server) handleAnalyzeJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	job, existing, err := s.db.GetJobByID(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "vaga não encontrada")
		return
	}

	analysis, err := s.analyzer.AnalyzeJob(profilePath, job)
	if err != nil {
		slog.Warn("server: falha ao analisar vaga", "title", job.Title, "error", err)
		writeError(w, http.StatusBadGateway, "falha ao analisar vaga")
		return
	}

	if existing.ID != "" {
		analysis.ID = existing.ID
		if err := s.db.UpdateAnalysisResult(existing.ID, analysis); err != nil {
			slog.Error("server: erro ao atualizar análise", "error", err)
			writeError(w, http.StatusInternalServerError, "erro ao salvar análise")
			return
		}
	} else {
		analysisID, err := s.db.InsertAnalysis(analysis)
		if err != nil {
			slog.Error("server: erro ao salvar análise", "error", err)
			writeError(w, http.StatusInternalServerError, "erro ao salvar análise")
			return
		}
		analysis.ID = analysisID
	}

	s.telegram.NotifyJobAnalyzed(job, analysis)

	updated, err := s.db.GetJobWithAnalysisByID(id)
	if err != nil {
		slog.Error("server: erro ao buscar vaga atualizada", "error", err)
		writeError(w, http.StatusInternalServerError, "erro ao buscar vaga atualizada")
		return
	}

	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleGetResume(w http.ResponseWriter, r *http.Request) {
	id := sanitizeID(r.PathValue("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "id inválido")
		return
	}

	pdfPath := filepath.Join(resumeDir, id+".pdf")
	if _, err := os.Stat(pdfPath); err == nil {
		http.ServeFile(w, r, pdfPath)
		return
	}

	mdPath := filepath.Join(resumeDir, id+".md")
	data, err := os.ReadFile(mdPath)
	if err != nil {
		writeError(w, http.StatusNotFound, "currículo não encontrado")
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(data); err != nil {
		slog.Warn("server: erro ao enviar currículo em markdown", "id", id, "error", err)
	}
}

// handleGenerateResume gera sob demanda o currículo completo (Markdown/PDF)
// de uma vaga já analisada e persiste o caminho gerado na análise.
func (s *Server) handleGenerateResume(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	job, analysis, err := s.db.GetJobByID(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "vaga não encontrada")
		return
	}
	if analysis.ID == "" {
		writeError(w, http.StatusConflict, "vaga ainda não foi analisada")
		return
	}

	profileBytes, err := os.ReadFile(profilePath)
	if err != nil {
		slog.Error("server: erro ao ler perfil", "error", err)
		writeError(w, http.StatusInternalServerError, "erro ao ler perfil")
		return
	}

	path, err := s.resumeGen.Generate(string(profileBytes), job, analysis)
	if err != nil {
		slog.Warn("server: falha ao gerar currículo", "title", job.Title, "error", err)
		writeError(w, http.StatusBadGateway, "falha ao gerar currículo")
		return
	}

	if err := s.db.UpdateResumePath(analysis.ID, path); err != nil {
		slog.Error("server: erro ao salvar caminho do currículo", "error", err)
		writeError(w, http.StatusInternalServerError, "erro ao salvar currículo gerado")
		return
	}

	updated, err := s.db.GetJobWithAnalysisByID(id)
	if err != nil {
		slog.Error("server: erro ao buscar vaga atualizada", "error", err)
		writeError(w, http.StatusInternalServerError, "erro ao buscar vaga atualizada")
		return
	}

	writeJSON(w, http.StatusOK, updated)
}

// handleGenerateChecklist gera sob demanda o checklist de palavras-chave e
// pontos a destacar no currículo de uma vaga já analisada, e persiste o
// resultado na análise.
func (s *Server) handleGenerateChecklist(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	job, analysis, err := s.db.GetJobByID(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "vaga não encontrada")
		return
	}
	if analysis.ID == "" {
		writeError(w, http.StatusConflict, "vaga ainda não foi analisada")
		return
	}

	profileBytes, err := os.ReadFile(profilePath)
	if err != nil {
		slog.Error("server: erro ao ler perfil", "error", err)
		writeError(w, http.StatusInternalServerError, "erro ao ler perfil")
		return
	}

	result, err := s.checklistGen.Generate(string(profileBytes), job)
	if err != nil {
		slog.Warn("server: falha ao gerar checklist de currículo", "title", job.Title, "error", err)
		writeError(w, http.StatusBadGateway, "falha ao gerar checklist de currículo")
		return
	}

	keywords := strings.Join(result.Keywords, "; ")
	points := strings.Join(result.Points, "; ")
	if err := s.db.UpdateResumeChecklist(analysis.ID, keywords, points); err != nil {
		slog.Error("server: erro ao salvar checklist de currículo", "error", err)
		writeError(w, http.StatusInternalServerError, "erro ao salvar checklist gerado")
		return
	}

	updated, err := s.db.GetJobWithAnalysisByID(id)
	if err != nil {
		slog.Error("server: erro ao buscar vaga atualizada", "error", err)
		writeError(w, http.StatusInternalServerError, "erro ao buscar vaga atualizada")
		return
	}

	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleUpdateStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var body struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "corpo da requisição inválido")
		return
	}
	if !validStatuses[body.Status] {
		writeError(w, http.StatusBadRequest, "status inválido")
		return
	}

	if err := s.db.UpdateJobStatus(id, body.Status); err != nil {
		writeError(w, http.StatusNotFound, "vaga não encontrada")
		return
	}

	job, _, err := s.db.GetJobByID(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "erro ao buscar vaga atualizada")
		return
	}

	writeJSON(w, http.StatusOK, job)
}

func (s *Server) handleTrigger(w http.ResponseWriter, r *http.Request) {
	go s.scheduler.RunNow()
	writeJSON(w, http.StatusAccepted, map[string]string{"message": "pipeline iniciado"})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	counts, err := s.db.CountJobsByStatus()
	if err != nil {
		slog.Error("server: erro ao contar vagas por status", "error", err)
		writeError(w, http.StatusInternalServerError, "erro ao buscar status")
		return
	}

	resp := map[string]any{
		"counts_by_status": counts,
		"last_run":         nil,
		"next_run":         nil,
	}

	if last, ok := s.scheduler.LastRun(); ok {
		resp["last_run"] = last.Format(time.RFC3339)
	}
	if next, err := s.scheduler.NextRun(); err == nil {
		resp["next_run"] = next.Format(time.RFC3339)
	}

	writeJSON(w, http.StatusOK, resp)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("server: erro ao serializar resposta json", "error", err)
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

// sanitizeID reduz o id ao seu nome base, prevenindo path traversal ao
// montar caminhos de arquivo a partir de um valor vindo da URL.
func sanitizeID(id string) string {
	base := filepath.Base(id)
	if base == "." || base == string(filepath.Separator) || strings.ContainsAny(id, `/\`) {
		return ""
	}
	return base
}
