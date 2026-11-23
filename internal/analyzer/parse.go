package analyzer

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
)

// maxLoggedResponse é quanto da resposta inválida do modelo vai pra
// mensagem de erro (e daí pro log).
const maxLoggedResponse = 300

// extractJSONObject devolve o primeiro objeto JSON válido dentro de text,
// ignorando cercas ```json e qualquer texto antes ou depois dele.
func extractJSONObject(text string) (json.RawMessage, bool) {
	for i := strings.IndexByte(text, '{'); i >= 0; {
		var raw json.RawMessage
		// Decode lê um valor só e para: o que vier depois do objeto não
		// atrapalha.
		if err := json.NewDecoder(strings.NewReader(text[i:])).Decode(&raw); err == nil {
			return raw, true
		}
		next := strings.IndexByte(text[i+1:], '{')
		if next < 0 {
			break
		}
		i += next + 1
	}
	return nil, false
}

// truncate corta s em n runas, indicando quantos bytes ficaram de fora.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	cut := string(r[:n])
	return fmt.Sprintf("%s… (+%d bytes)", cut, len(s)-len(cut))
}

// rawAnalysisResult aceita fit_score como número qualquer (alguns modelos
// mandam 85.0) e distingue campo ausente de zero.
type rawAnalysisResult struct {
	FitScore     *float64 `json:"fit_score"`
	Summary      string   `json:"summary"`
	Benefits     []string `json:"benefits"`
	FitReasoning string   `json:"fit_reasoning"`
}

// parseResult extrai e valida a análise devolvida pelo modelo. Resposta
// sem JSON, com JSON incompleto ou com fit_score fora de 0-100 é erro (e
// conta como tentativa de análise que falhou).
func parseResult(text string) (analysisResult, error) {
	raw, ok := extractJSONObject(text)
	if !ok {
		return analysisResult{}, fmt.Errorf("resposta do modelo sem objeto JSON (texto: %q)", truncate(strings.TrimSpace(text), maxLoggedResponse))
	}

	var r rawAnalysisResult
	if err := json.Unmarshal(raw, &r); err != nil {
		return analysisResult{}, fmt.Errorf("erro ao parsear JSON da análise: %w (texto: %q)", err, truncate(string(raw), maxLoggedResponse))
	}
	if err := r.validate(); err != nil {
		return analysisResult{}, fmt.Errorf("análise inválida: %w (texto: %q)", err, truncate(string(raw), maxLoggedResponse))
	}

	return analysisResult{
		FitScore:     int(math.Round(*r.FitScore)),
		Summary:      strings.TrimSpace(r.Summary),
		Benefits:     r.Benefits,
		FitReasoning: strings.TrimSpace(r.FitReasoning),
	}, nil
}

func (r rawAnalysisResult) validate() error {
	var errs []error
	switch {
	case r.FitScore == nil:
		errs = append(errs, errors.New("fit_score ausente"))
	case *r.FitScore < 0 || *r.FitScore > 100 || math.IsNaN(*r.FitScore):
		errs = append(errs, fmt.Errorf("fit_score %v fora de 0-100", *r.FitScore))
	}
	if strings.TrimSpace(r.Summary) == "" {
		errs = append(errs, errors.New("summary vazio"))
	}
	if strings.TrimSpace(r.FitReasoning) == "" {
		errs = append(errs, errors.New("fit_reasoning vazio"))
	}
	return errors.Join(errs...)
}
