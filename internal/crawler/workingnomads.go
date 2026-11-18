package crawler

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"job-scout/internal/models"
)

// workingNomadsAPIURL é o feed JSON público do Working Nomads: devolve as
// vagas mais recentes de todas as categorias de uma vez, sem paginação nem
// busca.
const workingNomadsAPIURL = "https://www.workingnomads.com/api/exposed_jobs/"

type workingNomadsJob struct {
	URL          string `json:"url"`
	Title        string `json:"title"`
	Description  string `json:"description"`
	CompanyName  string `json:"company_name"`
	CategoryName string `json:"category_name"`
	Tags         string `json:"tags"` // separadas por vírgula
	Location     string `json:"location"`
	PubDate      string `json:"pub_date"`
}

// WorkingNomadsCrawler busca vagas remotas no feed público do Working
// Nomads (workingnomads.com).
type WorkingNomadsCrawler struct {
	searchTerms []string
}

// NewWorkingNomadsCrawler cria um crawler para o Working Nomads que filtra
// as vagas pelos termos de busca informados.
func NewWorkingNomadsCrawler(searchTerms []string) *WorkingNomadsCrawler {
	return &WorkingNomadsCrawler{searchTerms: searchTerms}
}

func (c *WorkingNomadsCrawler) Name() string {
	return "workingnomads"
}

// IgnoresSearchTerms: o feed não tem busca, então Fetch é chamado uma vez e
// filtra pelos termos localmente.
func (c *WorkingNomadsCrawler) IgnoresSearchTerms() bool {
	return true
}

// Fetch baixa o feed e filtra pelos termos. term e maxPages são ignorados.
func (c *WorkingNomadsCrawler) Fetch(_ string, _ int) ([]models.Job, error) {
	body, err := httpGetAPI(workingNomadsAPIURL)
	if err != nil {
		return nil, fmt.Errorf("erro ao buscar vagas no working nomads: %w", err)
	}
	return parseWorkingNomads(body, c.searchTerms)
}

func parseWorkingNomads(body []byte, terms []string) ([]models.Job, error) {
	var raw []workingNomadsJob
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("erro ao parsear resposta do working nomads: %w", err)
	}

	var jobs []models.Job
	for _, w := range raw {
		if w.URL == "" || w.Title == "" {
			continue
		}
		if !matchesAnyTerm(w.Title+" "+w.Tags+" "+w.CategoryName, terms) {
			continue
		}
		jobs = append(jobs, models.Job{
			ID:          hashID(w.URL),
			Source:      "workingnomads",
			Title:       w.Title,
			Company:     w.CompanyName,
			Location:    "Remote",
			URL:         w.URL,
			Description: stripHTML(w.Description),
			FoundAt:     parseTimeOrNow(time.RFC3339, w.PubDate),
			Status:      "new",
			// Ex: "Europe, LATAM, APAC, the U.S., Canada" ou "USA ONLY".
			LocationRestriction: strings.TrimSpace(w.Location),
		})
	}
	return jobs, nil
}
