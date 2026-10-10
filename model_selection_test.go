package screenoperator_test

// Tests de la historia 8.2: tabla de comparacion de modelos, regla de eleccion
// y su aplicacion en config/base.yaml.

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"screenoperator/internal/config"
	"screenoperator/internal/llm"
)

// selRow es una fila de la tabla de docs/model-selection.md.
type selRow struct {
	ID, Actor, Escalation string
	Hits, Total, False    int
	P95, Cost, Steps      float64
}

var selHeader = []string{"Combinación", "Actor", "Escalamiento", "Acierto", "Falsos éxitos", "p95 (s)", "Costo medio (US$)", "Pasos medios"}

// parseSelection lee la tabla, el techo de costo y la combinacion elegida.
func parseSelection(t *testing.T) (rows []selRow, ceiling float64, chosen string) {
	t.Helper()
	doc := readRepoFile(t, "docs/model-selection.md")
	inTable := false
	hitRe := regexp.MustCompile(`^(\d+)/(\d+)`)
	for _, line := range strings.Split(doc, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "|") {
			inTable = false
			continue
		}
		cells := strings.Split(strings.Trim(line, "|"), "|")
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
		}
		if len(cells) >= len(selHeader) && cells[0] == selHeader[0] {
			for i, h := range selHeader {
				if cells[i] != h {
					t.Fatalf("columna %d = %q, se esperaba %q", i, cells[i], h)
				}
			}
			inTable = true
			continue
		}
		if !inTable || strings.HasPrefix(cells[0], "---") {
			continue
		}
		m := hitRe.FindStringSubmatch(cells[3])
		if m == nil {
			t.Fatalf("acierto ilegible en %q", line)
		}
		r := selRow{ID: strings.Trim(cells[0], "*"), Actor: strings.Trim(cells[1], "`"), Escalation: strings.Trim(cells[2], "`")}
		r.Hits, _ = strconv.Atoi(m[1])
		r.Total, _ = strconv.Atoi(m[2])
		var err error
		if r.False, err = strconv.Atoi(cells[4]); err != nil {
			t.Fatalf("falsos exitos ilegibles en %q", line)
		}
		for i, dst := range []*float64{&r.P95, &r.Cost, &r.Steps} {
			if *dst, err = strconv.ParseFloat(strings.ReplaceAll(cells[5+i], ",", "."), 64); err != nil {
				t.Fatalf("numero ilegible %q en %q", cells[5+i], line)
			}
		}
		rows = append(rows, r)
	}
	if m := regexp.MustCompile(`Techo de costo medio: US\$ ([0-9.,]+)`).FindStringSubmatch(doc); m != nil {
		ceiling, _ = strconv.ParseFloat(strings.ReplaceAll(m[1], ",", "."), 64)
	}
	if m := regexp.MustCompile(`Elegida: \*\*([A-Za-z0-9]+)\*\*`).FindStringSubmatch(doc); m != nil {
		chosen = m[1]
	}
	return rows, ceiling, chosen
}

// TestE8_2_S1_ComparisonTable: la tabla tiene acierto, falsos exitos, p95,
// costo medio y pasos medios por combinacion, con la linea de base A.
func TestE8_2_S1_ComparisonTable(t *testing.T) {
	rows, ceiling, _ := parseSelection(t)
	if len(rows) < 3 {
		t.Fatalf("la tabla tiene %d combinaciones; se esperaban al menos A, B y C", len(rows))
	}
	ids := map[string]bool{}
	for _, r := range rows {
		ids[r.ID] = true
		if r.Total <= 0 || r.Hits > r.Total {
			t.Fatalf("acierto invalido en %+v", r)
		}
	}
	for _, id := range []string{"A", "B", "C"} {
		if !ids[id] {
			t.Fatalf("falta la combinacion %s", id)
		}
	}
	if ceiling <= 0 {
		t.Fatal("falta 'Techo de costo medio: US$ ...'")
	}
}

// chooseCombination aplica la regla de E8.2-S2: entre las que tienen 0 falsos
// exitos y costo medio <= techo, la de mayor acierto y, a igual acierto, la de
// menor costo; si ninguna mejora el acierto de A, se queda A.
func chooseCombination(rows []selRow, ceiling float64) string {
	var base *selRow
	for i := range rows {
		if rows[i].ID == "A" {
			base = &rows[i]
		}
	}
	var best *selRow
	for i := range rows {
		r := &rows[i]
		if r.False != 0 || r.Cost > ceiling {
			continue
		}
		if best == nil || r.Hits*best.Total > best.Hits*r.Total ||
			(r.Hits*best.Total == best.Hits*r.Total && r.Cost < best.Cost) {
			best = r
		}
	}
	if best == nil || base == nil || best.Hits*base.Total <= base.Hits*best.Total {
		return "A"
	}
	return best.ID
}

func TestE8_2_S2_ChoiceFollowsRule(t *testing.T) {
	rows, ceiling, chosen := parseSelection(t)
	if chosen == "" {
		t.Fatal("falta 'Elegida: **X**' en docs/model-selection.md")
	}
	if want := chooseCombination(rows, ceiling); chosen != want {
		t.Fatalf("elegida %s, la regla da %s", chosen, want)
	}
	doc := readRepoFile(t, "docs/model-selection.md")
	if !strings.Contains(doc, "## Razón") {
		t.Fatal("falta la seccion '## Razón'")
	}
	// La regla misma, sobre una tabla sintetica.
	syn := []selRow{
		{ID: "A", Hits: 2, Total: 5, Cost: 0.03},
		{ID: "B", Hits: 4, Total: 5, Cost: 0.05},
		{ID: "C", Hits: 4, Total: 5, Cost: 0.04},
		{ID: "D", Hits: 5, Total: 5, False: 1, Cost: 0.02},
		{ID: "E", Hits: 5, Total: 5, Cost: 0.09},
	}
	if got := chooseCombination(syn, 0.07); got != "C" {
		t.Fatalf("regla sintetica = %s, se esperaba C", got)
	}
	if got := chooseCombination(syn[:1], 0.07); got != "A" {
		t.Fatalf("solo A = %s", got)
	}
}

// TestE8_2_S3_BaseYamlReflectsChoice: base.yaml tiene el actor y el
// escalamiento de la combinacion elegida y la configuracion valida.
func TestE8_2_S3_BaseYamlReflectsChoice(t *testing.T) {
	rows, _, chosen := parseSelection(t)
	var row *selRow
	for i := range rows {
		if rows[i].ID == chosen {
			row = &rows[i]
		}
	}
	if row == nil {
		t.Fatalf("la elegida %q no esta en la tabla", chosen)
	}
	l, err := config.NewLoader(filepath.Join(repoRoot(t), "config"), func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := l.ForTask("wss://x/websockify", "")
	if err != nil {
		t.Fatalf("la configuracion no valida: %v", err)
	}
	ms, err := llm.ModelsFromConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got := ms.Role("actor").Model; got != row.Actor {
		t.Fatalf("actor en base.yaml = %s, elegida %s usa %s", got, chosen, row.Actor)
	}
	esc, ok := ms.EscalationRole()
	switch {
	case row.Escalation == "-" || row.Escalation == "":
		if ok {
			t.Fatalf("la elegida no escala pero base.yaml tiene escalation = %s", esc.Model)
		}
	case !ok || esc.Model != row.Escalation:
		t.Fatalf("escalation en base.yaml = %q, elegida usa %s", esc.Model, row.Escalation)
	}
}
