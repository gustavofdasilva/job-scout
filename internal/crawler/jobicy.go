package crawler

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"job-scout/internal/models"
)

// jobicyURLTemplate é a API pública de vagas remotas do Jobicy. tag filtra
// por palavra-chave; a paginação é por cursor (nextCursor/hasMore).
//
// Os termos de uso pedem crédito ao Jobicy com link pra vaga original — o
// job-scout guarda a URL do Jobicy e a fonte "jobicy" em cada vaga.
const jobicyURLTemplate = "https://jobicy.com/api/v2/remote-jobs?count=50&tag=%s"

type jobicyResponse struct {
	NextCursor *string     `json:"nextCursor"`
	HasMore    bool        `json:"hasMore"`
	Jobs       []jobicyJob `json:"jobs"`
}

type jobicyJob struct {
	URL            string   `json:"url"`
	JobTitle       string   `json:"jobTitle"`
	CompanyName    string   `json:"companyName"`
	JobType        []string `json:"jobType"`
	JobGeo         string   `json:"jobGeo"`
	JobLevel       string   `json:"jobLevel"`
	JobDescription string   `json:"jobDescription"`
	PubDate        string   `json:"pubDate"`
	SalaryMin      *float64 `json:"salaryMin"`
	SalaryMax      *float64 `json:"salaryMax"`
	SalaryCurrency string   `json:"salaryCurrency"`
	SalaryPeriod   string   `json:"salaryPeriod"`
}

// JobicyCrawler busca vagas remotas na API pública do Jobicy (jobicy.com).
type JobicyCrawler struct{}

// NewJobicyCrawler cria um novo crawler para o Jobicy.
func NewJobicyCrawler() *JobicyCrawler {
	return &JobicyCrawler{}
}

func (c *JobicyCrawler) Name() string {
	return "jobicy"
}

// Fetch busca até maxPages páginas (50 vagas cada) com a tag term.
func (c *JobicyCrawler) Fetch(term string, maxPages int) ([]models.Job, error) {
	var jobs []models.Job
	reqURL := fmt.Sprintf(jobicyURLTemplate, url.QueryEscape(term))
	for page := 1; page <= max(maxPages, 1); page++ {
		if page > 1 {
			randomDelay()
		}

		body, err := httpGetAPI(reqURL)
		if err != nil {
			return jobs, fmt.Errorf("erro ao buscar página %d de %q no jobicy: %w", page, term, err)
		}
		found, next, err := parseJobicy(body)
		if err != nil {
			return jobs, fmt.Errorf("erro na página %d de %q no jobicy: %w", page, term, err)
		}
		jobs = append(jobs, found...)

		if next == "" {
			break
		}
		reqURL = fmt.Sprintf(jobicyURLTemplate, url.QueryEscape(term)) + "&cursor=" + url.QueryEscape(next)
	}
	return jobs, nil
}

// parseJobicy converte uma página da API em vagas e devolve o cursor da
// próxima página (vazio se não houver mais).
func parseJobicy(body []byte) ([]models.Job, string, error) {
	var resp jobicyResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, "", fmt.Errorf("erro ao parsear resposta do jobicy: %w", err)
	}

	var jobs []models.Job
	for _, j := range resp.Jobs {
		if j.URL == "" || j.JobTitle == "" {
			continue
		}
		job := models.Job{
			ID:                  hashID(j.URL),
			Source:              "jobicy",
			Title:               j.JobTitle,
			Company:             j.CompanyName,
			Location:            joinNonEmpty(" · ", "Remote", strings.Join(j.JobType, ", "), j.JobLevel),
			URL:                 j.URL,
			Description:         stripHTML(j.JobDescription),
			FoundAt:             parseTimeOrNow(time.RFC3339, j.PubDate),
			Status:              "new",
			LocationRestriction: j.JobGeo, // "USA", "Anywhere", "LATAM", "Canada, USA"...
		}
		if j.SalaryMin != nil && j.SalaryMax != nil {
			job.SalaryMin = int(*j.SalaryMin)
			job.SalaryMax = int(*j.SalaryMax)
			job.SalaryCurrency = j.SalaryCurrency
			job.SalaryPeriod = j.SalaryPeriod
			job.Salary = fmt.Sprintf("%s %d–%d / %s", j.SalaryCurrency, job.SalaryMin, job.SalaryMax, j.SalaryPeriod)
		}
		jobs = append(jobs, job)
	}

	next := ""
	if resp.HasMore && resp.NextCursor != nil {
		next = *resp.NextCursor
	}
	return jobs, next, nil
}
