package crawler

import "testing"

func TestParseArbeitnow(t *testing.T) {
	jobs, hasNext, err := parseArbeitnow(readFixture(t, "arbeitnow.json"), []string{"backend", "software engineer"})
	if err != nil {
		t.Fatal(err)
	}
	if !hasNext {
		t.Error("fixture tem links.next, esperava hasNext=true")
	}
	// Fixture: 2 remotas de engenharia, 1 remota de CS e 2 presenciais
	// (uma delas de engenharia, que deve ser descartada por não ser remota).
	if len(jobs) != 2 {
		t.Fatalf("esperava 2 vagas remotas filtradas, veio %d", len(jobs))
	}

	j := jobs[0]
	if j.Source != "arbeitnow" || j.Company != "ElevenLabs" || j.Location != "Remote" {
		t.Errorf("campos básicos inesperados: %+v", j)
	}

	all, _, _ := parseArbeitnow(readFixture(t, "arbeitnow.json"), nil)
	if len(all) != 3 {
		t.Fatalf("sem termos deveria trazer as 3 remotas, veio %d", len(all))
	}
	if all[2].Location != "Berlin · Remote" {
		t.Errorf("Location = %q", all[2].Location)
	}
}
