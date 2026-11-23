package crawler

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"job-scout/internal/models"
)

// getOnBoardSearchURLTemplate é a API pública v0 do Get on Board
// (getonbrd.com). expand=["company"] embute os dados da empresa na vaga.
// O valor de expand vai codificado: com colchetes/aspas crus a API responde 400.
const getOnBoardSearchURLTemplate = "https://www.getonbrd.com/api/v0/search/jobs?query=%s&per_page=50&page=%d&expand=%%5B%%22company%%22%%5D"

type getOnBoardResponse struct {
	Data []getOnBoardJob `json:"data"`
	Meta struct {
		Page       int `json:"page"`
		TotalPages int `json:"total_pages"`
	} `json:"meta"`
}

type getOnBoardJob struct {
	ID         string `json:"id"`
	Attributes struct {
		Title          string   `json:"title"`
		Description    string   `json:"description"`
		Projects       string   `json:"projects"`
		Functions      string   `json:"functions"`
		Benefits       string   `json:"benefits"`
		Desirable      string   `json:"desirable"`
		Remote         bool     `json:"remote"`
		RemoteModality string   `json:"remote_modality"`
		RemoteZone     *string  `json:"remote_zone"`
		Countries      []string `json:"countries"`
		MinSalary      *float64 `json:"min_salary"`
		MaxSalary      *float64 `json:"max_salary"`
		PublishedAt    int64    `json:"published_at"`
		Company        struct {
			Data struct {
				Attributes struct {
					Name    string `json:"name"`
					Country string `json:"country"`
				} `json:"attributes"`
			} `json:"data"`
		} `json:"company"`
	} `json:"attributes"`
	Links struct {
		PublicURL string `json:"public_url"`
	} `json:"links"`
}

// GetOnBoardCrawler busca vagas de tecnologia na API pública do Get on
// Board, board latino-americano com muitas vagas remotas para LATAM.
type GetOnBoardCrawler struct{}

// NewGetOnBoardCrawler cria um novo crawler para o Get on Board.
func NewGetOnBoardCrawler() *GetOnBoardCrawler {
	return &GetOnBoardCrawler{}
}

func (c *GetOnBoardCrawler) Name() string {
	return "getonbrd"
}

// Fetch busca até maxPages páginas (50 vagas cada) da busca por term.
func (c *GetOnBoardCrawler) Fetch(term string, maxPages int) ([]models.Job, error) {
	var jobs []models.Job
	for page := 1; page <= max(maxPages, 1); page++ {
		if page > 1 {
			randomDelay()
		}

		body, err := httpGetAPI(fmt.Sprintf(getOnBoardSearchURLTemplate, url.QueryEscape(term), page))
		if err != nil {
			return jobs, fmt.Errorf("erro ao buscar página %d de %q no get on board: %w", page, term, err)
		}
		found, totalPages, err := parseGetOnBoard(body)
		if err != nil {
			return jobs, fmt.Errorf("erro na página %d de %q no get on board: %w", page, term, err)
		}
		jobs = append(jobs, found...)

		if page >= totalPages {
			break
		}
	}
	return jobs, nil
}

// parseGetOnBoard converte uma página da API em vagas. Vagas presenciais ou
// híbridas fora do Brasil são descartadas — o board é forte no Chile, e
// essas vagas não servem pra quem está no Brasil.
func parseGetOnBoard(body []byte) ([]models.Job, int, error) {
	var resp getOnBoardResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, 0, fmt.Errorf("erro ao parsear resposta do get on board: %w", err)
	}

	var jobs []models.Job
	for _, g := range resp.Data {
		a := g.Attributes
		if g.Links.PublicURL == "" || a.Title == "" {
			continue
		}
		if !a.Remote && !containsFold(a.Countries, "Brazil", "Brasil") {
			continue
		}

		company := a.Company.Data.Attributes
		description := stripHTML(joinNonEmpty(" ", a.Functions, a.Description, a.Desirable, a.Benefits, a.Projects))

		job := models.Job{
			ID:                  hashID(g.Links.PublicURL),
			Source:              "getonbrd",
			Title:               a.Title,
			Company:             company.Name,
			Location:            joinNonEmpty(" · ", strings.Join(a.Countries, ", "), a.RemoteModality),
			URL:                 g.Links.PublicURL,
			Description:         description,
			FoundAt:             unixOrNow(a.PublishedAt),
			Status:              "new",
			LocationRestriction: getOnBoardRestriction(a.RemoteModality, a.RemoteZone, company.Country),
		}
		// O Get on Board publica faixas salariais em USD brutos por mês.
		if a.MinSalary != nil && a.MaxSalary != nil {
			job.SalaryMin = salaryAmount(*a.MinSalary)
			job.SalaryMax = salaryAmount(*a.MaxSalary)
			job.SalaryCurrency = "USD"
			job.SalaryPeriod = "monthly"
			job.Salary = salaryText("USD", *a.MinSalary, *a.MaxSalary, "monthly")
		}
		jobs = append(jobs, job)
	}
	return jobs, resp.Meta.TotalPages, nil
}

// getOnBoardRestriction traduz a modalidade remota do Get on Board:
// fully_remote aceita qualquer país (ou a zona informada), remote_local é
// remoto só para residentes de um país — a API não diz qual sem outra
// request, então usamos o país da empresa como indicação.
func getOnBoardRestriction(modality string, zone *string, companyCountry string) string {
	switch modality {
	case "fully_remote":
		if zone != nil && *zone != "" {
			return *zone
		}
		return "Worldwide"
	case "remote_local":
		if companyCountry != "" {
			return "Remoto apenas para residentes de um país (empresa em " + companyCountry + ")"
		}
		return "Remoto apenas para residentes de um país"
	}
	return ""
}

func containsFold(list []string, values ...string) bool {
	for _, item := range list {
		for _, v := range values {
			if strings.EqualFold(strings.TrimSpace(item), v) {
				return true
			}
		}
	}
	return false
}
