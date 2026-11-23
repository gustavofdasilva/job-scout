package analyzer

import (
	"strings"
	"testing"
)

const validJSON = `{"fit_score": 82, "summary": "Vaga backend Go.", "benefits": ["remoto"], "fit_reasoning": "Bate com o perfil."}`

func TestParseResultExtractsJSON(t *testing.T) {
	cases := map[string]string{
		"puro":           validJSON,
		"cerca":          "```json\n" + validJSON + "\n```",
		"texto em volta": "Claro! Segue a análise:\n" + validJSON + "\nEspero ter ajudado {:",
		"chave antes":    "Formato {fit_score} abaixo:\n" + validJSON,
		"score decimal":  strings.Replace(validJSON, "82", "81.6", 1),
	}
	for name, text := range cases {
		got, err := parseResult(text)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if got.FitScore != 82 || got.Summary != "Vaga backend Go." || len(got.Benefits) != 1 {
			t.Errorf("%s: resultado %+v", name, got)
		}
	}
}

func TestParseResultRejectsInvalid(t *testing.T) {
	cases := map[string]string{
		"sem json":       "asdkjh qwe lorem ipsum",
		"score fora":     strings.Replace(validJSON, "82", "140", 1),
		"score negativo": strings.Replace(validJSON, "82", "-1", 1),
		"sem score":      `{"summary": "x", "fit_reasoning": "y"}`,
		"sem summary":    `{"fit_score": 50, "fit_reasoning": "y"}`,
		"score string":   strings.Replace(validJSON, "82", `"82"`, 1),
	}
	for name, text := range cases {
		if _, err := parseResult(text); err == nil {
			t.Errorf("%s: esperava erro", name)
		}
	}
}

func TestParseResultTruncatesGarbage(t *testing.T) {
	_, err := parseResult(strings.Repeat("lixo ", 1000))
	if err == nil || len(err.Error()) > 500 {
		t.Fatalf("erro deveria vir truncado: %d bytes", len(err.Error()))
	}
}
