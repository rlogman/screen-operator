package harness

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestE5_4_S2_OwnCasesWithoutStateCheck(t *testing.T) {
	own := mustCase(t, `{"id":"P1","categoria":"consultar","instruccion":"¿Cuántas facturas vencidas tiene Ferretería Ruiz?","estado_esperado":"terminada","respuesta_esperada":"45","diff_esperado":[],"lista_blanca":[]}`)
	own.Path = "cases/p1.json"
	w := newWorld(t)
	w.checkStatus, w.checkBody = 400, `{"error":"solicitud inválida"}`
	w.resumen = "respuesta: 45"
	var out bytes.Buffer
	recs, err := w.runner(&out).Run(context.Background(), []Case{own})
	if err != nil {
		t.Fatal(err)
	}
	r := recs[0]
	if !r.SinCheckEstado || r.Check != nil || r.Puntaje == nil || !r.Puntaje.SinCheckEstado {
		t.Fatalf("registro = %+v", r)
	}
	// Solo reglas locales: respuesta correcta => correcta, sin motivo de check.
	if !r.Puntaje.Correcta {
		t.Fatalf("con reglas locales deberia ser correcta: %+v", r.Puntaje)
	}
	// Las reglas locales siguen aplicando: respuesta equivocada => incorrecta.
	w2 := newWorld(t)
	w2.checkStatus, w2.checkBody = 400, `{"error":"x"}`
	w2.resumen = "respuesta: 44"
	recs2, _ := w2.runner(nil).Run(context.Background(), []Case{own})
	if recs2[0].Puntaje.Correcta || !hasMotivo(*recs2[0].Puntaje, MotivoRespuesta) {
		t.Fatalf("respuesta equivocada sin check: %+v", recs2[0].Puntaje)
	}

	// El reporte marca el caso como "sin check de estado".
	rep := BuildReport(append(recs, recs2...), goalsForTest(t), nil)
	md := rep.Markdown()
	if rep.Total.SinCheckEstado != 2 || !strings.Contains(md, "Casos sin check de estado") || !strings.Contains(md, "- P1 (corrida 1)") {
		t.Fatalf("reporte:\n%s", md)
	}
	if !strings.Contains(out.String(), `"sin_check_estado":true`) {
		t.Fatalf("JSONL: %s", out.String())
	}

	// Un /__check caido (5xx) no es "sin check de estado": la tarea queda incorrecta.
	w3 := newWorld(t)
	w3.checkStatus, w3.checkBody = 502, "bad gateway"
	w3.resumen = "respuesta: 45"
	recs3, _ := w3.runner(nil).Run(context.Background(), []Case{own})
	if recs3[0].SinCheckEstado || recs3[0].Puntaje.Correcta || !hasMotivo(*recs3[0].Puntaje, MotivoCheckFallo) {
		t.Fatalf("check caido: %+v", recs3[0])
	}
}
