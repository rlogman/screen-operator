package agent

import "testing"

// Vocabulario de AsksQuantity y rangos con "al" (observaciones del validador de so-epic-6 iter 3).
func TestAsksQuantityVocabulary(t *testing.T) {
	yes := []string{
		"Cuenta las facturas vencidas de Ferretería Ruiz",
		"¿Qué número de facturas vencidas tiene Ferretería Ruiz?",
		"How many overdue invoices does Ferretería Ruiz have?",
		"¿Cuánto debe Ferretería Ruiz en facturas vencidas?",
		"¿Cuántas facturas vencidas tiene Ferretería Ruiz?",
	}
	for _, q := range yes {
		if !AsksQuantity(q, nil) {
			t.Errorf("AsksQuantity(%q) = false, se esperaba true", q)
		}
	}
	if AsksQuantity("Cambia el teléfono de Ana Torres a 3014445566", nil) {
		t.Errorf("una orden de edición no pide cantidad")
	}
}

func TestParseIndicatorRangeWithAl(t *testing.T) {
	if _, _, ok := ParseIndicator("Mostrando 1 al 5 de 5"); ok {
		t.Errorf("'Mostrando 1 al 5 de 5' es un rango de registros, no un número de página")
	}
}
