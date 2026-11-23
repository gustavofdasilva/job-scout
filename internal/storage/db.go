package storage

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"job-scout/internal/models"
)

// DB encapsula a conexão com o banco SQLite e as operações do job-scout.
type DB struct {
	conn *sql.DB
}

// dsnParams fixa como o driver grava e lê datas. Sem _time_format, o
// modernc.org/sqlite grava time.Time com t.String() ("2026-11-20 10:00:00
// -0500 -0500", às vezes com " m=+1.23"), e esse formato só volta a virar
// time.Time quando o fuso tem nome (UTC, -03...): datas vindas de APIs com
// offset fixo (RFC3339 "-05:00", comum em Greenhouse/Ashby/SmartRecruiters)
// voltavam como string e quebravam o Scan em *time.Time. Com "sqlite" o
// formato é "2006-01-02 15:04:05.999999999-07:00", que o driver sempre
// reconhece; _timezone=UTC grava tudo em UTC, o que mantém a ordenação
// textual (ORDER BY found_at) igual à cronológica.
const dsnParams = "_time_format=sqlite&_timezone=UTC"

// Open conecta ao banco SQLite no caminho informado e garante que as tabelas existam.
func Open(path string) (*DB, error) {
	dsn := path + "?" + dsnParams
	if strings.Contains(path, "?") {
		dsn = path + "&" + dsnParams
	}
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("erro ao abrir banco de dados %q: %w", path, err)
	}
	if err := conn.Ping(); err != nil {
		return nil, fmt.Errorf("erro ao conectar ao banco de dados %q: %w", path, err)
	}

	db := &DB{conn: conn}
	if err := db.migrate(); err != nil {
		return nil, err
	}
	return db, nil
}

// Close fecha a conexão com o banco de dados.
func (db *DB) Close() error {
	return db.conn.Close()
}

func (db *DB) migrate() error {
	schema := `
CREATE TABLE IF NOT EXISTS jobs (
	id TEXT PRIMARY KEY,
	source TEXT NOT NULL,
	title TEXT NOT NULL,
	company TEXT NOT NULL,
	location TEXT,
	url TEXT NOT NULL UNIQUE,
	description TEXT,
	salary TEXT,
	found_at DATETIME NOT NULL,
	status TEXT NOT NULL DEFAULT 'new'
);

CREATE TABLE IF NOT EXISTS analyses (
	id TEXT PRIMARY KEY,
	job_id TEXT NOT NULL REFERENCES jobs(id),
	fit_score INTEGER NOT NULL,
	summary TEXT,
	benefits TEXT,
	fit_reasoning TEXT,
	resume_md TEXT,
	resume_pdf_path TEXT,
	resume_keywords TEXT,
	resume_points TEXT,
	created_at DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS profile (
	key TEXT PRIMARY KEY,
	value TEXT
);
`
	if _, err := db.conn.Exec(schema); err != nil {
		return fmt.Errorf("erro ao criar tabelas: %w", err)
	}

	// CREATE TABLE IF NOT EXISTS não adiciona colunas a tabelas já
	// existentes — bancos criados antes do checklist de currículo existir
	// precisam dessas colunas adicionadas manualmente.
	if err := addColumnIfMissing(db.conn, "analyses", "resume_keywords", "TEXT"); err != nil {
		return err
	}
	if err := addColumnIfMissing(db.conn, "analyses", "resume_points", "TEXT"); err != nil {
		return err
	}
	if err := addColumnIfMissing(db.conn, "jobs", "analysis_attempts", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}

	// Restrição de localização e salário estruturado, preenchidos pelas
	// fontes que trazem esses dados (Himalayas, Jobicy etc.).
	newJobColumns := []struct{ name, sqlType string }{
		{"location_restriction", "TEXT NOT NULL DEFAULT ''"},
		{"salary_min", "INTEGER NOT NULL DEFAULT 0"},
		{"salary_max", "INTEGER NOT NULL DEFAULT 0"},
		{"salary_currency", "TEXT NOT NULL DEFAULT ''"},
		{"salary_period", "TEXT NOT NULL DEFAULT ''"},
		{"dedup_key", "TEXT NOT NULL DEFAULT ''"},
	}
	for _, c := range newJobColumns {
		if err := addColumnIfMissing(db.conn, "jobs", c.name, c.sqlType); err != nil {
			return err
		}
	}
	if _, err := db.conn.Exec(`CREATE INDEX IF NOT EXISTS idx_jobs_dedup_key ON jobs(dedup_key)`); err != nil {
		return fmt.Errorf("erro ao criar índice de dedup_key: %w", err)
	}
	// Usado pela subquery "análise mais recente da vaga" da listagem.
	if _, err := db.conn.Exec(`CREATE INDEX IF NOT EXISTS idx_analyses_job_created ON analyses(job_id, created_at)`); err != nil {
		return fmt.Errorf("erro ao criar índice de analyses: %w", err)
	}

	if err := db.backfillDedupKeys(); err != nil {
		return err
	}
	return db.normalizeTimestamps()
}

