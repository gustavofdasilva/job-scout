// Package analyzer usa um LLM (via gateway OmniRoute self-hosted, com API
// compatível com OpenAI) para avaliar o fit de vagas encontradas pelos
// crawlers contra o perfil do usuário.
package analyzer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"job-scout/internal/models"
)

const (
	maxTokens  = 4096
	maxRetries = 3
)

const systemPrompt = "Você é um especialista em recrutamento e análise de fit cultural/técnico.\n" +
	"Responda APENAS com JSON válido, sem markdown, sem explicações."

var httpClient = &http.Client{Timeout: 60 * time.Second}

// Analyzer usa um LLM exposto pelo gateway OmniRoute para analisar o fit de
// vagas contra o perfil do usuário.
type Analyzer struct {
	baseURL string
	apiKey  string
	model   string

	// jsonMode envia response_format {"type": "json_object"}. Desliga
	// sozinho (até reiniciar) se o gateway recusar o campo com 400.
	jsonMode atomic.Bool
}

// NewAnalyzer cria um novo Analyzer apontando para o endpoint OpenAI-compatible
// do OmniRoute (baseURL, ex: "http://omniroute:20128/v1"), com a chave de API
// e o modelo a serem usados. jsonMode liga o envio de response_format
// json_object (config omniroute_json_mode).
func NewAnalyzer(baseURL, apiKey, model string, jsonMode bool) *Analyzer {
	a := &Analyzer{baseURL: baseURL, apiKey: apiKey, model: model}
	a.jsonMode.Store(jsonMode)
	return a
}

// AnalyzeJob lê o perfil do usuário em profilePath (sem cachear) e usa o LLM
// configurado para avaliar o fit da vaga informada, retornando uma
// models.Analysis preenchida com o job_id já associado.
func (a *Analyzer) AnalyzeJob(profilePath string, job models.Job) (models.Analysis, error) {
	profileBytes, err := os.ReadFile(profilePath)
	if err != nil {
		return models.Analysis{}, fmt.Errorf("erro ao ler perfil %q: %w", profilePath, err)
	}

	prompt := buildPrompt(string(profileBytes), job)

	result, err := a.callWithRetry(prompt)
	if err != nil {
		return models.Analysis{}, fmt.Errorf("erro ao analisar vaga %q: %w", job.Title, err)
	}

	slog.Info("vaga analisada", "title", job.Title, "fit_score", result.FitScore)

	return models.Analysis{
		JobID:        job.ID,
		FitScore:     result.FitScore,
		Summary:      result.Summary,
		Benefits:     strings.Join(result.Benefits, "; "),
		FitReasoning: result.FitReasoning,
	}, nil
}

func buildPrompt(profile string, job models.Job) string {
	return fmt.Sprintf(`## Meu Perfil
%s

## Vaga
Título: %s
Empresa: %s
Localização: %s
Restrição de localização (países/regiões aceitos): %s
Salário: %s
Descrição: %s

## Regra de localização
Eu moro no Brasil. Se a vaga for remota mas restrita a países/regiões que NÃO
incluam Brasil, América Latina (LATAM) ou o mundo todo (Worldwide/Anywhere),
trate isso como ponto negativo explícito: reduza o fit_score e escreva
"vaga restrita a <região>" no fit_reasoning. Use a restrição de localização,
a localização e a descrição pra decidir. Se não houver informação sobre
restrição, NÃO penalize.

Retorne exatamente nesse formato:
{
  "fit_score": <0-100>,
  "summary": "<resumo da vaga em 3-4 linhas em PT-BR>",
  "benefits": ["<benefício 1>", "<benefício 2>"],
  "fit_reasoning": "<por que essa vaga é ou não é boa pra mim, em 2-3 linhas>"
}`, profile, job.Title, job.Company,
		orNotInformed(job.Location), orNotInformed(job.LocationRestriction), orNotInformed(jobSalary(job)),
		job.Description)
}

func orNotInformed(s string) string {
	if strings.TrimSpace(s) == "" {
		return "não informado"
	}
	return s
}

// jobSalary prefere o texto livre de Salary e, na falta dele, monta um
// texto a partir do salário estruturado (ex: "USD 120000–150000 / yearly").
func jobSalary(job models.Job) string {
	if job.Salary != "" {
		return job.Salary
	}
	if job.SalaryMin == 0 && job.SalaryMax == 0 {
		return ""
	}
	s := fmt.Sprintf("%s %d–%d", job.SalaryCurrency, job.SalaryMin, job.SalaryMax)
	if job.SalaryPeriod != "" {
		s += " / " + job.SalaryPeriod
	}
	return strings.TrimSpace(s)
}

// analysisResult é o formato esperado no JSON retornado pelo modelo.
type analysisResult struct {
	FitScore     int      `json:"fit_score"`
	Summary      string   `json:"summary"`
	Benefits     []string `json:"benefits"`
	FitReasoning string   `json:"fit_reasoning"`
}

