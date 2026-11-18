package crawler

import (
	"encoding/json"
	"fmt"
	"strings"

	"job-scout/internal/models"
)

// workableWidgetURLTemplate é a API pública do widget de vagas do Workable.
// details=true inclui a descrição de cada vaga. Só responde com vagas para
// empresas que deixam o widget habilitado — muitas contas retornam lista
// vazia mesmo com vagas abertas em apply.workable.com.
const workableWidgetURLTemplate = "https://apply.workable.com/api/v1/widget/accounts/%s?details=true"

type workableAccount struct {
	Name string        `json:"name"`
	Jobs []workableJob `json:"jobs"`
}

type workableJob struct {
	Title         string `json:"title"`
	URL           string `json:"url"`
	Telecommuting bool   `json:"telecommuting"`
	Department    string `json:"department"`
	Function      string `json:"function"`
	Experience    string `json:"experience"`
	PublishedOn   string `json:"published_on"`
	City          string `json:"city"`
	State         string `json:"state"`
	Country       string `json:"country"`
	Locations     []struct {
		Country string `json:"country"`
		City    string `json:"city"`
	} `json:"locations"`
	Description string `json:"description"`
}

// WorkableCrawler busca vagas nas páginas públicas do Workable
// (apply.workable.com/{slug}) de uma lista fixa de empresas.
type WorkableCrawler struct {
	companies   []string
	searchTerms []string
}

// NewWorkableCrawler cria um crawler para as contas do Workable das
// empresas informadas, filtrando as vagas pelos termos de busca.
func NewWorkableCrawler(companies, searchTerms []string) *WorkableCrawler {
	return &WorkableCrawler{companies: companies, searchTerms: searchTerms}
}

func (c *WorkableCrawler) Name() string {
	return "workable"
}

// IgnoresSearchTerms: Fetch é chamado uma vez e itera sobre as empresas.
func (c *WorkableCrawler) IgnoresSearchTerms() bool {
	return true
}

// Fetch busca as vagas de todas as empresas configuradas, em paralelo com
// concorrência limitada. term e maxPages são ignorados.
func (c *WorkableCrawler) Fetch(_ string, _ int) ([]models.Job, error) {
	return fetchCompanies(c.Name(), c.companies, func(company string) ([]models.Job, error) {
		body, err := httpGetAPI(fmt.Sprintf(workableWidgetURLTemplate, company))
		if err != nil {
			return nil, fmt.Errorf("erro ao buscar vagas de %q no workable: %w", company, err)
		}
		return parseWorkableAccount(body, company, c.searchTerms)
	}), nil
}

// parseWorkableAccount converte a resposta do widget em vagas, filtrando
// por título/departamento/função.
func parseWorkableAccount(body []byte, company string, terms []string) ([]models.Job, error) {
	var account workableAccount
	if err := json.Unmarshal(body, &account); err != nil {
		return nil, fmt.Errorf("erro ao parsear vagas de %q no workable: %w", company, err)
	}

	companyName := account.Name
	if companyName == "" {
		companyName = capitalize(company)
	}

	var jobs []models.Job
	for _, w := range account.Jobs {
		title := strings.TrimSpace(w.Title)
		if w.URL == "" || title == "" {
			continue
		}
		if !matchesAnyTerm(title+" "+w.Department+" "+w.Function, terms) {
			continue
		}

		var countries []string
		for _, l := range w.Locations {
			countries = append(countries, l.Country)
		}
		if len(countries) == 0 {
			countries = []string{w.Country}
		}

		job := models.Job{
			ID:          hashID(w.URL),
			Source:      "workable",
			Title:       title,
			Company:     companyName,
			Location:    joinNonEmpty(", ", w.City, w.State, w.Country),
			URL:         w.URL,
			Description: stripHTML(w.Description),
			FoundAt:     parseTimeOrNow("2006-01-02", w.PublishedOn),
			Status:      "new",
		}
		// Vaga remota no Workable é sempre atrelada aos países listados.
		if w.Telecommuting {
			job.Location = joinNonEmpty(" · ", job.Location, "Remote")
			job.LocationRestriction = joinNonEmpty(", ", countries...)
		}
		jobs = append(jobs, job)
	}
	return jobs, nil
}