// timestampColumns são as colunas DATETIME reescritas por
// normalizeTimestamps.
var timestampColumns = []struct{ table, column string }{
	{"jobs", "found_at"},
	{"analyses", "created_at"},
}

// storedTimeLayout é o formato gravado pelo driver com _time_format=sqlite.
const storedTimeLayout = "2006-01-02 15:04:05.999999999-07:00"

// normalizeTimestamps reescreve no formato de storedTimeLayout, em UTC, as
// datas gravadas antes de dsnParams existir (formato t.String(), com fusos
// variados). Linhas já em UTC terminam em "+00:00" e são puladas, então
// depois da primeira execução isso é um no-op.
func (db *DB) normalizeTimestamps() error {
	for _, tc := range timestampColumns {
		// CAST tira o tipo declarado da coluna: o driver devolve o texto
		// cru em vez de tentar (e falhar) convertê-lo em time.Time.
		rows, err := db.conn.Query(fmt.Sprintf(
			`SELECT rowid, CAST(%[2]s AS TEXT) FROM %[1]s WHERE typeof(%[2]s) = 'text' AND %[2]s NOT LIKE '%%+00:00'`,
			tc.table, tc.column,
		))
		if err != nil {
			return fmt.Errorf("erro ao buscar datas de %s.%s: %w", tc.table, tc.column, err)
		}
		type fix struct {
			rowid int64
			value string
		}
		var fixes []fix
		for rows.Next() {
			var rowid int64
			var raw string
			if err := rows.Scan(&rowid, &raw); err != nil {
				rows.Close()
				return fmt.Errorf("erro ao ler data de %s.%s: %w", tc.table, tc.column, err)
			}
			t, err := parseStoredTime(raw)
			if err != nil {
				slog.Warn("storage: data não normalizada", "table", tc.table, "column", tc.column, "rowid", rowid, "error", err)
				continue
			}
			fixes = append(fixes, fix{rowid, t.UTC().Format(storedTimeLayout)})
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return fmt.Errorf("erro ao iterar datas de %s.%s: %w", tc.table, tc.column, err)
		}

		for _, f := range fixes {
			if _, err := db.conn.Exec(fmt.Sprintf(`UPDATE %s SET %s = ? WHERE rowid = ?`, tc.table, tc.column), f.value, f.rowid); err != nil {
				return fmt.Errorf("erro ao normalizar data de %s.%s (rowid %d): %w", tc.table, tc.column, f.rowid, err)
			}
		}
	}
	return nil
}

