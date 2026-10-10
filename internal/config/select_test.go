package config

import (
	"errors"
	"strings"
	"testing"
)

func TestE1_3_S1_WebsockifyURLSelectsVNCKiosk(t *testing.T) {
	l := newTestLoader(t, nil)
	s, err := l.ResolveSurface("wss://host.example/websockify")
	if err != nil {
		t.Fatal(err)
	}
	if s.ID != "vnc-kiosk" {
		t.Fatalf("superficie = %q, se esperaba vnc-kiosk", s.ID)
	}
	c, err := l.ForTask("wss://host.example/websockify", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := mustLookup(t, c, "surface.id"); got != "vnc-kiosk" {
		t.Fatalf("surface.id = %v", got)
	}
}

func TestE1_3_S2_HTTPSURLUnsupported(t *testing.T) {
	l := newTestLoader(t, nil)
	_, err := l.ResolveSurface("https://host.example/")
	if err == nil {
		t.Fatal("se esperaba error")
	}
	if !errors.Is(err, ErrUnsupportedSurface) || !strings.Contains(err.Error(), "superficie no soportada") {
		t.Fatalf("se esperaba 'superficie no soportada', fue %v", err)
	}
	if !strings.Contains(err.Error(), `"https"`) || !strings.Contains(err.Error(), `"/"`) {
		t.Fatalf("el error debe nombrar esquema https y ruta /: %v", err)
	}
	if _, err := l.ForTask("https://host.example/", "SIGECOM"); !errors.Is(err, ErrUnsupportedSurface) {
		t.Fatalf("ForTask deberia fallar igual, fue %v", err)
	}
}

func TestE1_3_S3_SigecomTextSelectsProfile(t *testing.T) {
	l := newTestLoader(t, nil)
	text := "SIGECOM · Distribuidora Andina v3.9"
	a, ok, err := l.ResolveApp(text)
	if err != nil || !ok || a.ID != "sigecom" {
		t.Fatalf("perfil = %q ok=%v err=%v, se esperaba sigecom", a.ID, ok, err)
	}
	c, err := l.ForTask("wss://host.example/websockify", text)
	if err != nil {
		t.Fatal(err)
	}
	if got := mustLookup(t, c, "app.id"); got != "sigecom" {
		t.Fatalf("app.id = %v", got)
	}
}

func TestE1_3_S4_UnknownAppGivesEmptyProfile(t *testing.T) {
	l := newTestLoader(t, nil)
	a, ok, err := l.ResolveApp("Sistema Farmacia XYZ")
	if err != nil || ok || a.ID != "" {
		t.Fatalf("se esperaba perfil vacio; fue %q ok=%v err=%v", a.ID, ok, err)
	}
	c, err := l.ForTask("wss://host.example/websockify", "Sistema Farmacia XYZ")
	if err != nil {
		t.Fatal(err)
	}
	if v, present := c.Lookup("app.id"); present {
		t.Fatalf("la configuracion no debe contener app.id; tiene %v", v)
	}
	if app, _ := c.Lookup("app"); len(app.(map[string]any)) != 0 {
		t.Fatalf("app deberia ser {} (hints vacios), es %v", app)
	}
}

func TestE1_3_S5_DisableAppProfilesEnv(t *testing.T) {
	l := newTestLoader(t, map[string]string{EnvDisableAppProfiles: "1"})
	a, ok, err := l.ResolveApp("SIGECOM")
	if err != nil || ok || a.ID != "" {
		t.Fatalf("con %s=1 se esperaba perfil vacio; fue %q ok=%v err=%v", EnvDisableAppProfiles, a.ID, ok, err)
	}
	c, err := l.ForTask("wss://host.example/websockify", "SIGECOM")
	if err != nil {
		t.Fatal(err)
	}
	if _, present := c.Lookup("app.id"); present {
		t.Fatal("con perfiles desactivados la configuracion no debe contener app.id")
	}
	// Control: con la variable en 0 el perfil vuelve.
	l0 := newTestLoader(t, map[string]string{EnvDisableAppProfiles: "0"})
	if a, ok, _ := l0.ResolveApp("SIGECOM"); !ok || a.ID != "sigecom" {
		t.Fatalf("con %s=0 se esperaba sigecom, fue %q", EnvDisableAppProfiles, a.ID)
	}
}
