package rfbserver

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/hex"
	"image"
	"image/color"
	"io"
	"net"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// rawClient habla RFB a mano (sin internal/rfb) para probar el servidor falso
// de forma independiente del cliente real.
type rawClient struct {
	t      *testing.T
	nc     net.Conn
	br     *bufio.Reader
	w, h   int
	result uint32
	reason string
}

func dialRaw(t *testing.T, url, password string) *rawClient {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	wsc, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{Subprotocols: []string{"binary"}})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	nc := websocket.NetConn(context.Background(), wsc, websocket.MessageBinary)
	nc.SetDeadline(time.Now().Add(10 * time.Second))
	c := &rawClient{t: t, nc: nc, br: bufio.NewReader(nc)}
	t.Cleanup(func() { nc.Close() })
	ver := c.read(12)
	if string(ver) != "RFB 003.008\n" {
		t.Fatalf("version %q", ver)
	}
	c.write([]byte("RFB 003.008\n"))
	n := c.read(1)[0]
	types := c.read(int(n))
	c.write(types[:1])
	if types[0] == 2 {
		ch := c.read(16)
		c.write(encryptChallenge(ch, password))
	}
	c.result = binary.BigEndian.Uint32(c.read(4))
	if c.result != 0 {
		l := binary.BigEndian.Uint32(c.read(4))
		c.reason = string(c.read(int(l)))
		return c
	}
	c.write([]byte{1})
	init := c.read(24)
	c.w = int(binary.BigEndian.Uint16(init[0:2]))
	c.h = int(binary.BigEndian.Uint16(init[2:4]))
	c.read(int(binary.BigEndian.Uint32(init[20:24])))
	return c
}

func (c *rawClient) read(n int) []byte {
	c.t.Helper()
	b := make([]byte, n)
	if _, err := io.ReadFull(c.br, b); err != nil {
		c.t.Fatalf("read %d: %v", n, err)
	}
	return b
}

func (c *rawClient) write(b []byte) {
	c.t.Helper()
	if _, err := c.nc.Write(b); err != nil {
		c.t.Fatalf("write: %v", err)
	}
}

func (c *rawClient) pointer(x, y int, mask uint8) {
	b := []byte{5, mask, 0, 0, 0, 0}
	binary.BigEndian.PutUint16(b[2:], uint16(x))
	binary.BigEndian.PutUint16(b[4:], uint16(y))
	c.write(b)
}

func (c *rawClient) key(ks uint32, down bool) {
	b := []byte{4, 0, 0, 0, 0, 0, 0, 0}
	if down {
		b[1] = 1
	}
	binary.BigEndian.PutUint32(b[4:], ks)
	c.write(b)
}

func (c *rawClient) click(x, y int, hold time.Duration) {
	c.pointer(x, y, 0)
	c.pointer(x, y, 1)
	time.Sleep(hold)
	c.pointer(x, y, 0)
}

