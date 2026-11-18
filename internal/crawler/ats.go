package crawler

import (
	"log/slog"
	"sync"

	"job-scout/internal/models"
)

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
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
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
		}()
	}

	for _, c := range companies {
		queue <- c
	}
	close(queue)
	wg.Wait()

	return jobs
}
