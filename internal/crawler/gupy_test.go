package crawler

import (
	"strings"
	"testing"
	"time"
)

func TestParseGupy(t *testing.T) {
	jobs, total, err := parseGupy(readFixture(t, "gupy.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 3 || total != 4 {
		t.Fatalf("esperava 3 vagas (total 4), veio %d (total %d)", len(jobs), total)
	}

	j := jobs[0]
	if j.Title != "PESSOA DESENVOLVEDORA JR Golang" || j.Source != "gupy" || j.Company == "" {
		t.Errorf("campos básicos inesperados: %+v", j)
	}
	if !strings.HasPrefix(j.URL, "https://qualitydigital.gupy.io/job/") {
		t.Errorf("URL inesperada: %q", j.URL)
	}
	if !strings.HasPrefix(j.Location, "Remoto") {
		t.Errorf("vaga remota deveria ter Location \"Remoto\", veio %q", j.Location)
	}
	if want := time.Date(2026, 11, 11, 20, 51, 11, 563e6, time.UTC); !j.FoundAt.Equal(want) {
		t.Errorf("FoundAt = %v, esperava %v", j.FoundAt, want)
	}
	if j.Description == "" {
		t.Error("descrição vazia")
	}
}
