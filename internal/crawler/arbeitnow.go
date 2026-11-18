package crawler

import (
	"encoding/json"
	"fmt"
	"strings"

	"job-scout/internal/models"
)

// arbeitnowAPIURL é a API pública do Arbeitnow (board focado na Europa,
// sobretudo Alemanha): 300 vagas por página, paginado por ?page=N, sem
// busca. Os termos de uso pedem link de volta pro site — guardamos a URL
// do Arbeitnow em cada vaga.
const arbeitnowAPIURL = "https://www.arbeitnow.com/api/job-board-api?page=%d"

type arbeitnowResponse struct {
	Data  []arbeitnowJob `json:"data"`
	Links struct {
		Next *string `json:"next"`
	} `json:"links"`
}

type arbeitnowJob struct {
	CompanyName string   `json:"company_name"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Remote      bool     `json:"remote"`
	URL         string   `json:"url"`
	Tags        []string `json:"tags"`
	JobTypes    []string `json:"job_types"`
	Location    string   `json:"location"`
	CreatedAt   int64    `json:"created_at"`
}

// ArbeitnowCrawler busca vagas remotas na API pública do Arbeitnow.
type ArbeitnowCrawler struct {
	searchTerms []string
}

// NewArbeitnowCrawler cria um crawler para o Arbeitnow que filtra as vagas
// pelos termos de busca informados.
func NewArbeitnowCrawler(searchTerms []string) *ArbeitnowCrawler {
	return &ArbeitnowCrawler{searchTerms: searchTerms}
}

func (c *ArbeitnowCrawler) Name() string {
	return "arbeitnow"
}

// IgnoresSearchTerms: a API não tem busca, então Fetch é chamado uma vez e
// filtra pelos termos localmente.
func (c *ArbeitnowCrawler) IgnoresSearchTerms() bool {
	return true
}

// Fetch percorre até maxPages páginas do feed (ordenado do mais recente pro
// mais antigo), mantendo só vagas remotas que casam com os termos.
func (c *ArbeitnowCrawler) Fetch(_ string, maxPages int) ([]models.Job, error) {
	var jobs []models.Job
	for page := 1; page <= max(maxPages, 1); page++ {
		if page > 1 {
			randomDelay()
		}

		body, err := httpGetAPI(fmt.Sprintf(arbeitnowAPIURL, page))
		if err != nil {
			return jobs, fmt.Errorf("erro ao buscar página %d no arbeitnow: %w", page, err)
		}
		found, hasNext, err := parseArbeitnow(body, c.searchTerms)
		if err != nil {
			return jobs, fmt.Errorf("erro na página %d no arbeitnow: %w", page, err)
		}
		jobs = append(jobs, found...)

		if !hasNext {
			break
		}
	}
	return jobs, nil
}

// parseArbeitnow converte uma página em vagas e informa se há próxima.
// Vagas presenciais são descartadas: o board é quase todo de vagas na
// Alemanha, que não servem pra quem está no Brasil.
func parseArbeitnow(body []byte, terms []string) ([]models.Job, bool, error) {
	var resp arbeitnowResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, false, fmt.Errorf("erro ao parsear resposta do arbeitnow: %w", err)
	}

	var jobs []models.Job
	for _, a := range resp.Data {
		if a.URL == "" || a.Title == "" || !a.Remote {
			continue
		}
		if !matchesAnyTerm(a.Title+" "+strings.Join(a.Tags, " "), terms) {
			continue
		}
		location := a.Location
		if !strings.EqualFold(strings.TrimSpace(location), "remote") {
			location = joinNonEmpty(" · ", location, "Remote")
		}
		jobs = append(jobs, models.Job{
			ID:          hashID(a.URL),
			Source:      "arbeitnow",
			Title:       a.Title,
			Company:     a.CompanyName,
			Location:    location,
			URL:         a.URL,
			Description: stripHTML(a.Description),
			FoundAt:     unixOrNow(a.CreatedAt),
			Status:      "new",
		})
	}
	return jobs, resp.Links.Next != nil && *resp.Links.Next != "", nil
}
