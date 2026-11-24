// Package jobfilter decide, antes do LLM, se uma vaga vale ser analisada,
// a partir do bloco job_filter do config.yaml: regex (RE2) aplicadas ao
// título e à descrição de vagas de todas as fontes.
package jobfilter

import (
	"fmt"
	"html"
	"regexp"
	"strings"
)

// Motivos de rejeição gravados em jobs.filter_reason.
const (
	ReasonTitleExclude = "title_exclude"
	ReasonNoMatch      = "sem Go"
)

// Config espelha o bloco job_filter do config.yaml.
type Config struct {
	Enabled         bool     `yaml:"enabled"`
	RequirePatterns []string `yaml:"require_patterns"`
	IgnorePatterns  []string `yaml:"ignore_patterns"`
	TitleExclude    []string `yaml:"title_exclude"`
}

// Filter é o job_filter com as regex já compiladas. O valor zero (ou nil)
// aprova tudo.
type Filter struct {
	enabled      bool
	require      []*regexp.Regexp
	ignore       []*regexp.Regexp
	titleExclude []*regexp.Regexp
}

// New compila todas as regex do config. Erro em qualquer uma delas diz o
// campo, a posição e o padrão inválido.
func New(c Config) (*Filter, error) {
	f := &Filter{enabled: c.Enabled}
	var err error
	if f.require, err = compile("require_patterns", c.RequirePatterns); err != nil {
		return nil, err
	}
	if f.ignore, err = compile("ignore_patterns", c.IgnorePatterns); err != nil {
		return nil, err
	}
	if f.titleExclude, err = compile("title_exclude", c.TitleExclude); err != nil {
		return nil, err
	}
	if f.enabled && len(f.require) == 0 {
		return nil, fmt.Errorf("job_filter: enabled sem nenhum require_patterns rejeitaria todas as vagas")
	}
	return f, nil
}

func compile(field string, patterns []string) ([]*regexp.Regexp, error) {
	out := make([]*regexp.Regexp, 0, len(patterns))
	for i, p := range patterns {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("job_filter.%s[%d] inválida (%q): %w", field, i, p, err)
		}
		out = append(out, re)
	}
	return out, nil
}

// Enabled informa se o filtro está ativo.
func (f *Filter) Enabled() bool {
	return f != nil && f.enabled
}

// Check diz se a vaga passa no filtro e, se não passar, o motivo
// (ReasonTitleExclude ou ReasonNoMatch):
//  1. título casa com algum title_exclude → rejeita;
//  2. texto = título + "\n" + descrição sem HTML, com os trechos que casam
//     com ignore_patterns apagados;
//  3. aprova se o texto casa com algum require_patterns.
func (f *Filter) Check(title, description string) (reason string, ok bool) {
	if !f.Enabled() {
		return "", true
	}
	for _, re := range f.titleExclude {
		if re.MatchString(title) {
			return ReasonTitleExclude, false
		}
	}
	text := title + "\n" + stripHTML(description)
	for _, re := range f.ignore {
		// Espaço no lugar do trecho: não cola as palavras vizinhas.
		text = re.ReplaceAllString(text, " ")
	}
	for _, re := range f.require {
		if re.MatchString(text) {
			return "", true
		}
	}
	return ReasonNoMatch, false
}

var (
	tagRe       = regexp.MustCompile(`(?s)<[^>]*>`)
	scriptStyle = regexp.MustCompile(`(?is)<(script|style)\b.*?</(script|style)>`)
	spaceRunRe  = regexp.MustCompile(`[ \t\r\f\v]+`)
)

// stripHTML remove tags e decodifica entidades. A maioria dos crawlers já
// grava a descrição em texto puro; isso cobre os que não gravam (e "&amp;"
// ou "<li>Go</li>" não escondem nem inventam um "Go").
func stripHTML(s string) string {
	if !strings.ContainsAny(s, "<&") {
		return s
	}
	s = scriptStyle.ReplaceAllString(s, " ")
	s = tagRe.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	return spaceRunRe.ReplaceAllString(s, " ")
}
