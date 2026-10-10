package rfb

import (
	"context"
	"encoding/hex"
	"errors"
	"image"
	"image/color"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"screenoperator/internal/fakes/rfbserver"
)

func dialFake(t *testing.T, s *rfbserver.Server, password string) *Session {
	t.Helper()
	sess, err := Dial(context.Background(), s.URL(), Options{Password: password, Shared: true, ConnectTimeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { sess.Close() })
	return sess
}

func TestVNCAuthResponseVector(t *testing.T) {
	// Vector independiente: openssl enc -des-ecb -K cea6c64ea62e868c sobre 00..0f.
	ch := make([]byte, 16)
	for i := range ch {
		ch[i] = byte(i)
	}
	got, err := VNCAuthResponse(ch, "secreta1")
	if err != nil {
		t.Fatal(err)
	}
	if h := hex.EncodeToString(got); h != "37e123c8cdc141be6a960d6e01674bad" {
		t.Fatalf("respuesta %s", h)
	}
}

func TestE2_2_S1_ConnectOK(t *testing.T) {
	s := rfbserver.Start(rfbserver.Options{Password: "secreta1", Width: 1280, Height: 800})
	defer s.Close()
	sess := dialFake(t, s, "secreta1")
	if w, h := sess.Size(); w != 1280 || h != 800 {
		t.Fatalf("tamano %dx%d", w, h)
	}
	if sess.Name() == "" {
		t.Fatal("nombre de escritorio vacio")
	}
}

func TestE2_2_S2_AuthFallidaSinContrasenaEnError(t *testing.T) {
	s := rfbserver.Start(rfbserver.Options{Password: "secreta1"})
	defer s.Close()
	_, err := Dial(context.Background(), s.URL(), Options{Password: "otra", ConnectTimeout: 5 * time.Second})
	if !errors.Is(err, ErrAuth) {
		t.Fatalf("error %v, se esperaba ErrAuth", err)
	}
	for _, secret := range []string{"otra", "secreta1"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("el error contiene %q: %v", secret, err)
		}
	}
}

func TestE2_2_S3_TimeoutServidorMudo(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer c.Close() // acepta y nunca responde
		}
	}()
	start := time.Now()
	_, err = Dial(context.Background(), "ws://"+ln.Addr().String()+"/websockify",
		Options{Password: "secreta1", ConnectTimeout: 5 * time.Second})
	elapsed := time.Since(start)
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("error %v, se esperaba ErrTimeout", err)
	}
	if elapsed > 6*time.Second {
		t.Fatalf("Connect tardo %v (> 6s)", elapsed)
	}
	if strings.Contains(err.Error(), "secreta1") {
		t.Fatalf("el error contiene la contrasena: %v", err)
	}
}

func TestDialTimeoutDuranteHandshakeRFB(t *testing.T) {
	// websocket abierto pero el servidor nunca envia la version RFB.
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{"binary"}})
		if err != nil {
			return
		}
		defer c.CloseNow()
		<-release
	}))
	defer srv.Close()
	defer close(release)
	start := time.Now()
	_, err := Dial(context.Background(), "ws"+strings.TrimPrefix(srv.URL, "http")+"/websockify",
		Options{Password: "secreta1", ConnectTimeout: time.Second})
	if !errors.Is(err, ErrTimeout) || time.Since(start) > 2*time.Second {
		t.Fatalf("err=%v en %v, se esperaba ErrTimeout en ~1s", err, time.Since(start))
	}
}

func TestE2_2_S4_CloseCierraWebsocketYEnviosFallan(t *testing.T) {
	s := rfbserver.Start(rfbserver.Options{Password: "secreta1"})
	defer s.Close()
	sess, err := Dial(context.Background(), s.URL(), Options{Password: "secreta1", ConnectTimeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.Close(); err != nil {
		t.Fatal(err)
	}
	if !s.WaitClosed(3 * time.Second) {
		t.Fatal("el servidor no vio cerrarse el websocket")
	}
	if err := sess.Pointer(10, 10, 0); !errors.Is(err, ErrClosed) {
		t.Fatalf("Pointer tras Close: %v", err)
	}
	if err := sess.Key('a', true); !errors.Is(err, ErrClosed) {
		t.Fatalf("Key tras Close: %v", err)
	}
	if _, err := sess.Capture(context.Background()); err == nil {
		t.Fatal("Capture tras Close no fallo")
	}
}

func TestE2_3_S1_CapturaConRectanguloRojo(t *testing.T) {
	bg := color.RGBA{240, 240, 240, 255}
	red := color.RGBA{255, 0, 0, 255}
	s := rfbserver.Start(rfbserver.Options{Password: "secreta1", Background: bg})
	defer s.Close()
	s.FillRect(image.Rect(100, 100, 150, 150), red)
	sess := dialFake(t, s, "secreta1")
	img, err := sess.Capture(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := img.RGBAAt(120, 120); got != red {
		t.Fatalf("(120,120) = %v, se esperaba rojo", got)
	}
	if got := img.RGBAAt(10, 10); got != bg {
		t.Fatalf("(10,10) = %v, se esperaba el fondo %v", got, bg)
	}
}

func TestE2_3_S2_CopyRect(t *testing.T) {
	s := rfbserver.Start(rfbserver.Options{Password: "secreta1"})
	defer s.Close()
	// Patron no uniforme en (0,0,100,100) para que la copia sea verificable.
	for y := 0; y < 100; y += 10 {
		for x := 0; x < 100; x += 10 {
			s.FillRect(image.Rect(x, y, x+10, y+10), color.RGBA{uint8(x * 2), uint8(y * 2), uint8(x + y), 255})
		}
	}
	sess := dialFake(t, s, "secreta1")
	if _, err := sess.Capture(context.Background()); err != nil {
		t.Fatal(err)
	}
	s.CopyRect(image.Rect(0, 0, 100, 100), image.Pt(200, 200))
	img, err := sess.Capture(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st := sess.Stats(); st.CopyRects == 0 {
		t.Fatalf("el servidor no envio CopyRect: %+v", st)
	}
	for y := 0; y < 100; y++ {
		for x := 0; x < 100; x++ {
			if a, b := img.RGBAAt(x, y), img.RGBAAt(200+x, 200+y); a != b {
				t.Fatalf("(%d,%d)=%v pero (%d,%d)=%v", x, y, a, 200+x, 200+y, b)
			}
		}
	}
}

func TestDialRechazaEncodingNoImplementado(t *testing.T) {
	_, err := Dial(context.Background(), "ws://127.0.0.1:1/websockify", Options{Encodings: []string{"tight"}})
	if err == nil || !strings.Contains(err.Error(), "tight") {
		t.Fatalf("error %v", err)
	}
}