// parseStoredTime entende os formatos já gravados no banco: o de
// time.Time.String() (com ou sem " m=+..." e com qualquer nome de fuso,
// inclusive "-0500") e os formatos ISO que o SQLite aceita.
func parseStoredTime(raw string) (time.Time, error) {
	s := strings.TrimSpace(raw)
	if i := strings.Index(s, " m="); i >= 0 {
		s = s[:i]
	}
	// t.String() = "data hora offset nomeDoFuso": o nome é redundante com o
	// offset e é justamente o que o time.Parse não aceita quando vem como
	// "-0500", então descartamos.
	if f := strings.Fields(s); len(f) >= 3 {
		if t, err := time.Parse("2006-01-02 15:04:05.999999999 -0700", f[0]+" "+f[1]+" "+f[2]); err == nil {
			return t, nil
		}
	}
	for _, layout := range []string{
		storedTimeLayout,
		"2006-01-02T15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999Z07:00",
		time.RFC3339Nano,
		"2006-01-02 15:04:05.999999999",
		"2006-01-02T15:04:05.999999999",
		"2006-01-02",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("formato de data desconhecido: %q", raw)
}

// backfillDedupKeys preenche dedup_key das vagas gravadas antes da coluna
// existir, pra que a deduplicação por empresa+título também as considere.
// Depois da primeira execução não há mais linhas vazias e isso é um no-op.
func (db *DB) backfillDedupKeys() error {
	rows, err := db.conn.Query(`SELECT id, company, title FROM jobs WHERE dedup_key = ''`)
	if err != nil {
		return fmt.Errorf("erro ao buscar vagas sem dedup_key: %w", err)
	}
	var pending []models.Job
	for rows.Next() {
		var j models.Job
		if err := rows.Scan(&j.ID, &j.Company, &j.Title); err != nil {
			rows.Close()
			return fmt.Errorf("erro ao ler vaga sem dedup_key: %w", err)
		}
		pending = append(pending, j)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("erro ao iterar vagas sem dedup_key: %w", err)
	}

	for _, j := range pending {
		key := j.DedupKey()
		if key == "" {
			continue
		}
		if _, err := db.conn.Exec(`UPDATE jobs SET dedup_key = ? WHERE id = ?`, key, j.ID); err != nil {
			return fmt.Errorf("erro ao preencher dedup_key da vaga %q: %w", j.ID, err)
		}
	}
	return nil
}

// addColumnIfMissing tenta adicionar a coluna à tabela, ignorando o erro caso
// ela já exista (SQLite não suporta "ADD COLUMN IF NOT EXISTS").
func addColumnIfMissing(conn *sql.DB, table, column, sqlType string) error {
	_, err := conn.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, sqlType))
	if err != nil && !strings.Contains(err.Error(), "duplicate column name") {
		return fmt.Errorf("erro ao adicionar coluna %q em %q: %w", column, table, err)
	}
	return nil
}

