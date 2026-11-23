package crawler

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	"job-scout/internal/models"
)

// leverPostingsURLTemplate é o endpoint público e oficial do Lever — não é
// "jobs.lever.co/{empresa}/postings" (esse domínio serve só a página HTML
// da board); a API JSON mora em api.lever.co.
const leverPostingsURLTemplate = "https://api.lever.co/v0/postings/%s?mode=json"

type leverPosting struct {
	ID               string            `json:"id"`
	Text             string            `json:"text"`
	Categories       leverCategories   `json:"categories"`
	DescriptionPlain string            `json:"descriptionPlain"`
	SalaryRange      *leverSalaryRange `json:"salaryRange"`
	HostedURL        string            `json:"hostedUrl"`
	CreatedAt        int64             `json:"createdAt"`
}

type leverCategories struct {
	Commitment string `json:"commitment"`
	Location   string `json:"location"`
	Team       string `json:"team"`
	Department string `json:"department"`
}

type leverSalaryRange struct {
	Min      float64 `json:"min"`
	Max      float64 `json:"max"`
	Currency string `json:"currency"`
	Interval string `json:"interval"`
}

// LeverCrawler busca vagas nas boards públicas do Lever (jobs.lever.co) de
// uma lista fixa de empresas — o Lever não oferece busca global, então cada
// empresa-alvo precisa ser configurada explicitamente.
type LeverCrawler struct {
	companies   []string
	searchTerms []string
}

// NewLeverCrawler cria um novo crawler para as boards do Lever das empresas
// informadas, filtrando as vagas pelos termos de busca informados.
func NewLeverCrawler(companies, searchTerms []string) *LeverCrawler {
	return &LeverCrawler{companies: companies, searchTerms: searchTerms}
}

func (c *LeverCrawler) Name() string {
	return "lever"
}

// IgnoresSearchTerms indica ao Orchestrator que os search_terms já são
// aplicados internamente (ver Fetch) e que ele deve chamar Fetch uma única
// vez — o crawler itera sobre as empresas configuradas internamente.
func (c *LeverCrawler) IgnoresSearchTerms() bool {
	return true
}

// Fetch busca e filtra as vagas das boards do Lever de todas as empresas
// configuradas. term e maxPages são ignorados — o Lever não pagina nem
// busca por termo; os filtros vêm de searchTerms e a lista de empresas vem
// de companies (informados em NewLeverCrawler).
func (c *LeverCrawler) Fetch(_ string, _ int) ([]models.Job, error) {
	var jobs []models.Job

	for i, company := range c.companies {
		if i > 0 {
			randomDelay()
		}

		found, err := c.fetchCompany(company)
		if err != nil {
			slog.Warn("lever: erro ao buscar vagas da empresa", "company", company, "error", err)
			continue
		}

		slog.Info("lever: empresa processada", "company", company, "vagas_encontradas", len(found))
		jobs = append(jobs, found...)
	}

	return jobs, nil
}

func (c *LeverCrawler) fetchCompany(company string) ([]models.Job, error) {
	reqURL := fmt.Sprintf(leverPostingsURLTemplate, company)

	body, err := httpGet(reqURL)
	if err != nil {
		return nil, fmt.Errorf("erro ao buscar postings de %q no lever: %w", company, err)
	}
	return parseLever(body, company, c.searchTerms)
}

// parseLever converte a resposta da API do Lever de uma empresa nas vagas
// que casam com searchTerms.
func parseLever(body []byte, company string, searchTerms []string) ([]models.Job, error) {
	var postings []leverPosting
	if err := json.Unmarshal(body, &postings); err != nil {
		return nil, fmt.Errorf("erro ao parsear postings de %q no lever: %w", company, err)
	}

	var jobs []models.Job
	for _, p := range postings {
		if p.HostedURL == "" || p.Text == "" {
			continue
		}
		if !matchesAnyTerm(p.Text+" "+p.DescriptionPlain, searchTerms) {
			continue
		}

		job := models.Job{
			ID:          hashID(p.HostedURL),
			Source:      "lever",
			Title:       p.Text,
			Company:     capitalize(company),
			Location:    leverLocation(p.Categories),
			URL:         p.HostedURL,
			Description: p.DescriptionPlain,
			FoundAt:     leverCreatedAt(p.CreatedAt),
			Status:      "new",
		}
		applyLeverSalary(&job, p.SalaryRange)
		jobs = append(jobs, job)
	}

	return jobs, nil
}

func leverLocation(cat leverCategories) string {
	loc := strings.TrimSpace(cat.Location)
	commitment := strings.TrimSpace(cat.Commitment)
	if commitment == "" {
		return loc
	}
	if loc == "" {
		return commitment
	}
	return loc + " · " + commitment
}

// applyLeverSalary preenche o salário estruturado e o texto exibido.
// Faixas anuais ficam no formato curto "$120k–$150k/yr USD"; as demais
// (por hora, por mês...) vão por extenso, já que dividir 24.5/hora por
// mil daria "$0k".
func applyLeverSalary(job *models.Job, r *leverSalaryRange) {
	if r == nil || (r.Min == 0 && r.Max == 0) {
		return
	}
	period := leverInterval(r.Interval)
	job.SalaryMin = salaryAmount(r.Min)
	job.SalaryMax = salaryAmount(r.Max)
	job.SalaryCurrency = r.Currency
	job.SalaryPeriod = period

	if period == "yearly" && r.Min >= 1000 {
		job.Salary = fmt.Sprintf("$%sk–$%sk/yr %s",
			formatSalaryAmount(math.Round(r.Min/100)/10), formatSalaryAmount(math.Round(r.Max/100)/10), r.Currency)
		return
	}
	job.Salary = salaryText(r.Currency, r.Min, r.Max, period)
}

// leverInterval traduz o intervalo do Lever ("per-year-salary",
// "per-hour-wage"...) pros períodos usados no restante do projeto.
func leverInterval(interval string) string {
	i := strings.ToLower(interval)
	switch {
	case i == "":
		return "yearly" // padrão do Lever quando a faixa não diz o intervalo
	case strings.Contains(i, "year"):
		return "yearly"
	case strings.Contains(i, "month"):
		return "monthly"
	case strings.Contains(i, "bi-week"), strings.Contains(i, "biweek"):
		return "biweekly"
	case strings.Contains(i, "week"):
		return "weekly"
	case strings.Contains(i, "day"):
		return "daily"
	case strings.Contains(i, "hour"):
		return "hourly"
	}
	return i
}

func leverCreatedAt(unixMillis int64) time.Time {
	if unixMillis <= 0 {
		return time.Now()
	}
	return time.UnixMilli(unixMillis)
}

// capitalize deixa a primeira letra de s maiúscula (ex: "stripe" -> "Stripe").
// Suficiente pros slugs de empresa do Lever, que são uma única palavra.
func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
