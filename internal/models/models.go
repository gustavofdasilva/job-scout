package models

import (
	"strings"
	"time"
	"unicode"
)

// Status de vaga.
const (
	JobStatusNew = "new"
	// JobStatusFiltered marca vagas descartadas pelo job_filter sem passar
	// pelo LLM; o motivo fica em Job.FilterReason.
	JobStatusFiltered = "filtrada"
)

// Job representa uma vaga encontrada por um crawler.
type Job struct {
	ID          string    `json:"id"`
	Source      string    `json:"source"`
	Title       string    `json:"title"`
	Company     string    `json:"company"`
	Location    string    `json:"location"`
	URL         string    `json:"url"`
	Description string    `json:"description"`
	Salary      string    `json:"salary"`
	FoundAt     time.Time `json:"found_at"`
	Status      string    `json:"status"`

	// LocationRestriction lista os países/regiões aceitos por uma vaga
	// remota (ex: "USA", "Worldwide", "LATAM, Brazil"), quando a fonte
	// informa isso explicitamente. Vazio = sem informação.
	LocationRestriction string `json:"location_restriction"`

	// Salário estruturado, preenchido só quando a fonte traz os valores
	// separados. Salary (texto livre) continua sendo o campo exibido.
	SalaryMin      int    `json:"salary_min"`
	SalaryMax      int    `json:"salary_max"`
	SalaryCurrency string `json:"salary_currency"`
	SalaryPeriod   string `json:"salary_period"`

	// FilterReason é o motivo pelo qual o job_filter descartou a vaga
	// ("title_exclude" ou "sem Go"); vazio se ela não foi filtrada.
	FilterReason string `json:"filter_reason"`
}

// DedupKey identifica a mesma vaga publicada em fontes diferentes (ex: no
// ATS da empresa e num board remoto), combinando empresa e título
// normalizados: minúsculas e só letras/dígitos. Vazio se faltar empresa ou
// título.
func (j Job) DedupKey() string {
	company, title := normalizeKeyPart(j.Company), normalizeKeyPart(j.Title)
	if company == "" || title == "" {
		return ""
	}
	return company + "|" + title
}

func normalizeKeyPart(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Analysis representa o resultado da análise de fit de uma vaga feita pela LLM.
type Analysis struct {
	ID             string    `json:"id"`
	JobID          string    `json:"job_id"`
	FitScore       int       `json:"fit_score"`
	Summary        string    `json:"summary"`
	Benefits       string    `json:"benefits"`
	FitReasoning   string    `json:"fit_reasoning"`
	ResumeMD       string    `json:"resume_md"`
	ResumePDFPath  string    `json:"resume_pdf_path"`
	ResumeKeywords string    `json:"resume_keywords"`
	ResumePoints   string    `json:"resume_points"`
	CreatedAt      time.Time `json:"created_at"`
}

// Profile representa um par chave/valor do perfil do usuário armazenado no banco.
type Profile struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}
