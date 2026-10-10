package agent

// Fecha de la app y fechas relativas (6.4). El modelo lee "hoy" de la pantalla;
// la aritmetica de fechas es codigo. Semanas de lunes a domingo.

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var foldMap = map[rune]rune{'á': 'a', 'é': 'e', 'í': 'i', 'ó': 'o', 'ú': 'u', 'ü': 'u', 'ñ': 'n', 'à': 'a', 'è': 'e', 'ì': 'i', 'ò': 'o', 'ù': 'u'}

// DateRange es un rango de fechas inclusivo (dias, sin hora).
type DateRange struct {
	From time.Time
	To   time.Time
}

// String da "AAAA-MM-DD" o "AAAA-MM-DD a AAAA-MM-DD".
func (r DateRange) String() string {
	if r.From.Equal(r.To) {
		return r.From.Format("2006-01-02")
	}
	return r.From.Format("2006-01-02") + " a " + r.To.Format("2006-01-02")
}

func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

func dayOf(t time.Time) time.Time { return day(t.Year(), t.Month(), t.Day()) }

var (
	reDMY   = regexp.MustCompile(`\b(\d{1,2})[/.-](\d{1,2})[/.-](\d{4})\b`)
	reYMD   = regexp.MustCompile(`\b(\d{4})-(\d{1,2})-(\d{1,2})\b`)
	reWords = regexp.MustCompile(`\b(\d{1,2})\s+de\s+([a-z]+)\s+(?:de|del)\s+(\d{4})\b`)
)

var months = map[string]time.Month{
	"enero": 1, "febrero": 2, "marzo": 3, "abril": 4, "mayo": 5, "junio": 6, "julio": 7, "agosto": 8,
	"septiembre": 9, "setiembre": 9, "octubre": 10, "noviembre": 11, "diciembre": 12,
}

var weekdays = map[string]time.Weekday{
	"lunes": time.Monday, "martes": time.Tuesday, "miercoles": time.Wednesday, "jueves": time.Thursday,
	"viernes": time.Friday, "sabado": time.Saturday, "domingo": time.Sunday,
}

// WeekdayES es el nombre del dia en español.
func WeekdayES(t time.Time) string {
	return [...]string{"domingo", "lunes", "martes", "miércoles", "jueves", "viernes", "sábado"}[t.Weekday()]
}

// fold pasa a minusculas y quita tildes (solo para interpretar expresiones).
func fold(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		if r >= 0x300 && r <= 0x36f { // tildes combinantes
			continue
		}
		if f, ok := foldMap[r]; ok {
			r = f
		}
		b.WriteRune(r)
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// ParseAppDate busca una fecha en el texto que la app muestra como "hoy".
// format es el formato que vio el modelo (DD/MM/AAAA por defecto; MM/DD/AAAA
// invierte dia y mes). Acepta tambien AAAA-MM-DD y "14 de octubre de 2026".
func ParseAppDate(text, format string) (time.Time, error) {
	t := fold(text)
	if m := reYMD.FindStringSubmatch(t); m != nil {
		return mkDate(m[1], m[2], m[3])
	}
	if m := reDMY.FindStringSubmatch(t); m != nil {
		d, mo := m[1], m[2]
		if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(format)), "MM") {
			d, mo = mo, d
		}
		return mkDate(m[3], mo, d)
	}
	if m := reWords.FindStringSubmatch(t); m != nil {
		mo, ok := months[m[2]]
		if !ok {
			return time.Time{}, fmt.Errorf("mes %q desconocido", m[2])
		}
		return mkDate(m[3], strconv.Itoa(int(mo)), m[1])
	}
	return time.Time{}, fmt.Errorf("no hay una fecha reconocible en %q", text)
}

func mkDate(ys, ms, ds string) (time.Time, error) {
	y, _ := strconv.Atoi(ys)
	m, _ := strconv.Atoi(ms)
	d, _ := strconv.Atoi(ds)
	if m < 1 || m > 12 || d < 1 || d > 31 {
		return time.Time{}, fmt.Errorf("fecha invalida %s-%s-%s", ys, ms, ds)
	}
	t := day(y, time.Month(m), d)
	if t.Day() != d {
		return time.Time{}, fmt.Errorf("fecha invalida %s-%s-%s", ys, ms, ds)
	}
	return t, nil
}