type chatRequest struct {
	Model          string          `json:"model"`
	MaxTokens      int             `json:"max_tokens"`
	Messages       []chatMessage   `json:"messages"`
	ResponseFormat *responseFormat `json:"response_format,omitempty"`
}

type responseFormat struct {
	Type string `json:"type"`
}

// badRequestError é um 400 do gateway — usado pra detectar quando ele não
// aceita response_format.
type badRequestError struct {
	message string
}

func (e *badRequestError) Error() string {
	return fmt.Sprintf("status inesperado %d do omniroute: %s", http.StatusBadRequest, e.message)
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	Choices []struct {
		Message      chatMessage `json:"message"`
		FinishReason string      `json:"finish_reason"`
	} `json:"choices"`
}

type chatErrorResponse struct {
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// retryableError representa um erro transitório do gateway OmniRoute (rate
// limit ou erro de servidor) que justifica uma nova tentativa.
type retryableError struct {
	statusCode int
	message    string
}

func (e *retryableError) Error() string {
	return fmt.Sprintf("erro transitório do omniroute (status %d): %s", e.statusCode, e.message)
}

// callWithRetry chama o gateway OmniRoute, tentando novamente com backoff
// exponencial (e jitter aleatório) quando a resposta indica rate limit ou
// erro de servidor, até maxRetries tentativas.
func (a *Analyzer) callWithRetry(prompt string) (analysisResult, error) {
	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			delay := backoffDelay(attempt)
			slog.Warn("omniroute: erro transitório, aguardando antes de tentar novamente",
				"attempt", attempt+1, "delay", delay, "error", lastErr)
			time.Sleep(delay)
		}

		text, err := a.callAPIWithFallback(prompt)
		if err != nil {
			if rlErr, ok := err.(*retryableError); ok {
				lastErr = rlErr
				continue
			}
			return analysisResult{}, err
		}

		return parseResult(text)
	}

	return analysisResult{}, fmt.Errorf("esgotadas %d tentativas: %w", maxRetries, lastErr)
}

func backoffDelay(attempt int) time.Duration {
	base := time.Duration(1<<uint(attempt)) * time.Second
	jitter := time.Duration(rand.Int63n(int64(500 * time.Millisecond)))
	return base + jitter
}

// callAPIWithFallback chama o gateway com response_format (se ligado). Se
// ele responder 400 e a mesma chamada sem response_format funcionar, o
// campo é desligado para as próximas chamadas.
func (a *Analyzer) callAPIWithFallback(prompt string) (string, error) {
	if !a.jsonMode.Load() {
		return a.callAPI(prompt, false)
	}
	text, err := a.callAPI(prompt, true)
	var badReq *badRequestError
	if !errors.As(err, &badReq) {
		return text, err
	}
	text, retryErr := a.callAPI(prompt, false)
	if retryErr != nil {
		return "", err
	}
	if a.jsonMode.CompareAndSwap(true, false) {
		slog.Warn("omniroute: gateway recusou response_format json_object, desativado até reiniciar — configure omniroute_json_mode: false",
			"error", truncate(badReq.message, maxLoggedResponse))
	}
	return text, nil
}

func (a *Analyzer) callAPI(prompt string, jsonMode bool) (string, error) {
	reqBody := chatRequest{
		Model:     a.model,
		MaxTokens: maxTokens,
		Messages: []chatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: prompt},
		},
	}
	if jsonMode {
		reqBody.ResponseFormat = &responseFormat{Type: "json_object"}
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("erro ao serializar requisição: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, a.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("erro ao criar requisição: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.apiKey)

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("erro ao chamar api do omniroute: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("erro ao ler resposta do omniroute: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var apiErr chatErrorResponse
		_ = json.Unmarshal(respBody, &apiErr)
		msg := apiErr.Error.Message
		if msg == "" {
			msg = string(respBody)
		}

		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= http.StatusInternalServerError {
			return "", &retryableError{statusCode: resp.StatusCode, message: msg}
		}
		if resp.StatusCode == http.StatusBadRequest {
			return "", &badRequestError{message: msg}
		}
		return "", fmt.Errorf("status inesperado %d do omniroute: %s", resp.StatusCode, msg)
	}

	var parsed chatResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return "", fmt.Errorf("erro ao parsear resposta do omniroute: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("resposta do omniroute sem conteúdo")
	}

	choice := parsed.Choices[0]
	if choice.Message.Content == "" {
		return "", fmt.Errorf("resposta do omniroute vazia (finish_reason=%q) — se o modelo roteado for de raciocínio, os tokens de pensamento podem ter consumido todo o max_tokens=%d antes de gerar a resposta final", choice.FinishReason, maxTokens)
	}

	return choice.Message.Content, nil
}
