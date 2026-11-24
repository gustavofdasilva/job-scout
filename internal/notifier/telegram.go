// Package notifier envia notificações via Telegram quando uma vaga
// analisada atinge o fit_score mínimo configurado.
package notifier

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"job-scout/internal/models"
)

const telegramAPIURL = "https://api.telegram.org"

var httpClient = &http.Client{Timeout: 15 * time.Second}

// Telegram envia notificações de vagas com fit_score alto para um chat do
// Telegram via bot.
type Telegram struct {
	botToken string
	chatID   string
	minScore int

	// apiURL e sleep são trocados nos testes.
	apiURL string
	sleep  func(time.Duration)
}

// NewTelegram cria um notifier do Telegram. Se botToken ou chatID estiverem
// vazios, NotifyJobAnalyzed não faz nada (notificações desabilitadas).
func NewTelegram(botToken, chatID string, minScore int) *Telegram {
	return &Telegram{botToken: botToken, chatID: chatID, minScore: minScore, apiURL: telegramAPIURL, sleep: time.Sleep}
}

// Enabled indica se o notifier está configurado (bot token e chat id presentes).
func (t *Telegram) Enabled() bool {
	return t.botToken != "" && t.chatID != ""
}

// NotifyJobAnalyzed envia uma mensagem ao Telegram se o fit_score da análise
// atingir o mínimo configurado. Erros de envio são logados como warning e
// não interrompem o pipeline.
func (t *Telegram) NotifyJobAnalyzed(job models.Job, analysis models.Analysis) {
	if !t.Enabled() || analysis.FitScore < t.minScore {
		return
	}

	if err := t.send(buildMessage(job, analysis)); err != nil {
		slog.Warn("telegram: falha ao enviar notificação", "title", job.Title, "error", err)
	}
}

func buildMessage(job models.Job, analysis models.Analysis) string {
	msg := fmt.Sprintf("🎯 <b>Nova vaga com fit %d/100</b>\n\n<b>%s</b> — %s\n\n%s",
		analysis.FitScore, escapeHTML(job.Title), escapeHTML(job.Company), escapeHTML(analysis.Summary))
	if job.URL != "" {
		msg += "\n\n" + escapeHTML(job.URL)
	}
	return msg
}

// escapeHTML escapa os únicos caracteres que o parse_mode "HTML" do Telegram
// interpreta (&, < e >), evitando que texto da vaga vindo do LLM ou de
// scraping quebre a formatação. Diferente do Markdown legado, não há
// pareamento de entidades pra desbalancear — cada caractere é independente.
func escapeHTML(s string) string {
	replacer := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return replacer.Replace(s)
}

type sendMessageRequest struct {
	ChatID    string `json:"chat_id"`
	Text      string `json:"text"`
	ParseMode string `json:"parse_mode"`
}

type telegramErrorResponse struct {
	OK          bool   `json:"ok"`
	Description string `json:"description"`
	Parameters  struct {
		RetryAfter int `json:"retry_after"`
	} `json:"parameters"`
}

// maxSendAttempts é quantas vezes uma notificação é tentada antes de
// desistir. Erros de rede passageiros ("connection reset by peer",
// timeout), 429 e 5xx justificam nova tentativa; outros 4xx não.
const maxSendAttempts = 3

// maxRetryAfter limita a espera pedida pelo Telegram num 429, pra uma
// notificação não travar o pipeline por minutos.
const maxRetryAfter = 30 * time.Second

// retryableSendError é uma falha passageira; wait > 0 é a espera pedida
// pelo Telegram (retry_after do 429).
type retryableSendError struct {
	err  error
	wait time.Duration
}

func (e *retryableSendError) Error() string { return e.err.Error() }
func (e *retryableSendError) Unwrap() error { return e.err }

func (t *Telegram) send(text string) error {
	var err error
	for attempt := 1; attempt <= maxSendAttempts; attempt++ {
		err = t.sendOnce(text)
		var retryable *retryableSendError
		if err == nil || !errors.As(err, &retryable) || attempt == maxSendAttempts {
			break
		}
		wait := retryable.wait
		if wait <= 0 {
			wait = time.Duration(attempt) * time.Second
		}
		slog.Warn("telegram: falha passageira, tentando de novo",
			"tentativa", attempt, "espera", wait, "error", t.redact(err))
		t.sleep(wait)
	}
	if err != nil {
		return t.redact(err)
	}
	return nil
}

func (t *Telegram) sendOnce(text string) error {
	reqBody := sendMessageRequest{ChatID: t.chatID, Text: text, ParseMode: "HTML"}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("erro ao serializar mensagem: %w", err)
	}

	endpoint := fmt.Sprintf("%s/bot%s/sendMessage", t.apiURL, t.botToken)
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("erro ao criar requisição: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return &retryableSendError{err: fmt.Errorf("erro ao chamar api do telegram: %w", err)}
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return &retryableSendError{err: fmt.Errorf("erro ao ler resposta do telegram: %w", err)}
	}

	if resp.StatusCode != http.StatusOK {
		var apiErr telegramErrorResponse
		_ = json.Unmarshal(respBody, &apiErr)
		msg := apiErr.Description
		if msg == "" {
			msg = string(respBody)
		}
		err := fmt.Errorf("status inesperado %d do telegram: %s", resp.StatusCode, msg)
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= http.StatusInternalServerError {
			wait := min(time.Duration(apiErr.Parameters.RetryAfter)*time.Second, maxRetryAfter)
			return &retryableSendError{err: err, wait: wait}
		}
		return err
	}

	return nil
}

// redact tira o token do bot do erro. Erros do net/http (*url.Error)
// trazem a URL da requisição, e a URL da API do Telegram contém o token —
// sem isso ele vazaria no log.
func (t *Telegram) redact(err error) error {
	if err == nil || t.botToken == "" || !strings.Contains(err.Error(), t.botToken) {
		return err
	}
	return errors.New(strings.ReplaceAll(err.Error(), t.botToken, "<token>"))
}
