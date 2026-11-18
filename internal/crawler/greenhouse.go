package crawler

import (
	"encoding/json"
	"fmt"
	"html"
	"strings"
	"time"

	"job-scout/internal/models"
)

// greenhouseJobsURLTemplate é a Job Board API pública do Greenhouse.
// content=true inclui a descrição (HTML escapado) de cada vaga.
const greenhouseJobsURLTemplate = "https://boards-api.greenhouse.io/v1/boards/%s/jobs?content=true"

type greenhouseBoard struct {
	Jobs []greenhouseJob `json:"jobs"`
}

type greenhouseJob struct {
	AbsoluteURL    string `json:"absolute_url"`
	Title          string `json:"title"`
	CompanyName    string `json:"company_name"`
	FirstPublished string `json:"first_published"`
	UpdatedAt      string `json:"updated_at"`
	Content        string `json:"content"`
	Location       struct {
		Name string `json:"name"`
	} `json:"location"`
	Departments []struct {
		Name string `json:"name"`
	} `json:"departments"`
	Offices []struct {
		Name string `json:"name"`
	} `json:"offices"`
}

// GreenhouseCrawler busca vagas nas job boards públicas do Greenhouse
// (boards.greenhouse.io/{slug}) de uma lista fixa de empresas.
type GreenhouseCrawler struct {
	companies   []string
	searchTerms []string
}

// NewGreenhouseCrawler cria um crawler para as boards do Greenhouse das
// empresas informadas, filtrando as vagas pelos termos de busca.
func NewGreenhouseCrawler(companies, searchTerms []string) *GreenhouseCrawler {
	return &GreenhouseCrawler{companies: companies, searchTerms: searchTerms}
}

func (c *GreenhouseCrawler) Name() string {
	return "greenhouse"
}

// IgnoresSearchTerms: Fetch é chamado uma vez e itera sobre as empresas.
func (c *GreenhouseCrawler) IgnoresSearchTerms() bool {
	return true
}

// Fetch busca as vagas de todas as empresas configuradas, em paralelo com
// concorrência limitada. term e maxPages são ignorados (a API não pagina).
func (c *GreenhouseCrawler) Fetch(_ string, _ int) ([]models.Job, error) {
	return fetchCompanies(c.Name(), c.companies, func(company string) ([]models.Job, error) {
		body, err := httpGetAPI(fmt.Sprintf(greenhouseJobsURLTemplate, company))
		if err != nil {
			return nil, fmt.Errorf("erro ao buscar board de %q no greenhouse: %w", company, err)
		}
		return parseGreenhouseBoard(body, company, c.searchTerms)
	}), nil
}

// parseGreenhouseBoard converte a resposta da Job Board API em vagas,
// filtrando por título/departamento (ver parseAshbyBoard pro motivo).
func parseGreenhouseBoard(body []byte, company string, terms []string) ([]models.Job, error) {
	var board greenhouseBoard
	if err := json.Unmarshal(body, &board); err != nil {
		return nil, fmt.Errorf("erro ao parsear board de %q no greenhouse: %w", company, err)
	}

	var jobs []models.Job
	for _, g := range board.Jobs {
		title := strings.TrimSpace(g.Title)
		if g.AbsoluteURL == "" || title == "" {
			continue
		}

		var departments, offices []string
		for _, d := range g.Departments {
			departments = append(departments, d.Name)
		}
		for _, o := range g.Offices {
			offices = append(offices, o.Name)
		}
		if !matchesAnyTerm(title+" "+strings.Join(departments, " "), terms) {
			continue
		}

		companyName := g.CompanyName
		if companyName == "" {
			companyName = capitalize(company)
		}

		published := g.FirstPublished
		if published == "" {
			published = g.UpdatedAt
		}

		jobs = append(jobs, models.Job{
			ID:       hashID(g.AbsoluteURL),
			Source:   "greenhouse",
			Title:    title,
			Company:  companyName,
			Location: g.Location.Name,
			URL:      g.AbsoluteURL,
			// content vem com o HTML escapado (&lt;p&gt;): desescapa antes
			// de remover as tags.
			Description:         stripHTML(html.UnescapeString(g.Content)),
			FoundAt:             parseTimeOrNow(time.RFC3339, published),
			Status:              "new",
			LocationRestriction: extractRemoteRegions(append([]string{g.Location.Name}, offices...)...),
		})
	}
	return jobs, nil
}
