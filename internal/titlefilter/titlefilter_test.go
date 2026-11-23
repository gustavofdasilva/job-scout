package titlefilter

import "testing"

func TestReject(t *testing.T) {
	f, err := New(
		[]string{"sales", "account executive", "account manager", "recruiter", "product manager", "director", "head of", "ios", "mobile"},
		[]string{"engineer", "developer", "desenvolvedor", "sre", "devops", "backend", "platform"},
	)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		title  string
		reject bool
	}{
		{"Senior Backend Engineer", false},
		{"Desenvolvedor Golang Pleno", false},
		{"Pre-Sales Engineer", true},
		{"Sales Director", true},
		{"Technical Account Manager", true},
		{"Account Executive, Enterprise", true},
		{"Technical Recruiter", true},
		{"Senior Product Manager", true},
		{"Head of Platform", true},
		{"Head-of Platform", true},
		{"iOS Engineer", true},
		{"Salesforce Developer", false},           // "sales" não casa dentro de "salesforce"
		{"Game Studios Platform Engineer", false}, // "ios" não casa dentro de "studios"
		{"Customer Success Specialist", true},     // sem termo obrigatório
		{"SRE / DevOps", false},
	}
	for _, c := range cases {
		reason, got := f.Reject(c.title)
		if got != c.reject {
			t.Errorf("%q: reject=%v (%s), esperava %v", c.title, got, reason, c.reject)
		}
	}
}

func TestEmptyFilterAcceptsEverything(t *testing.T) {
	f, err := New(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if f.Enabled() {
		t.Fatal("filtro vazio não deveria estar ativo")
	}
	if _, rej := f.Reject("Sales Director"); rej {
		t.Fatal("filtro vazio rejeitou vaga")
	}
}
