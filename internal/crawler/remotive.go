package crawler

import (
	"encoding/json"
	"fmt"
	"net/url"

	"job-scout/internal/models"
)

// remotiveAPIURLTemplate é a API pública do Remotive (remotive.com).
//
// ATENÇÃO — por isso o crawler vem desativado (enabled: false) no
// example.config.yaml:
//   - os termos da API exigem atribuição: link de volta pra vaga no
//     Remotive e menção ao Remotive como fonte;
//   - as vagas da API pública chegam com 24h de atraso;
//   - o robots.txt do site restringe acesso automatizado, e o aviso legal
//     da API pede no máximo ~4 requests por dia (excesso é bloqueado).
//
// Se ativar, rode o pipeline poucas vezes ao dia e mantenha a URL do
// Remotive na vaga (é o que o crawler já faz).
const remotiveAPIURLTemplate = "https://remotive.com/api/remote-jobs?search=%s&limit=%d"

const remotiveLimit = 50

type remotiveResponse struct {
	Jobs []remotiveJob `json:"jobs"`
}

type remotiveJob struct {
	URL                       string `json:"url"`
	Title                     string `json:"title"`
	CompanyName               string `json:"company_name"`
	Category                  string `json:"category"`
	JobType                   string `json:"job_type"`
	PublicationDate           string `json:"publication_date"`
	CandidateRequiredLocation string `json:"candidate_required_location"`
	Salary                    string `json:"salary"`
	Description               string `json:"description"`
}

// RemotiveCrawler busca vagas remotas na API pública do Remotive.
type RemotiveCrawler struct{}

// NewRemotiveCrawler cria um novo crawler para o Remotive.
func NewRemotiveCrawler() *RemotiveCrawler {
	return &RemotiveCrawler{}
}

func (c *RemotiveCrawler) Name() string {
	return "remotive"
}

// Fetch busca até 50 vagas para term. A API não pagina, então maxPages é
// ignorado.
func (c *RemotiveCrawler) Fetch(term string, _ int) ([]models.Job, error) {
	body, err := httpGetAPI(fmt.Sprintf(remotiveAPIURLTemplate, url.QueryEscape(term), remotiveLimit))
	if err != nil {
		return nil, fmt.Errorf("erro ao buscar %q no remotive: %w", term, err)
	}
	return parseRemotive(body)
}

func parseRemotive(body []byte) ([]models.Job, error) {
	var resp remotiveResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("erro ao parsear resposta do remotive: %w", err)
	}

	var jobs []models.Job
	for _, r := range resp.Jobs {
		if r.URL == "" || r.Title == "" {
			continue
		}
		jobs = append(jobs, models.Job{
			ID:          hashID(r.URL),
			Source:      "remotive",
			Title:       r.Title,
			Company:     r.CompanyName,
			Location:    joinNonEmpty(" · ", "Remote", r.JobType, r.Category),
			URL:         r.URL,
			Description: stripHTML(r.Description),
			Salary:      r.Salary,
			// Sem fuso no formato ("2026-11-14T20:33:53").
			FoundAt:             parseTimeOrNow("2006-01-02T15:04:05", r.PublicationDate),
			Status:              "new",
			LocationRestriction: r.CandidateRequiredLocation, // ex: "USA, Canada", "Worldwide", "LATAM"
		})
	}
	return jobs, nil
}
