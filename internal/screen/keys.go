package screen

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Keysyms X11 de teclas especiales y modificadores.
const (
	KeyBackSpace uint32 = 0xff08
	KeyTab       uint32 = 0xff09
	KeyReturn    uint32 = 0xff0d
	KeyEscape    uint32 = 0xff1b
	KeyHome      uint32 = 0xff50
	KeyLeft      uint32 = 0xff51
	KeyUp        uint32 = 0xff52
	KeyRight     uint32 = 0xff53
	KeyDown      uint32 = 0xff54
	KeyPageUp    uint32 = 0xff55
	KeyPageDown  uint32 = 0xff56
	KeyEnd       uint32 = 0xff57
	KeyInsert    uint32 = 0xff63
	KeyF1        uint32 = 0xffbe
	KeyShiftL    uint32 = 0xffe1
	KeyControlL  uint32 = 0xffe3
	KeyAltL      uint32 = 0xffe9
	KeySuperL    uint32 = 0xffeb
	KeyDelete    uint32 = 0xffff
)

var namedKeys = map[string]uint32{
	"enter": KeyReturn, "return": KeyReturn, "intro": KeyReturn,
	"tab": KeyTab, "tabulador": KeyTab,
	"escape": KeyEscape, "esc": KeyEscape,
	"backspace": KeyBackSpace, "retroceso": KeyBackSpace,
	"delete": KeyDelete, "del": KeyDelete, "supr": KeyDelete, "suprimir": KeyDelete,
	"left": KeyLeft, "up": KeyUp, "right": KeyRight, "down": KeyDown,
	"izquierda": KeyLeft, "arriba": KeyUp, "derecha": KeyRight, "abajo": KeyDown,
	"home": KeyHome, "inicio": KeyHome, "end": KeyEnd, "fin": KeyEnd,
	"pageup": KeyPageUp, "pgup": KeyPageUp, "pagedown": KeyPageDown, "pgdn": KeyPageDown,
	"insert": KeyInsert, "space": ' ', "espacio": ' ',
	"ctrl": KeyControlL, "control": KeyControlL,
	"shift": KeyShiftL, "mayus": KeyShiftL,
	"alt":   KeyAltL,
	"super": KeySuperL, "meta": KeySuperL, "win": KeySuperL, "cmd": KeySuperL,
}

func init() {
	for i := 0; i < 12; i++ {
		namedKeys[fmt.Sprintf("f%d", i+1)] = KeyF1 + uint32(i)
	}
}

// KeysymForRune devuelve el keysym X11 de un caracter: ASCII y Latin-1
// (incluye áéíóúñÁÉÍÓÚÑü) tienen keysym igual a su codigo; el resto usa la
// convencion Unicode de X11 (0x01000000 | codigo). Salto de linea y tabulador
// se envian como Return y Tab.
func KeysymForRune(r rune) (uint32, error) {
	switch {
	case r == '\n' || r == '\r':
		return KeyReturn, nil
	case r == '\t':
		return KeyTab, nil
	case r >= 0x20 && r <= 0x7e, r >= 0xa0 && r <= 0xff:
		return uint32(r), nil
	case r > 0xff && r <= 0x10ffff && r != utf8.RuneError:
		return 0x01000000 | uint32(r), nil
	}
	return 0, fmt.Errorf("screen: caracter %U sin keysym", r)
}

// ParseCombo interpreta una tecla o combinacion como "enter", "ctrl+a",
// "shift+tab" o "ctrl+alt+supr". Devuelve los keysyms en orden de pulsacion.
func ParseCombo(combo string) ([]uint32, error) {
	combo = strings.TrimSpace(combo)
	if combo == "" {
		return nil, fmt.Errorf("screen: combinacion de teclas vacia")
	}
	var parts []string
	if combo == "+" {
		parts = []string{"+"}
	} else {
		parts = strings.Split(combo, "+")
	}
	out := make([]uint32, 0, len(parts))
	for _, p := range parts {
		k, err := keyByName(strings.TrimSpace(p))
		if err != nil {
			return nil, fmt.Errorf("screen: combinacion %q: %w", combo, err)
		}
		out = append(out, k)
	}
	return out, nil
}

func keyByName(name string) (uint32, error) {
	if name == "" {
		return 0, fmt.Errorf("tecla vacia")
	}
	if k, ok := namedKeys[strings.ToLower(name)]; ok {
		return k, nil
	}
	if utf8.RuneCountInString(name) == 1 {
		r, _ := utf8.DecodeRuneInString(name)
		return KeysymForRune(r)
	}
	return 0, fmt.Errorf("tecla %q desconocida", name)
}
