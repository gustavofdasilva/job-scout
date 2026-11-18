package crawler

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"job-scout/internal/models"
)

// ashbyJobBoardURLTemplate é a Posting API pública do Ashby — uma request
// por empresa ({slug} é o mesmo de jobs.ashbyhq.com/{slug}).
const ashbyJobBoardURLTemplate = "https://api.ashbyhq.com/posting-api/job-board/%s?includeCompensation=true"

type ashbyBoard struct {
	Jobs []ashbyJob `json:"jobs"`
}

type ashbyJob struct {
	Title              string `json:"title"`
	Department         string `json:"department"`
	Team               string `json:"team"`
	EmploymentType     string `json:"employmentType"`
	Location           string `json:"location"`
	SecondaryLocations []struct {
		Location string `json:"location"`
	} `json:"secondaryLocations"`
	PublishedAt      string             `json:"publishedAt"`
	IsListed         bool               `json:"isListed"`
	IsRemote         bool               `json:"isRemote"`
	WorkplaceType    string             `json:"workplaceType"`
	JobURL           string             `json:"jobUrl"`
	DescriptionHTML  string             `json:"descriptionHtml"`
	DescriptionPlain string             `json:"descriptionPlain"`
	Compensation     *ashbyCompensation `json:"compensation"`
}

type ashbyCompensation struct {
	CompensationTierSummary string `json:"compensationTierSummary"`
	SummaryComponents       []struct {
		CompensationType string   `json:"compensationType"`
		Interval         string   `json:"interval"`
		CurrencyCode     string   `json:"currencyCode"`
		MinValue         *float64 `json:"minValue"`
		MaxValue         *float64 `json:"maxValue"`
	} `json:"summaryComponents"`
}

// AshbyCrawler busca vagas nas job boards públicas do Ashby
// (jobs.ashbyhq.com) de uma lista fixa de empresas — como no Lever, não há
// busca global, então cada empresa precisa ser configurada.
type AshbyCrawler struct {
	companies   []string
	searchTerms []string
}

// NewAshbyCrawler cria um crawler para as boards do Ashby das empresas
// informadas, filtrando as vagas pelos termos de busca.
func NewAshbyCrawler(companies, searchTerms []string) *AshbyCrawler {
	return &AshbyCrawler{companies: companies, searchTerms: searchTerms}
}

func (c *AshbyCrawler) Name() string {
	return "ashby"
}

// IgnoresSearchTerms indica ao Orchestrator que Fetch deve ser chamado uma
// única vez: o crawler itera sobre as empresas e aplica os termos sozinho.
func (c *AshbyCrawler) IgnoresSearchTerms() bool {
	return true
}

// Fetch busca as vagas de todas as empresas configuradas, em paralelo com
// concorrência limitada. term e maxPages são ignorados (a API não pagina).
func (c *AshbyCrawler) Fetch(_ string, _ int) ([]models.Job, error) {
	return fetchCompanies(c.Name(), c.companies, func(company string) ([]models.Job, error) {
		body, err := httpGetAPI(fmt.Sprintf(ashbyJobBoardURLTemplate, company))
		if err != nil {
			return nil, fmt.Errorf("erro ao buscar board de %q no ashby: %w", company, err)
		}
		return parseAshbyBoard(body, company, c.searchTerms)
	}), nil
}

// parseAshbyBoard converte a resposta da Posting API em vagas. O filtro por
// termo olha só título/time/departamento: boards grandes (OpenAI, Ramp) têm
// centenas de vagas cujas descrições citam "backend" de passagem.
func parseAshbyBoard(body []byte, company string, terms []string) ([]models.Job, error) {
	var board ashbyBoard
	if err := json.Unmarshal(body, &board); err != nil {
		return nil, fmt.Errorf("erro ao parsear board de %q no ashby: %w", company, err)
	}

	var jobs []models.Job
	for _, a := range board.Jobs {
		title := strings.TrimSpace(a.Title)
		if a.JobURL == "" || title == "" || !a.IsListed {
			continue
		}
		if !matchesAnyTerm(title+" "+a.Team+" "+a.Department, terms) {
			continue
		}

		locations := []string{a.Location}
		for _, s := range a.SecondaryLocations {
			locations = append(locations, s.Location)
		}

		description := a.DescriptionPlain
		if description == "" {
			description = stripHTML(a.DescriptionHTML)
		}

		job := models.Job{
			ID:                  hashID(a.JobURL),
			Source:              "ashby",
			Title:               title,
			Company:             capitalize(company),
			Location:            joinNonEmpty(" · ", strings.Join(locations, " / "), a.WorkplaceType),
			URL:                 a.JobURL,
			Description:         description,
			FoundAt:             parseTimeOrNow(time.RFC3339, a.PublishedAt),
			Status:              "new",
			LocationRestriction: extractRemoteRegions(locations...),
		}
		applyAshbyCompensation(&job, a.Compensation)
		jobs = append(jobs, job)
	}
	return jobs, nil
}

func applyAshbyCompensation(job *models.Job, comp *ashbyCompensation) {
	if comp == nil {
		return
	}
	job.Salary = comp.CompensationTierSummary
	for _, s := range comp.SummaryComponents {
		if s.CompensationType != "Salary" || s.MinValue == nil || s.MaxValue == nil {
			continue
		}
		job.SalaryMin = int(*s.MinValue)
		job.SalaryMax = int(*s.MaxValue)
		job.SalaryCurrency = s.CurrencyCode
		job.SalaryPeriod = ashbyInterval(s.Interval)
		return
	}
}

// ashbyInterval traduz o intervalo do Ashby ("1 YEAR", "1 MONTH", "1 HOUR")
// pros períodos usados no restante do projeto.
func ashbyInterval(interval string) string {
	switch strings.ToUpper(strings.TrimSpace(interval)) {
	case "1 YEAR":
		return "yearly"
	case "1 MONTH":
		return "monthly"
	case "1 WEEK":
		return "weekly"
	case "1 DAY":
		return "daily"
	case "1 HOUR":
		return "hourly"
	}
	return strings.ToLower(interval)
}
