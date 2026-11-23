package crawler

import (
	"log/slog"
	"math"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"job-scout/internal/models"
)

var (
	// "Remote (US)", "Remote (Canada; UK)", "Remote (FR; CH)"
	remoteParenRe = regexp.MustCompile(`(?i)remote\s*\(([^)]+)\)`)
	// "Remote - Germany", "Remote – LATAM", "Remote: Europe"
	remoteDashRe = regexp.MustCompile(`(?i)remote\s*[-–:]\s*([^,/;|()]+)`)
)

// extractRemoteRegions extrai as regiões de vagas remotas descritas nos
// textos de localização dos ATS (ex: "Remote (US)", "Remote - Germany") e
// as junta, sem repetição, para preencher Job.LocationRestriction. Retorna
// vazio se nenhum texto trouxer essa informação.
func extractRemoteRegions(locations ...string) string {
	seen := make(map[string]struct{})
	var regions []string
	add := func(r string) {
		for _, part := range strings.Split(r, ";") {
			part = strings.TrimSpace(part)
			key := strings.ToLower(part)
			if part == "" {
				continue
			}
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			regions = append(regions, part)
		}
	}
	for _, loc := range locations {
		for _, m := range remoteParenRe.FindAllStringSubmatch(loc, -1) {
			add(m[1])
		}
		for _, m := range remoteDashRe.FindAllStringSubmatch(loc, -1) {
			add(m[1])
		}
	}
	return strings.Join(regions, ", ")
}

// joinNonEmpty junta as partes não vazias (após TrimSpace) com sep.
func joinNonEmpty(sep string, parts ...string) string {
	var out []string
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, sep)
}

// salaryAmount arredonda um valor de salário da API — que pode ser
// decimal, como 24.5 por hora — pro inteiro mais próximo, já que
// SalaryMin/SalaryMax são inteiros. Decodifique sempre em float64: um
// campo int quebra o json.Unmarshal da resposta inteira no primeiro
// valor decimal.
func salaryAmount(v float64) int {
	return int(math.Round(v))
}

// formatSalaryAmount formata o valor sem casas decimais desnecessárias
// (120000, 24.5) pro texto exibido em Salary.
func formatSalaryAmount(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// salaryText monta o texto exibido em Salary: "USD 24.5–30 / hourly".
func salaryText(currency string, min, max float64, period string) string {
	s := strings.TrimSpace(currency + " " + formatSalaryAmount(min) + "–" + formatSalaryAmount(max))
	if period != "" {
		s += " / " + period
	}
	return s
}

// parseTimeOrNow faz o parse de s no layout informado e volta time.Now()
// se s estiver vazio ou inválido — as fontes nem sempre trazem a data.
func parseTimeOrNow(layout, s string) time.Time {
	t, err := time.Parse(layout, strings.TrimSpace(s))
	if err != nil {
		return time.Now()
	}
	return t
}

// unixOrNow converte segundos Unix em time.Time, voltando time.Now() se o
// valor não for positivo.
func unixOrNow(sec int64) time.Time {
	if sec <= 0 {
		return time.Now()
	}
	return time.Unix(sec, 0)
}

// atsConcurrency é quantas empresas um crawler de ATS (Ashby, Greenhouse,
// Workable, SmartRecruiters) consulta ao mesmo tempo. Cada worker ainda
// espera randomDelay entre as suas requisições.
const atsConcurrency = 5

// fetchCompanies roda fetch para cada empresa com concorrência limitada.
// A falha de uma empresa é logada e não interrompe as demais. source é usado
// só nos logs.
func fetchCompanies(source string, companies []string, fetch func(company string) ([]models.Job, error)) []models.Job {
	var (
		mu   sync.Mutex
		jobs []models.Job
		wg   sync.WaitGroup
	)

	queue := make(chan string)
	workers := min(atsConcurrency, len(companies))
	for range workers {
		wg.Go(func() {
			first := true
			for company := range queue {
				if !first {
					randomDelay()
				}
				first = false

				found, err := fetch(company)
				if err != nil {
					slog.Warn(source+": erro ao buscar vagas da empresa", "company", company, "error", err)
					continue
				}
				slog.Info(source+": empresa processada", "company", company, "vagas_encontradas", len(found))

				mu.Lock()
				jobs = append(jobs, found...)
				mu.Unlock()
			}
		})
	}

	for _, c := range companies {
		queue <- c
	}
	close(queue)
	wg.Wait()

	return jobs
}
