package crawler

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"job-scout/internal/models"
)

// smartRecruitersPostingsURLTemplate é a Posting API pública do
// SmartRecruiters: lista paginada (até 100 por página) das vagas de uma
// empresa. A lista não traz a descrição; ela vem do detalhe de cada vaga.
const smartRecruitersPostingsURLTemplate = "https://api.smartrecruiters.com/v1/companies/%s/postings?limit=%d&offset=%d"

const (
	smartRecruitersPageSize = 100
	// smartRecruitersMaxDetails limita quantas vagas filtradas por empresa
	// têm o detalhe (descrição) buscado — uma request por vaga.
	smartRecruitersMaxDetails = 20
)

type smartRecruitersList struct {
	TotalFound int                      `json:"totalFound"`
	Content    []smartRecruitersPosting `json:"content"`
}

type smartRecruitersPosting struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	ReleasedDate string `json:"releasedDate"`
	Ref          string `json:"ref"`
	Company      struct {
		Identifier string `json:"identifier"`
		Name       string `json:"name"`
	} `json:"company"`
	Location struct {
		Remote       bool   `json:"remote"`
		Hybrid       bool   `json:"hybrid"`
		FullLocation string `json:"fullLocation"`
	} `json:"location"`
	Function struct {
		Label string `json:"label"`
	} `json:"function"`
	TypeOfEmployment struct {
		Label string `json:"label"`
	} `json:"typeOfEmployment"`
}

type smartRecruitersDetail struct {
	PostingURL string `json:"postingUrl"`
	JobAd      struct {
		Sections map[string]struct {
			Title string `json:"title"`
			Text  string `json:"text"`
		} `json:"sections"`
	} `json:"jobAd"`
}

// smartRecruitersSectionOrder é a ordem em que as seções do anúncio entram
// na descrição.
var smartRecruitersSectionOrder = []string{"jobDescription", "qualifications", "additionalInformation", "companyDescription"}

// SmartRecruitersCrawler busca vagas nas páginas públicas do
// SmartRecruiters (jobs.smartrecruiters.com/{empresa}) de uma lista fixa de
// empresas.
type SmartRecruitersCrawler struct {
	companies   []string
	searchTerms []string
}

// NewSmartRecruitersCrawler cria um crawler para as empresas informadas
// (identificador como aparece na URL, ex: "Wix2"), filtrando pelos termos.
func NewSmartRecruitersCrawler(companies, searchTerms []string) *SmartRecruitersCrawler {
	return &SmartRecruitersCrawler{companies: companies, searchTerms: searchTerms}
}

func (c *SmartRecruitersCrawler) Name() string {
	return "smartrecruiters"
}

// IgnoresSearchTerms: Fetch é chamado uma vez e itera sobre as empresas.
func (c *SmartRecruitersCrawler) IgnoresSearchTerms() bool {
	return true
}

// Fetch busca as vagas de todas as empresas configuradas, em paralelo com
// concorrência limitada. maxPages limita as páginas (100 vagas) por empresa.
func (c *SmartRecruitersCrawler) Fetch(_ string, maxPages int) ([]models.Job, error) {
	return fetchCompanies(c.Name(), c.companies, func(company string) ([]models.Job, error) {
		return c.fetchCompany(company, max(maxPages, 1))
	}), nil
}

