// Package titlefilter descarta, pelo título, vagas que não são de
// engenharia antes de elas chegarem ao LLM.
package titlefilter

import (
	"fmt"
	"regexp"
	"strings"
)

// Filter aplica as listas title_exclude/title_require do config. Cada
// padrão casa como palavra ou frase inteira, sem diferenciar maiúsculas:
// "sales" casa com "Pre-Sales Engineer" mas não com "Salesforce Developer";
// "ios" não casa com "Studios". Nas frases, espaço também casa com hífen,
// barra e sublinhado ("head of" casa com "Head-of Platform").
type Filter struct {
	exclude []term
	require []term
}

type term struct {
	pattern string
	re      *regexp.Regexp
}

// New compila os padrões. Listas vazias desativam a respectiva regra.
func New(exclude, require []string) (*Filter, error) {
	ex, err := compile(exclude)
	if err != nil {
		return nil, fmt.Errorf("title_exclude: %w", err)
	}
	req, err := compile(require)
	if err != nil {
		return nil, fmt.Errorf("title_require: %w", err)
	}
	return &Filter{exclude: ex, require: req}, nil
}

func compile(patterns []string) ([]term, error) {
	var terms []term
	for _, p := range patterns {
		words := strings.Fields(strings.ToLower(p))
		if len(words) == 0 {
			continue
		}
		for i, w := range words {
			words[i] = regexp.QuoteMeta(w)
		}
		expr := `(?i)(?:^|[^\p{L}\p{N}])` + strings.Join(words, `[\s\-_/]+`) + `(?:$|[^\p{L}\p{N}])`
		re, err := regexp.Compile(expr)
		if err != nil {
			return nil, fmt.Errorf("padrão %q inválido: %w", p, err)
		}
		terms = append(terms, term{pattern: p, re: re})
	}
	return terms, nil
}

// Enabled informa se o filtro tem alguma regra.
func (f *Filter) Enabled() bool {
	return f != nil && (len(f.exclude) > 0 || len(f.require) > 0)
}

// Reject diz se a vaga deve ser descartada pelo título e por quê: casou
// com um padrão de exclusão ou não casou com nenhum termo obrigatório.
func (f *Filter) Reject(title string) (string, bool) {
	if f == nil {
		return "", false
	}
	for _, t := range f.exclude {
		if t.re.MatchString(title) {
			return fmt.Sprintf("título contém %q", t.pattern), true
		}
	}
	if len(f.require) == 0 {
		return "", false
	}
	for _, t := range f.require {
		if t.re.MatchString(title) {
			return "", false
		}
	}
	return "título sem termo de engenharia", true
}
