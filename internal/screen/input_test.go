package screen

import (
	"context"
	"testing"
	"time"

	"screenoperator/internal/fakes/rfbserver"
)

func TestE2_4_S1_ClicConHold(t *testing.T) {
	surf, _ := kiosk(t)
	if surf.Input.ClickHoldMS != 150 {
		t.Fatalf("vnc-kiosk click_hold_ms=%d, el escenario asume 150", surf.Input.ClickHoldMS)
	}
	srv, d := fakeDesktop(t, rfbserver.Options{})
	in := NewInput(d, surf.Input)
	if err := in.Click(context.Background(), 500, 300, 0); err != nil {
		t.Fatal(err)
	}
	ev := waitEvents(t, srv, 3)
	if e := ev[0]; e.Kind != "pointer" || e.X != 500 || e.Y != 300 || e.Buttons != 0 {
		t.Fatalf("primer evento %+v, se esperaba movimiento a (500,300)", e)
	}
	if e := ev[1]; e.Buttons != ButtonLeft || e.X != 500 || e.Y != 300 {
		t.Fatalf("segundo evento %+v, se esperaba down del boton 1", e)
	}
	if e := ev[2]; e.Buttons != 0 {
		t.Fatalf("tercer evento %+v, se esperaba up", e)
	}
	if hold := ev[2].Time.Sub(ev[1].Time); hold < 150*time.Millisecond {
		t.Fatalf("hold de %v < 150ms", hold)
	}
}

func TestE2_4_S2_DobleClicConGap(t *testing.T) {
	surf, _ := kiosk(t)
	gap := ms(surf.Input.DoubleClickGapMS)
	srv, d := fakeDesktop(t, rfbserver.Options{})
	if err := NewInput(d, surf.Input).DoubleClick(context.Background(), 500, 300); err != nil {
		t.Fatal(err)
	}
	waitEvents(t, srv, 5)
	cl := srv.Clicks()
	if len(cl) != 2 {
		t.Fatalf("%d clics, se esperaban 2", len(cl))
	}
	for i, c := range cl {
		if c.Button != 1 || c.X != 500 || c.Y != 300 {
			t.Fatalf("clic %d = %+v", i, c)
		}
	}
	// Tolerancia de 10 ms por el reloj del servidor (timestamps de recepcion).
	if sep := cl[1].Down.Sub(cl[0].Up); sep < gap-10*time.Millisecond || sep > gap+150*time.Millisecond {
		t.Fatalf("separacion entre clics %v, se esperaba ~%v", sep, gap)
	}
}

func TestE2_4_S3_TextoConTildesYEnie(t *testing.T) {
	surf, _ := kiosk(t)
	surf.Input.TypeCharDelayMS, surf.Input.KeyHoldMS = 1, 1 // el orden no depende de los tiempos
	srv, d := fakeDesktop(t, rfbserver.Options{})
	text := "Calle 80 # 24-15 Peñalosa Ávila"
	if err := NewInput(d, surf.Input).Type(context.Background(), text); err != nil {
		t.Fatal(err)
	}
	// Keysyms esperados escritos a mano: ASCII = codigo, ñ = 0xf1, Á = 0xc1.
	want := []uint32{'C', 'a', 'l', 'l', 'e', ' ', '8', '0', ' ', '#', ' ', '2', '4', '-', '1', '5', ' ',
		'P', 'e', 0x00f1, 'a', 'l', 'o', 's', 'a', ' ', 0x00c1, 'v', 'i', 'l', 'a'}
	ev := keyEvents(waitEvents(t, srv, 2*len(want)))
	if len(ev) != 2*len(want) {
		t.Fatalf("%d eventos de teclado, se esperaban %d", len(ev), 2*len(want))
	}
	for i, k := range want {
		down, up := ev[2*i], ev[2*i+1]
		if down.Keysym != k || !down.Down || up.Keysym != k || up.Down {
			t.Fatalf("caracter %d: eventos %+v / %+v, se esperaba down/up de %#x", i, down, up, k)
		}
	}
}

func TestE2_4_S4_CtrlA(t *testing.T) {
	surf, _ := kiosk(t)
	srv, d := fakeDesktop(t, rfbserver.Options{})
	if err := NewInput(d, surf.Input).Combo(context.Background(), "ctrl+a"); err != nil {
		t.Fatal(err)
	}
	ev := keyEvents(waitEvents(t, srv, 4))
	want := []struct {
		k    uint32
		down bool
	}{{0xffe3, true}, {'a', true}, {'a', false}, {0xffe3, false}}
	for i, w := range want {
		if ev[i].Keysym != w.k || ev[i].Down != w.down {
			t.Fatalf("evento %d = %#x down=%v, se esperaba %#x down=%v", i, ev[i].Keysym, ev[i].Down, w.k, w.down)
		}
	}
}

func TestE2_4_S5_ScrollAbajo3Pasos(t *testing.T) {
	surf, _ := kiosk(t)
	srv, d := fakeDesktop(t, rfbserver.Options{})
	if err := NewInput(d, surf.Input).Wheel(context.Background(), 640, 400, "down", 3); err != nil {
		t.Fatal(err)
	}
	waitEvents(t, srv, 7)
	cl := srv.Clicks()
	if len(cl) != 3 {
		t.Fatalf("%d pares down/up, se esperaban 3", len(cl))
	}
	for i, c := range cl {
		if c.Button != 5 || c.X != 640 || c.Y != 400 {
			t.Fatalf("par %d = %+v, se esperaba boton 5 en (640,400)", i, c)
		}
	}
}

func TestE2_4_S6_TeclasEspeciales(t *testing.T) {
	surf, _ := kiosk(t)
	srv, d := fakeDesktop(t, rfbserver.Options{})
	in := NewInput(d, surf.Input)
	cases := []struct {
		name string
		ks   uint32
	}{
		{"Enter", 0xff0d}, {"Tab", 0xff09}, {"Escape", 0xff1b}, {"Backspace", 0xff08}, {"Delete", 0xffff},
		{"Left", 0xff51}, {"Up", 0xff52}, {"Right", 0xff53}, {"Down", 0xff54}, {"Home", 0xff50}, {"End", 0xff57},
	}
	for _, c := range cases {
		if err := in.Combo(context.Background(), c.name); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
	}
	ev := keyEvents(waitEvents(t, srv, 2*len(cases)))
	for i, c := range cases {
		down, up := ev[2*i], ev[2*i+1]
		if down.Keysym != c.ks || !down.Down || up.Keysym != c.ks || up.Down {
			t.Fatalf("%s: eventos %#x/%#x, se esperaba %#x", c.name, down.Keysym, up.Keysym, c.ks)
		}
	}
}