func (c *SmartRecruitersCrawler) fetchCompany(company string, maxPages int) ([]models.Job, error) {
	var postings []smartRecruitersPosting
	for page := 0; page < maxPages; page++ {
		if page > 0 {
			randomDelay()
		}
		reqURL := fmt.Sprintf(smartRecruitersPostingsURLTemplate, url.PathEscape(company), smartRecruitersPageSize, page*smartRecruitersPageSize)
		body, err := httpGetAPI(reqURL)
		if err != nil {
			return nil, fmt.Errorf("erro ao buscar vagas de %q no smartrecruiters: %w", company, err)
		}
		found, total, err := parseSmartRecruitersList(body)
		if err != nil {
			return nil, fmt.Errorf("erro ao parsear vagas de %q no smartrecruiters: %w", company, err)
		}
		postings = append(postings, found...)
		if (page+1)*smartRecruitersPageSize >= total {
			break
		}
	}

	var jobs []models.Job
	for i, p := range filterSmartRecruiters(postings, c.searchTerms) {
		var detail *smartRecruitersDetail
		if i < smartRecruitersMaxDetails && p.Ref != "" {
			randomDelay()
			if body, err := httpGetAPI(p.Ref); err != nil {
				slog.Warn("smartrecruiters: erro ao buscar detalhe da vaga", "company", company, "id", p.ID, "error", err)
			} else if d, err := parseSmartRecruitersDetail(body); err != nil {
				slog.Warn("smartrecruiters: erro ao parsear detalhe da vaga", "company", company, "id", p.ID, "error", err)
			} else {
				detail = &d
			}
		}
		jobs = append(jobs, smartRecruitersToJob(p, company, detail))
	}
	return jobs, nil
}

func parseSmartRecruitersList(body []byte) ([]smartRecruitersPosting, int, error) {
	var list smartRecruitersList
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, 0, err
	}
	return list.Content, list.TotalFound, nil
}

func parseSmartRecruitersDetail(body []byte) (smartRecruitersDetail, error) {
	var d smartRecruitersDetail
	err := json.Unmarshal(body, &d)
	return d, err
}

func filterSmartRecruiters(postings []smartRecruitersPosting, terms []string) []smartRecruitersPosting {
	var out []smartRecruitersPosting
	for _, p := range postings {
		if p.ID == "" || strings.TrimSpace(p.Name) == "" {
			continue
		}
		if matchesAnyTerm(p.Name+" "+p.Function.Label, terms) {
			out = append(out, p)
		}
	}
	return out
}

// smartRecruitersToJob monta a vaga a partir da lista e, se houver, do
// detalhe (URL pública e descrição).
func smartRecruitersToJob(p smartRecruitersPosting, company string, detail *smartRecruitersDetail) models.Job {
	jobURL := fmt.Sprintf("https://jobs.smartrecruiters.com/%s/%s", company, p.ID)
	var description string
	if detail != nil {
		if detail.PostingURL != "" {
			jobURL = detail.PostingURL
		}
		var parts []string
		for _, key := range smartRecruitersSectionOrder {
			if s, ok := detail.JobAd.Sections[key]; ok && strings.TrimSpace(stripHTML(s.Text)) != "" {
				parts = append(parts, s.Title+": "+stripHTML(s.Text))
			}
		}
		description = strings.Join(parts, "\n\n")
	}

	// fullLocation vem como "Cidade, Região, País" (com partes vazias).
	location := joinNonEmpty(", ", strings.Split(p.Location.FullLocation, ",")...)
	workplace := ""
	switch {
	case p.Location.Remote:
		workplace = "Remote"
	case p.Location.Hybrid:
		workplace = "Hybrid"
	}

	companyName := p.Company.Name
	if companyName == "" {
		companyName = company
	}

	job := models.Job{
		ID:          hashID(jobURL),
		Source:      "smartrecruiters",
		Title:       strings.TrimSpace(p.Name),
		Company:     companyName,
		Location:    joinNonEmpty(" · ", location, workplace, p.TypeOfEmployment.Label),
		URL:         jobURL,
		Description: description,
		FoundAt:     parseTimeOrNow(time.RFC3339, p.ReleasedDate),
		Status:      "new",
	}
	// Vaga remota no SmartRecruiters é atrelada ao país da localização.
	if p.Location.Remote {
		parts := strings.Split(p.Location.FullLocation, ",")
		job.LocationRestriction = strings.TrimSpace(parts[len(parts)-1])
	}
	return job
}