var reAgo = regexp.MustCompile(`^hace (\d+) dias?$`)
var reMonthYear = regexp.MustCompile(`^(?:el mes de |en )?([a-z]+)(?: (?:de|del) (\d{4})| (\d{4}))?$`)

// ResolveRelative convierte una expresion de fecha de la orden en un rango,
// contando desde today (la fecha de la app). Reglas:
//   - "hoy", "ayer", "anteayer"/"antier", "mañana", "hace N dias": un dia.
//   - "el sabado" (cualquier dia de la semana, con o sin "pasado"): la ocurrencia
//     mas reciente ANTES de hoy (si hoy es sabado, el sabado anterior).
//   - "esta semana": lunes de esta semana a hoy; "la semana pasada": lunes a
//     domingo de la semana anterior.
//   - "este mes": dia 1 a hoy; "el mes pasado": el mes anterior completo.
//   - "septiembre de 2026", "septiembre 2026" o "septiembre" (año de hoy): el mes completo.
//   - "este año" (1 de enero a hoy), "el año pasado" (completo).
//   - Una fecha explicita (14/10/2026, 2026-10-14) es ese dia.
func ResolveRelative(today time.Time, expr string) (DateRange, bool) {
	today = dayOf(today)
	e := fold(expr)
	e = strings.Trim(e, " .,;:")
	one := func(t time.Time) (DateRange, bool) { return DateRange{t, t}, true }
	switch e {
	case "hoy":
		return one(today)
	case "ayer":
		return one(today.AddDate(0, 0, -1))
	case "anteayer", "antier", "antes de ayer":
		return one(today.AddDate(0, 0, -2))
	case "manana":
		return one(today.AddDate(0, 0, 1))
	case "esta semana", "en esta semana":
		return DateRange{monday(today), today}, true
	case "la semana pasada", "semana pasada", "en la semana pasada":
		m := monday(today).AddDate(0, 0, -7)
		return DateRange{m, m.AddDate(0, 0, 6)}, true
	case "este mes", "en este mes":
		return DateRange{day(today.Year(), today.Month(), 1), today}, true
	case "el mes pasado", "mes pasado", "en el mes pasado":
		first := day(today.Year(), today.Month(), 1).AddDate(0, -1, 0)
		return DateRange{first, first.AddDate(0, 1, -1)}, true
	case "este ano", "en este ano":
		return DateRange{day(today.Year(), 1, 1), today}, true
	case "el ano pasado", "ano pasado":
		y := today.Year() - 1
		return DateRange{day(y, 1, 1), day(y, 12, 31)}, true
	}
	if m := reAgo.FindStringSubmatch(e); m != nil {
		n, _ := strconv.Atoi(m[1])
		return one(today.AddDate(0, 0, -n))
	}
	// Dia de la semana: "el sabado", "el sabado pasado", "el pasado sabado", "sabado".
	w := strings.TrimPrefix(e, "el ")
	w = strings.TrimPrefix(w, "pasado ")
	w = strings.TrimSuffix(w, " pasado")
	if wd, ok := weekdays[w]; ok {
		diff := (int(today.Weekday()) - int(wd) + 7) % 7
		if diff == 0 {
			diff = 7
		}
		return one(today.AddDate(0, 0, -diff))
	}
	if m := reMonthYear.FindStringSubmatch(e); m != nil {
		if mo, ok := months[m[1]]; ok {
			y := today.Year()
			if s := m[2] + m[3]; s != "" {
				y, _ = strconv.Atoi(s)
			}
			first := day(y, mo, 1)
			return DateRange{first, first.AddDate(0, 1, -1)}, true
		}
	}
	if t, err := ParseAppDate(expr, ""); err == nil {
		return one(t)
	}
	return DateRange{}, false
}

func monday(t time.Time) time.Time {
	off := (int(t.Weekday()) + 6) % 7 // lunes = 0
	return t.AddDate(0, 0, -off)
}
