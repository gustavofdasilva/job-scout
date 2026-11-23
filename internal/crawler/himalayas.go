package crawler

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"job-scout/internal/models"
)

// himalayasSearchURLTemplate é o endpoint de busca da API pública do
// Himalayas: 20 vagas por página, paginado com ?page=N (a busca não tem
// cursor; o cursor só existe no feed geral /jobs/api).
const himalayasSearchURLTemplate = "https://himalayas.app/jobs/api/search?q=%s&page=%d"

const himalayasPageSize = 20

type himalayasResponse struct {
	TotalCount int            `json:"totalCount"`
	Jobs       []himalayasJob `json:"jobs"`
}

type himalayasJob struct {
	Title                string   `json:"title"`
	CompanyName          string   `json:"companyName"`
	EmploymentType       string   `json:"employmentType"`
	MinSalary            *float64 `json:"minSalary"`
	MaxSalary            *float64 `json:"maxSalary"`
	Currency             *string  `json:"currency"`
	SalaryPeriod         string   `json:"salaryPeriod"`
	Seniority            []string `json:"seniority"`
	LocationRestrictions []string `json:"locationRestrictions"`
	Description          string   `json:"description"`
	PubDate              int64    `json:"pubDate"`
	ApplicationLink      string   `json:"applicationLink"`
	GUID                 string   `json:"guid"`
}

// HimalayasCrawler busca vagas remotas na API pública do Himalayas
// (himalayas.app), que informa os países aceitos em cada vaga.
type HimalayasCrawler struct{}

// NewHimalayasCrawler cria um novo crawler para o Himalayas.
func NewHimalayasCrawler() *HimalayasCrawler {
	return &HimalayasCrawler{}
}

func (c *HimalayasCrawler) Name() string {
	return "himalayas"
}

// Fetch busca até maxPages páginas (20 vagas cada) da busca por term.
func (c *HimalayasCrawler) Fetch(term string, maxPages int) ([]models.Job, error) {
	var jobs []models.Job
	for page := 1; page <= max(maxPages, 1); page++ {
		if page > 1 {
			randomDelay()
		}

		body, err := httpGetAPI(fmt.Sprintf(himalayasSearchURLTemplate, url.QueryEscape(term), page))
		if err != nil {
			return jobs, fmt.Errorf("erro ao buscar página %d de %q no himalayas: %w", page, term, err)
		}
		found, total, err := parseHimalayas(body)
		if err != nil {
			return jobs, fmt.Errorf("erro na página %d de %q no himalayas: %w", page, term, err)
		}
		jobs = append(jobs, found...)

		if len(found) < himalayasPageSize || page*himalayasPageSize >= total {
			break
		}
	}
	return jobs, nil
}

func parseHimalayas(body []byte) ([]models.Job, int, error) {
	var resp himalayasResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, 0, fmt.Errorf("erro ao parsear resposta do himalayas: %w", err)
	}

	var jobs []models.Job
	for _, h := range resp.Jobs {
		// guid é a URL canônica da vaga; applicationLink é a mesma com
		// ?utm_source=api.
		jobURL := h.GUID
		if jobURL == "" {
			jobURL = h.ApplicationLink
		}
		if jobURL == "" || h.Title == "" {
			continue
		}

		// Lista vazia no Himalayas significa vaga aberta a qualquer país.
		restriction := "Worldwide"
		if len(h.LocationRestrictions) > 0 {
			restriction = strings.Join(h.LocationRestrictions, ", ")
		}

		job := models.Job{
			ID:                  hashID(jobURL),
			Source:              "himalayas",
			Title:               h.Title,
			Company:             h.CompanyName,
			Location:            joinNonEmpty(" · ", "Remote", h.EmploymentType, strings.Join(h.Seniority, ", ")),
			URL:                 jobURL,
			Description:         stripHTML(h.Description),
			FoundAt:             unixOrNow(h.PubDate),
			Status:              "new",
			LocationRestriction: restriction,
		}
		if h.MinSalary != nil && h.MaxSalary != nil && h.Currency != nil {
			job.SalaryMin = salaryAmount(*h.MinSalary)
			job.SalaryMax = salaryAmount(*h.MaxSalary)
			job.SalaryCurrency = *h.Currency
			job.SalaryPeriod = h.SalaryPeriod
			job.Salary = salaryText(job.SalaryCurrency, *h.MinSalary, *h.MaxSalary, job.SalaryPeriod)
		}
		jobs = append(jobs, job)
	}
	return jobs, resp.TotalCount, nil
}