func newID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// InsertJob insere uma nova vaga no banco. Se ID, FoundAt ou Status estiverem
// vazios, valores padrão são gerados automaticamente.
func (db *DB) InsertJob(job models.Job) error {
	if job.ID == "" {
		id, err := newID()
		if err != nil {
			return fmt.Errorf("erro ao gerar id da vaga: %w", err)
		}
		job.ID = id
	}
	if job.FoundAt.IsZero() {
		job.FoundAt = time.Now()
	}
	if job.Status == "" {
		job.Status = "new"
	}

	_, err := db.conn.Exec(
		`INSERT INTO jobs (id, source, title, company, location, url, description, salary, found_at, status,
		                   location_restriction, salary_min, salary_max, salary_currency, salary_period, dedup_key)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		job.ID, job.Source, job.Title, job.Company, job.Location, job.URL, job.Description, job.Salary, job.FoundAt, job.Status,
		job.LocationRestriction, job.SalaryMin, job.SalaryMax, job.SalaryCurrency, job.SalaryPeriod, job.DedupKey(),
	)
	if err != nil {
		return fmt.Errorf("erro ao inserir vaga: %w", err)
	}
	return nil
}

// JobExistsByURL verifica se já existe uma vaga cadastrada com a URL informada.
func (db *DB) JobExistsByURL(url string) bool {
	var count int
	err := db.conn.QueryRow(`SELECT COUNT(1) FROM jobs WHERE url = ?`, url).Scan(&count)
	if err != nil {
		return false
	}
	return count > 0
}

// JobExistsByDedupKey verifica se já existe uma vaga da mesma empresa com o
// mesmo título (ver models.Job.DedupKey), mesmo que vinda de outra fonte ou
// com outra URL. Chave vazia nunca é considerada duplicata.
func (db *DB) JobExistsByDedupKey(key string) bool {
	if key == "" {
		return false
	}
	var count int
	err := db.conn.QueryRow(`SELECT COUNT(1) FROM jobs WHERE dedup_key = ?`, key).Scan(&count)
	if err != nil {
		return false
	}
	return count > 0
}

// GetPendingAnalysisJobs retorna as vagas que ainda não têm nenhuma análise.
// Inclui tanto vagas recém-inseridas quanto vagas de ciclos anteriores cuja
// análise falhou (erro do LLM, API key ausente etc.), que de outra forma
// nunca seriam analisadas porque o dedup por URL as considera "já
// existentes". Vagas que já falharam maxAttempts vezes são ignoradas, pra
// uma vaga que sempre falha não ser retentada para sempre, assim como as
// descartadas pelo filtro de título. limit <= 0 e maxAttempts <= 0
// desativam o respectivo limite.
//
// Ordem: primeiro as vagas de prioritySources (ATS e boards remotos), depois
// as demais; em cada grupo, das mais recentes para as mais antigas.
func (db *DB) GetPendingAnalysisJobs(limit, maxAttempts int, prioritySources []string) ([]models.Job, error) {
	query := `
		SELECT j.id, j.source, j.title, j.company, j.location, j.url, j.description, j.salary, j.found_at, j.status, j.location_restriction, j.salary_min, j.salary_max, j.salary_currency, j.salary_period
		FROM jobs j
		WHERE NOT EXISTS (SELECT 1 FROM analyses a WHERE a.job_id = j.id)
		  AND j.status != ?
	`
	args := []any{models.JobStatusFiltered}
	if maxAttempts > 0 {
		query += " AND j.analysis_attempts < ?"
		args = append(args, maxAttempts)
	}
	query += " ORDER BY "
	if len(prioritySources) > 0 {
		query += "CASE WHEN j.source IN (?" + strings.Repeat(", ?", len(prioritySources)-1) + ") THEN 0 ELSE 1 END, "
		for _, s := range prioritySources {
			args = append(args, s)
		}
	}
	query += "j.found_at DESC"
	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}

	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("erro ao buscar vagas pendentes de análise: %w", err)
	}
	defer rows.Close()

	var jobs []models.Job
	for rows.Next() {
		var j models.Job
		if err := rows.Scan(&j.ID, &j.Source, &j.Title, &j.Company, &j.Location, &j.URL, &j.Description, &j.Salary, &j.FoundAt, &j.Status, &j.LocationRestriction, &j.SalaryMin, &j.SalaryMax, &j.SalaryCurrency, &j.SalaryPeriod); err != nil {
			return nil, fmt.Errorf("erro ao ler vaga pendente de análise: %w", err)
		}
		jobs = append(jobs, j)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("erro ao iterar vagas pendentes de análise: %w", err)
	}

	return jobs, nil
}

// CountPendingAnalysisJobs conta as vagas que GetPendingAnalysisJobs
// retornaria sem limite por ciclo.
func (db *DB) CountPendingAnalysisJobs(maxAttempts int) (int, error) {
	query := `SELECT COUNT(1) FROM jobs j
		WHERE NOT EXISTS (SELECT 1 FROM analyses a WHERE a.job_id = j.id) AND j.status != ?`
	args := []any{models.JobStatusFiltered}
	if maxAttempts > 0 {
		query += " AND j.analysis_attempts < ?"
		args = append(args, maxAttempts)
	}
	var n int
	if err := db.conn.QueryRow(query, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("erro ao contar vagas pendentes de análise: %w", err)
	}
	return n, nil
}

// GetUnanalyzedNewJobTitles retorna id e título das vagas com status "new"
// que ainda não têm análise — as candidatas ao filtro de título.
func (db *DB) GetUnanalyzedNewJobTitles() ([]models.Job, error) {
	rows, err := db.conn.Query(`
		SELECT j.id, j.title FROM jobs j
		WHERE j.status = ? AND NOT EXISTS (SELECT 1 FROM analyses a WHERE a.job_id = j.id)
	`, models.JobStatusNew)
	if err != nil {
		return nil, fmt.Errorf("erro ao buscar vagas não analisadas: %w", err)
	}
	defer rows.Close()

	var jobs []models.Job
	for rows.Next() {
		var j models.Job
		if err := rows.Scan(&j.ID, &j.Title); err != nil {
			return nil, fmt.Errorf("erro ao ler vaga não analisada: %w", err)
		}
		jobs = append(jobs, j)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("erro ao iterar vagas não analisadas: %w", err)
	}
	return jobs, nil
}

// MarkJobsFiltered muda para "filtered" o status das vagas informadas, numa
// transação só. Retorna quantas foram atualizadas.
func (db *DB) MarkJobsFiltered(ids []string) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	tx, err := db.conn.Begin()
	if err != nil {
		return 0, fmt.Errorf("erro ao iniciar transação: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`UPDATE jobs SET status = ? WHERE id = ?`)
	if err != nil {
		return 0, fmt.Errorf("erro ao preparar atualização de status: %w", err)
	}
	defer stmt.Close()

	var n int
	for _, id := range ids {
		res, err := stmt.Exec(models.JobStatusFiltered, id)
		if err != nil {
			return 0, fmt.Errorf("erro ao marcar vaga %q como filtrada: %w", id, err)
		}
		if affected, err := res.RowsAffected(); err == nil {
			n += int(affected)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("erro ao confirmar vagas filtradas: %w", err)
	}
	return n, nil
}

// IncrementAnalysisAttempts registra mais uma tentativa de análise que falhou
// para a vaga e retorna o total de tentativas após o incremento.
func (db *DB) IncrementAnalysisAttempts(jobID string) (int, error) {
	var attempts int
	err := db.conn.QueryRow(
		`UPDATE jobs SET analysis_attempts = analysis_attempts + 1 WHERE id = ? RETURNING analysis_attempts`,
		jobID,
	).Scan(&attempts)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, fmt.Errorf("vaga %q não encontrada", jobID)
		}
		return 0, fmt.Errorf("erro ao incrementar tentativas de análise da vaga %q: %w", jobID, err)
	}
	return attempts, nil
}

// LatestJobFoundAt retorna o found_at mais recente entre as vagas de uma
// fonte específica (ex: "hackernews"). O segundo valor é false se nenhuma
// vaga dessa fonte existir ainda. Usado por crawlers que só devem rodar de
// tempos em tempos (ex: threads mensais) pra saber quando rodaram por
// último.
func (db *DB) LatestJobFoundAt(source string) (time.Time, bool, error) {
	var foundAt time.Time
	err := db.conn.QueryRow(
		`SELECT found_at FROM jobs WHERE source = ? ORDER BY found_at DESC LIMIT 1`, source,
	).Scan(&foundAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return time.Time{}, false, nil
		}
		return time.Time{}, false, fmt.Errorf("erro ao buscar found_at mais recente da fonte %q: %w", source, err)
	}
	return foundAt, true, nil
}

// GetJobs retorna as vagas filtradas por status e/ou pontuação mínima de fit.
// Strings vazias e valores <= 0 desativam o respectivo filtro.
func (db *DB) GetJobs(status string, minScore int) ([]models.Job, error) {
	query := `
		SELECT DISTINCT j.id, j.source, j.title, j.company, j.location, j.url, j.description, j.salary, j.found_at, j.status, j.location_restriction, j.salary_min, j.salary_max, j.salary_currency, j.salary_period
		FROM jobs j
		LEFT JOIN analyses a ON a.job_id = j.id
		WHERE 1=1
	`
	args := []any{}

	if status != "" {
		query += " AND j.status = ?"
		args = append(args, status)
	}
	if minScore > 0 {
		query += " AND a.fit_score >= ?"
		args = append(args, minScore)
	}
	query += " ORDER BY j.found_at DESC"

	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("erro ao buscar vagas: %w", err)
	}
	defer rows.Close()

	var jobs []models.Job
	for rows.Next() {
		var j models.Job
		if err := rows.Scan(&j.ID, &j.Source, &j.Title, &j.Company, &j.Location, &j.URL, &j.Description, &j.Salary, &j.FoundAt, &j.Status, &j.LocationRestriction, &j.SalaryMin, &j.SalaryMax, &j.SalaryCurrency, &j.SalaryPeriod); err != nil {
			return nil, fmt.Errorf("erro ao ler vaga: %w", err)
		}
		jobs = append(jobs, j)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("erro ao iterar vagas: %w", err)
	}

	return jobs, nil
}

// JobWithAnalysis representa uma vaga junto com os dados da sua análise mais
// recente (se houver). HasAnalysis é false quando a vaga ainda não foi
// analisada, caso em que os demais campos de análise vêm zerados.
type JobWithAnalysis struct {
	models.Job
	FitScore       int    `json:"fit_score"`
	Summary        string `json:"summary"`
	Benefits       string `json:"benefits"`
	FitReasoning   string `json:"fit_reasoning"`
	ResumePDFPath  string `json:"resume_pdf_path"`
	ResumeKeywords string `json:"resume_keywords"`
	ResumePoints   string `json:"resume_points"`
	HasAnalysis    bool   `json:"has_analysis"`
}

// jobsWithAnalysisSelect é a base da query usada tanto por
// GetJobsWithAnalysis (lista, com filtros) quanto por GetJobWithAnalysisByID
// (uma vaga só) — sempre traz a análise mais recente de cada vaga (se houver).
const jobsWithAnalysisSelect = `
	SELECT j.id, j.source, j.title, j.company, j.location, j.url, j.description, j.salary, j.found_at, j.status, j.location_restriction, j.salary_min, j.salary_max, j.salary_currency, j.salary_period,
	       COALESCE(a.id, ''), COALESCE(a.fit_score, 0), COALESCE(a.summary, ''), COALESCE(a.benefits, ''), COALESCE(a.fit_reasoning, ''),
	       COALESCE(a.resume_pdf_path, ''), COALESCE(a.resume_keywords, ''), COALESCE(a.resume_points, '')
` + jobsWithAnalysisFrom

// rowScanner é satisfeita tanto por *sql.Row quanto por *sql.Rows, permitindo
// reaproveitar scanJobWithAnalysis nos dois casos (uma linha ou várias).
type rowScanner interface {
	Scan(dest ...any) error
}

func scanJobWithAnalysis(row rowScanner) (JobWithAnalysis, error) {
	var item JobWithAnalysis
	var analysisID string
	if err := row.Scan(
		&item.ID, &item.Source, &item.Title, &item.Company, &item.Location, &item.URL, &item.Description, &item.Salary, &item.FoundAt, &item.Status, &item.LocationRestriction, &item.SalaryMin, &item.SalaryMax, &item.SalaryCurrency, &item.SalaryPeriod,
		&analysisID, &item.FitScore, &item.Summary, &item.Benefits, &item.FitReasoning,
		&item.ResumePDFPath, &item.ResumeKeywords, &item.ResumePoints,
	); err != nil {
		return JobWithAnalysis{}, err
	}
	item.HasAnalysis = analysisID != ""
	return item, nil
}

// JobFilter filtra e pagina a listagem de vagas do dashboard. Campos
// zerados desativam o respectivo filtro; Limit <= 0 traz todas as vagas.
type JobFilter struct {
	// Status vazio lista todas as vagas menos as descartadas pelo filtro de
	// título ("filtered"), que só aparecem pedindo esse status.
	Status string
	// MinScore só se aplica a vagas já analisadas: as ainda não analisadas
	// continuam aparecendo (no fim da lista, com score 0), como o dashboard
	// sempre as mostrou.
	MinScore int
	Source   string
	Limit    int
	Offset   int
}

// where monta a cláusula WHERE (sem status quando withStatus é false) e
// seus argumentos. Espera os aliases j (jobs) e a (análise mais recente)
// de jobsWithAnalysisSelect.
func (f JobFilter) where(withStatus bool) (string, []any) {
	clause := " WHERE 1=1"
	args := []any{}
	if withStatus {
		if f.Status != "" {
			clause += " AND j.status = ?"
			args = append(args, f.Status)
		} else {
			clause += " AND j.status != ?"
			args = append(args, models.JobStatusFiltered)
		}
	}
	if f.MinScore > 0 {
		clause += " AND (a.id IS NULL OR a.fit_score >= ?)"
		args = append(args, f.MinScore)
	}
	if f.Source != "" {
		clause += " AND j.source = ?"
		args = append(args, f.Source)
	}
	return clause, args
}

// jobsWithAnalysisFrom é o FROM de jobsWithAnalysisSelect, reaproveitado
// pelas contagens.
const jobsWithAnalysisFrom = `
	FROM jobs j
	LEFT JOIN analyses a ON a.id = (
		SELECT id FROM analyses WHERE job_id = j.id ORDER BY created_at DESC LIMIT 1
	)
`

// GetJobsWithAnalysis retorna uma página das vagas com os dados da análise
// mais recente de cada uma (se houver), filtradas por f, e o total de
// vagas que passam pelos filtros (ignorando Limit/Offset). O resultado vem
// ordenado por fit_score e found_at decrescentes.
func (db *DB) GetJobsWithAnalysis(f JobFilter) ([]JobWithAnalysis, int, error) {
	where, args := f.where(true)

	var total int
	if err := db.conn.QueryRow("SELECT COUNT(1)"+jobsWithAnalysisFrom+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("erro ao contar vagas com análise: %w", err)
	}

	// j.id desempata vagas com mesmo score e data, pra paginação por
	// offset não repetir nem pular vagas entre páginas.
	query := jobsWithAnalysisSelect + where + " ORDER BY COALESCE(a.fit_score, 0) DESC, j.found_at DESC, j.id"
	if f.Limit > 0 {
		query += " LIMIT ? OFFSET ?"
		args = append(args, f.Limit, max(f.Offset, 0))
	}

	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("erro ao buscar vagas com análise: %w", err)
	}
	defer rows.Close()

	results := []JobWithAnalysis{}
	for rows.Next() {
		item, err := scanJobWithAnalysis(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("erro ao ler vaga com análise: %w", err)
		}
		results = append(results, item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("erro ao iterar vagas com análise: %w", err)
	}

	return results, total, nil
}

// CountJobsWithAnalysisByStatus conta as vagas por status aplicando os
// demais filtros de f (score mínimo, fonte) — os números das abas do
// dashboard. f.Status, Limit e Offset são ignorados.
func (db *DB) CountJobsWithAnalysisByStatus(f JobFilter) (map[string]int, error) {
	where, args := f.where(false)
	rows, err := db.conn.Query("SELECT j.status, COUNT(1)"+jobsWithAnalysisFrom+where+" GROUP BY j.status", args...)
	if err != nil {
		return nil, fmt.Errorf("erro ao contar vagas por status: %w", err)
	}
	defer rows.Close()

	counts := make(map[string]int)
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return nil, fmt.Errorf("erro ao ler contagem por status: %w", err)
		}
		counts[status] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("erro ao iterar contagem por status: %w", err)
	}
	return counts, nil
}

// ListSources retorna as fontes que já têm vagas no banco, em ordem
// alfabética (opções do filtro de fonte do dashboard).
func (db *DB) ListSources() ([]string, error) {
	rows, err := db.conn.Query(`SELECT DISTINCT source FROM jobs ORDER BY source`)
	if err != nil {
		return nil, fmt.Errorf("erro ao listar fontes: %w", err)
	}
	defer rows.Close()

	sources := []string{}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, fmt.Errorf("erro ao ler fonte: %w", err)
		}
		sources = append(sources, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("erro ao iterar fontes: %w", err)
	}
	return sources, nil
}

// GetJobWithAnalysisByID retorna uma única vaga com os dados da sua análise
// mais recente (se houver), no mesmo formato "achatado" de
// GetJobsWithAnalysis — usado pelos endpoints que geram currículo/checklist
// sob demanda pra devolver a vaga já atualizada.
func (db *DB) GetJobWithAnalysisByID(id string) (JobWithAnalysis, error) {
	row := db.conn.QueryRow(jobsWithAnalysisSelect+" WHERE j.id = ?", id)
	item, err := scanJobWithAnalysis(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return JobWithAnalysis{}, fmt.Errorf("vaga %q não encontrada: %w", id, err)
		}
		return JobWithAnalysis{}, fmt.Errorf("erro ao buscar vaga %q com análise: %w", id, err)
	}
	return item, nil
}

// CountJobsByStatus retorna o total de vagas agrupado por status.
func (db *DB) CountJobsByStatus() (map[string]int, error) {
	rows, err := db.conn.Query(`SELECT status, COUNT(1) FROM jobs GROUP BY status`)
	if err != nil {
		return nil, fmt.Errorf("erro ao contar vagas por status: %w", err)
	}
	defer rows.Close()

	counts := make(map[string]int)
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return nil, fmt.Errorf("erro ao ler contagem por status: %w", err)
		}
		counts[status] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("erro ao iterar contagem por status: %w", err)
	}

	return counts, nil
}

// GetJobByID retorna a vaga e sua análise mais recente (se existir).
func (db *DB) GetJobByID(id string) (models.Job, models.Analysis, error) {
	var job models.Job
	err := db.conn.QueryRow(
		`SELECT id, source, title, company, location, url, description, salary, found_at, status, location_restriction, salary_min, salary_max, salary_currency, salary_period FROM jobs WHERE id = ?`,
		id,
	).Scan(&job.ID, &job.Source, &job.Title, &job.Company, &job.Location, &job.URL, &job.Description, &job.Salary, &job.FoundAt, &job.Status, &job.LocationRestriction, &job.SalaryMin, &job.SalaryMax, &job.SalaryCurrency, &job.SalaryPeriod)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return models.Job{}, models.Analysis{}, fmt.Errorf("vaga %q não encontrada: %w", id, err)
		}
		return models.Job{}, models.Analysis{}, fmt.Errorf("erro ao buscar vaga %q: %w", id, err)
	}

	var analysis models.Analysis
	err = db.conn.QueryRow(
		`SELECT id, job_id, fit_score, summary, benefits, fit_reasoning, resume_md, resume_pdf_path, COALESCE(resume_keywords, ''), COALESCE(resume_points, ''), created_at
		 FROM analyses WHERE job_id = ? ORDER BY created_at DESC LIMIT 1`,
		id,
	).Scan(&analysis.ID, &analysis.JobID, &analysis.FitScore, &analysis.Summary, &analysis.Benefits, &analysis.FitReasoning, &analysis.ResumeMD, &analysis.ResumePDFPath, &analysis.ResumeKeywords, &analysis.ResumePoints, &analysis.CreatedAt)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return job, models.Analysis{}, fmt.Errorf("erro ao buscar análise da vaga %q: %w", id, err)
	}

	return job, analysis, nil
}

// InsertAnalysis insere uma nova análise no banco e retorna o ID usado. Se ID
// ou CreatedAt estiverem vazios, valores padrão são gerados automaticamente.
func (db *DB) InsertAnalysis(a models.Analysis) (string, error) {
	if a.ID == "" {
		id, err := newID()
		if err != nil {
			return "", fmt.Errorf("erro ao gerar id da análise: %w", err)
		}
		a.ID = id
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now()
	}

	_, err := db.conn.Exec(
		`INSERT INTO analyses (id, job_id, fit_score, summary, benefits, fit_reasoning, resume_md, resume_pdf_path, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.JobID, a.FitScore, a.Summary, a.Benefits, a.FitReasoning, a.ResumeMD, a.ResumePDFPath, a.CreatedAt,
	)
	if err != nil {
		return "", fmt.Errorf("erro ao inserir análise: %w", err)
	}
	return a.ID, nil
}

// UpdateAnalysisResult atualiza o resultado de fit (fit_score, summary,
// benefits, fit_reasoning e created_at) de uma análise já existente,
// preservando currículo/checklist já gerados para ela. Usado ao reanalisar
// uma vaga manualmente pelo dashboard, pra não perder o que já foi gerado a
// partir da análise anterior.
func (db *DB) UpdateAnalysisResult(analysisID string, a models.Analysis) error {
	res, err := db.conn.Exec(
		`UPDATE analyses SET fit_score = ?, summary = ?, benefits = ?, fit_reasoning = ?, created_at = ? WHERE id = ?`,
		a.FitScore, a.Summary, a.Benefits, a.FitReasoning, time.Now(), analysisID,
	)
	if err != nil {
		return fmt.Errorf("erro ao atualizar análise %q: %w", analysisID, err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("erro ao verificar atualização da análise %q: %w", analysisID, err)
	}
	if rows == 0 {
		return fmt.Errorf("análise %q não encontrada", analysisID)
	}
	return nil
}

// UpdateJobStatus atualiza o status de uma vaga existente.
func (db *DB) UpdateJobStatus(id string, status string) error {
	res, err := db.conn.Exec(`UPDATE jobs SET status = ? WHERE id = ?`, status, id)
	if err != nil {
		return fmt.Errorf("erro ao atualizar status da vaga %q: %w", id, err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("erro ao verificar atualização da vaga %q: %w", id, err)
	}
	if rows == 0 {
		return fmt.Errorf("vaga %q não encontrada", id)
	}
	return nil
}

// UpdateResumePath atualiza o caminho do PDF do currículo gerado para uma análise.
func (db *DB) UpdateResumePath(analysisID string, path string) error {
	res, err := db.conn.Exec(`UPDATE analyses SET resume_pdf_path = ? WHERE id = ?`, path, analysisID)
	if err != nil {
		return fmt.Errorf("erro ao atualizar caminho do currículo da análise %q: %w", analysisID, err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("erro ao verificar atualização da análise %q: %w", analysisID, err)
	}
	if rows == 0 {
		return fmt.Errorf("análise %q não encontrada", analysisID)
	}
	return nil
}

// UpdateResumeChecklist atualiza o checklist de palavras-chave/pontos
// gerado sob demanda para uma análise.
func (db *DB) UpdateResumeChecklist(analysisID string, keywords string, points string) error {
	res, err := db.conn.Exec(`UPDATE analyses SET resume_keywords = ?, resume_points = ? WHERE id = ?`, keywords, points, analysisID)
	if err != nil {
		return fmt.Errorf("erro ao atualizar checklist de currículo da análise %q: %w", analysisID, err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("erro ao verificar atualização da análise %q: %w", analysisID, err)
	}
	if rows == 0 {
		return fmt.Errorf("análise %q não encontrada", analysisID)
	}
	return nil
}