// pixelAt pide una actualizacion completa en el formato por defecto (BGRX) y
// devuelve el color de (x,y).
func (c *rawClient) pixelAt(x, y int) color.RGBA {
	c.t.Helper()
	req := []byte{3, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	binary.BigEndian.PutUint16(req[6:], uint16(c.w))
	binary.BigEndian.PutUint16(req[8:], uint16(c.h))
	c.write(req)
	hdr := c.read(4)
	if hdr[0] != 0 {
		c.t.Fatalf("mensaje %d, se esperaba FramebufferUpdate", hdr[0])
	}
	var out color.RGBA
	for i := 0; i < int(binary.BigEndian.Uint16(hdr[2:])); i++ {
		rh := c.read(12)
		rx, ry := int(binary.BigEndian.Uint16(rh[0:])), int(binary.BigEndian.Uint16(rh[2:]))
		rw, rht := int(binary.BigEndian.Uint16(rh[4:])), int(binary.BigEndian.Uint16(rh[6:]))
		if enc := binary.BigEndian.Uint32(rh[8:]); enc != 0 {
			c.t.Fatalf("encoding %d inesperado", enc)
		}
		px := c.read(rw * rht * 4)
		if x >= rx && x < rx+rw && y >= ry && y < ry+rht {
			o := ((y-ry)*rw + (x - rx)) * 4
			out = color.RGBA{px[o+2], px[o+1], px[o], 255}
		}
	}
	return out
}

func TestEncryptChallengeVector(t *testing.T) {
	// Vector calculado con: openssl enc -des-ecb -K cea6c64ea62e868c (clave de
	// 'secreta1' con bits invertidos) sobre los bytes 00..0f.
	ch := make([]byte, 16)
	for i := range ch {
		ch[i] = byte(i)
	}
	got := hex.EncodeToString(encryptChallenge(ch, "secreta1"))
	if got != "37e123c8cdc141be6a960d6e01674bad" {
		t.Fatalf("respuesta DES %s", got)
	}
}

func TestE2_1_S1_ServerInitTrasAuthOK(t *testing.T) {
	s := Start(Options{Password: "secreta1", Width: 1280, Height: 800})
	defer s.Close()
	c := dialRaw(t, s.URL(), "secreta1")
	if c.result != 0 {
		t.Fatalf("SecurityResult %d (%s)", c.result, c.reason)
	}
	if c.w != 1280 || c.h != 800 {
		t.Fatalf("ServerInit %dx%d, se esperaba 1280x800", c.w, c.h)
	}
}

func TestE2_1_S2_AuthFallidaConRazonYCierre(t *testing.T) {
	s := Start(Options{Password: "secreta1"})
	defer s.Close()
	c := dialRaw(t, s.URL(), "otra")
	if c.result == 0 {
		t.Fatal("SecurityResult 0 con contrasena equivocada")
	}
	if c.reason == "" {
		t.Fatal("SecurityResult sin razon")
	}
	c.nc.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := c.br.ReadByte(); err == nil {
		t.Fatal("el servidor no cerro la conexion tras la auth fallida")
	}
}

func TestE2_1_S3_RegistraEventosDePunteroYTeclado(t *testing.T) {
	s := Start(Options{Password: "secreta1"})
	defer s.Close()
	c := dialRaw(t, s.URL(), "secreta1")
	start := time.Now()
	c.pointer(500, 300, 0)
	c.pointer(500, 300, 1)
	c.pointer(501, 301, 0)
	c.key('a', true)
	c.key('a', false)
	waitFor(t, func() bool { return len(s.Events()) == 5 })
	ev := s.Events()
	want := []Event{
		{Kind: "pointer", X: 500, Y: 300, Buttons: 0, Down: false},
		{Kind: "pointer", X: 500, Y: 300, Buttons: 1, Down: true},
		{Kind: "pointer", X: 501, Y: 301, Buttons: 0, Down: false},
		{Kind: "key", Keysym: 'a', Down: true},
		{Kind: "key", Keysym: 'a', Down: false},
	}
	for i, w := range want {
		g := ev[i]
		if g.Time.Before(start) || (i > 0 && g.Time.Before(ev[i-1].Time)) {
			t.Fatalf("evento %d sin timestamp valido: %v", i, g.Time)
		}
		g.Time = time.Time{}
		if g != w {
			t.Fatalf("evento %d = %+v, se esperaba %+v", i, g, w)
		}
	}
}

func TestE2_1_S4_RepintadoRetrasadoTrasClic(t *testing.T) {
	red := color.RGBA{255, 0, 0, 255}
	s := Start(Options{Password: "secreta1", RepaintDelay: 800 * time.Millisecond})
	defer s.Close()
	s.OnClick(func(s *Server, c Click) { s.FillRect(image.Rect(0, 0, 100, 100), red) })
	c := dialRaw(t, s.URL(), "secreta1")
	bg := c.pixelAt(50, 50)
	c.click(50, 50, 150*time.Millisecond)
	up := time.Now()
	for _, at := range []time.Duration{100 * time.Millisecond, 600 * time.Millisecond} {
		time.Sleep(time.Until(up.Add(at)))
		if got := c.pixelAt(50, 50); got != bg {
			t.Fatalf("a %v del clic el pixel ya cambio a %v", at, got)
		}
	}
	time.Sleep(time.Until(up.Add(900 * time.Millisecond)))
	if got := c.pixelAt(50, 50); got != red {
		t.Fatalf("a 900ms del clic el pixel es %v, se esperaba rojo", got)
	}
}

func TestE2_1_S5_IgnoraClicCorto(t *testing.T) {
	red := color.RGBA{255, 0, 0, 255}
	s := Start(Options{MinClickHold: 100 * time.Millisecond})
	defer s.Close()
	s.OnClick(func(s *Server, c Click) { s.FillRect(image.Rect(0, 0, 100, 100), red) })
	c := dialRaw(t, s.URL(), "")
	bg := c.pixelAt(50, 50)
	c.click(50, 50, 50*time.Millisecond)
	waitFor(t, func() bool { return len(s.Clicks()) == 1 })
	time.Sleep(200 * time.Millisecond)
	if got := c.pixelAt(50, 50); got != bg {
		t.Fatalf("un clic de 50ms aplico su efecto (pixel %v)", got)
	}
	if cl := s.Clicks()[0]; !cl.Ignored || cl.Hold() >= 100*time.Millisecond {
		t.Fatalf("clic registrado %+v, se esperaba ignorado", cl)
	}
	// Control: un clic de 150 ms si aplica el efecto.
	c.click(50, 50, 150*time.Millisecond)
	waitFor(t, func() bool { return c.pixelAt(50, 50) == red })
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condicion no cumplida en 3s")
}
