package crawler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"golang.org/x/net/html"

	"job-scout/internal/models"
)

const ycBaseURL = "https://www.ycombinator.com"

// ycListPaths são as páginas públicas de vagas do YC (ycombinator.com/jobs)
// consultadas. Não usamos o Work at a Startup, que exige login. Só a lista
// de vagas remotas de engenharia: as demais são quase todas presenciais nos
// EUA.
var ycListPaths = []string{
	"/jobs/role/software-engineer/remote",
}

// ycMaxDetails limita quantas páginas de detalhe (uma request por vaga, pra
// pegar a descrição) são buscadas por execução.
const ycMaxDetails = 40

// As páginas do YC são renderizadas com Inertia.js: os dados vêm como JSON
// no atributo data-page de um <div>, então não é preciso parsear o HTML
// visual.
type ycListPage struct {
	Props struct {
		JobPostings []ycJobPosting `json:"jobPostings"`
	} `json:"props"`
}

type ycDetailPage struct {
	Props struct {
		Job ycJobPosting `json:"job"`
	} `json:"props"`
}

type ycJobPosting struct {
	Title            string   `json:"title"`
	URL              string   `json:"url"`
	Location         string   `json:"location"`
	Type             string   `json:"type"`
	RoleSpecificType string   `json:"roleSpecificType"`
	SalaryRange      string   `json:"salaryRange"`
	EquityRange      string   `json:"equityRange"`
	MinExperience    string   `json:"minExperience"`
	Visa             string   `json:"visa"`
	Skills           []string `json:"skills"`
	CompanyName      string   `json:"companyName"`
	CompanyBatchName string   `json:"companyBatchName"`
	CompanyOneLiner  string   `json:"companyOneLiner"`
	Description      string   `json:"description"`
}

// YCombinatorCrawler busca vagas de startups do YC na página pública
// ycombinator.com/jobs.
type YCombinatorCrawler struct {
	searchTerms []string
}

// NewYCombinatorCrawler cria um crawler para as vagas do YC que filtra
// pelos termos de busca informados.
func NewYCombinatorCrawler(searchTerms []string) *YCombinatorCrawler {
	return &YCombinatorCrawler{searchTerms: searchTerms}
}

func (c *YCombinatorCrawler) Name() string {
	return "ycombinator"
}

// IgnoresSearchTerms: as páginas não têm busca, então Fetch é chamado uma
// vez e filtra pelos termos localmente.
func (c *YCombinatorCrawler) IgnoresSearchTerms() bool {
	return true
}

// Fetch lê as listas de vagas, filtra pelos termos e busca a página de
// detalhe de cada vaga filtrada pra obter a descrição. term e maxPages são
// ignorados.
func (c *YCombinatorCrawler) Fetch(_ string, _ int) ([]models.Job, error) {
	var postings []ycJobPosting
	for i, path := range ycListPaths {
		if i > 0 {
			randomDelay()
		}
		body, err := httpGetAPI(ycBaseURL + path)
		if err != nil {
			return nil, fmt.Errorf("erro ao buscar %q no yc: %w", path, err)
		}
		found, err := parseYCList(body)
		if err != nil {
			return nil, fmt.Errorf("erro ao parsear %q no yc: %w", path, err)
		}
		postings = append(postings, found...)
	}

	var jobs []models.Job
	details := 0
	for _, p := range filterYCPostings(postings, c.searchTerms) {
		if details < ycMaxDetails {
			randomDelay()
			details++
			if body, err := httpGetAPI(ycBaseURL + p.URL); err != nil {
				slog.Warn("ycombinator: erro ao buscar detalhe da vaga", "url", p.URL, "error", err)
			} else if detail, err := parseYCDetail(body); err != nil {
				slog.Warn("ycombinator: erro ao parsear detalhe da vaga", "url", p.URL, "error", err)
			} else {
				p.Description = detail.Description
			}
		}
		jobs = append(jobs, ycToJob(p))
	}
	return jobs, nil
}

func filterYCPostings(postings []ycJobPosting, terms []string) []ycJobPosting {
	var out []ycJobPosting
	for _, p := range postings {
		if p.URL == "" || strings.TrimSpace(p.Title) == "" {
			continue
		}
		if matchesAnyTerm(p.Title+" "+p.RoleSpecificType+" "+strings.Join(p.Skills, " "), terms) {
			out = append(out, p)
		}
	}
	return out
}

func parseYCList(body []byte) ([]ycJobPosting, error) {
	var page ycListPage
	if err := decodeInertiaPage(body, &page); err != nil {
		return nil, err
	}
	return page.Props.JobPostings, nil
}

func parseYCDetail(body []byte) (ycJobPosting, error) {
	var page ycDetailPage
	if err := decodeInertiaPage(body, &page); err != nil {
		return ycJobPosting{}, err
	}
	return page.Props.Job, nil
}

// decodeInertiaPage acha o atributo data-page no HTML e decodifica o JSON
// dele em v.
func decodeInertiaPage(body []byte, v any) error {
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("erro ao parsear HTML: %w", err)
	}
	node := find(doc, func(n *html.Node) bool {
		_, ok := attr(n, "data-page")
		return n.Type == html.ElementNode && ok
	})
	if node == nil {
		return fmt.Errorf("atributo data-page não encontrado")
	}
	raw, _ := attr(node, "data-page")
	if err := json.Unmarshal([]byte(raw), v); err != nil {
		return fmt.Errorf("erro ao decodificar data-page: %w", err)
	}
	return nil
}

func ycToJob(p ycJobPosting) models.Job {
	jobURL := ycBaseURL + p.URL

	// A descrição completa só vem da página de detalhe; os campos da lista
	// entram sempre, pra vaga ter contexto mesmo se o detalhe falhar.
	header := joinNonEmpty("\n",
		joinNonEmpty(" — ", p.CompanyName+" ("+p.CompanyBatchName+")", p.CompanyOneLiner),
		labeled("Tipo", joinNonEmpty(", ", p.Type, p.RoleSpecificType)),
		labeled("Experiência mínima", p.MinExperience),
		labeled("Skills", strings.Join(p.Skills, ", ")),
		labeled("Equity", p.EquityRange),
		labeled("Visto", p.Visa),
	)

	return models.Job{
		ID:          hashID(jobURL),
		Source:      "ycombinator",
		Title:       strings.TrimSpace(p.Title),
		Company:     p.CompanyName,
		Location:    p.Location,
		URL:         jobURL,
		Description: joinNonEmpty("\n\n", header, p.Description),
		Salary:      p.SalaryRange,
		Status:      "new",
		// "Remote (US)" → "US"; "Remote" sozinho não traz restrição.
		LocationRestriction: extractRemoteRegions(p.Location),
	}
}

func labeled(label, value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return label + ": " + value
}
