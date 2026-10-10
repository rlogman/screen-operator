// Package rfbserver es un servidor RFB 3.8 falso sobre websocket para tests.
//
// Emula las tres trampas de material/README.md ("Lo que vimos operando el
// escritorio"): el efecto de un clic se repinta con retraso (RepaintDelay), un
// clic con menos de MinClickHold entre down y up se ignora, y un campo de texto
// concatena lo que se escribe salvo que antes se seleccione todo.
//
// Es independiente del cliente de internal/rfb a proposito: el codigo de
// protocolo (DES, formato de pixel, mensajes) esta escrito aparte para que un
// error en el cliente no quede tapado por el mismo error en el doble.
package rfbserver

import (
	"bufio"
	"context"
	"crypto/des"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// Botones y keysyms que interpreta el servidor.
const (
	button1      = 1
	ksControlL   = 0xffe3
	ksControlR   = 0xffe4
	ksBackSpace  = 0xff08
	ksDelete     = 0xffff
	tripleWindow = 500 * time.Millisecond
)

// Options configura el servidor falso.
type Options struct {
	// Password activa VNC Authentication. Vacia = seguridad None.
	Password string
	// Width y Height del framebuffer (por defecto 1280x800).
	Width, Height int
	// Name es el nombre de escritorio de ServerInit.
	Name string
	// Background es el color inicial del framebuffer.
	Background color.RGBA
	// RepaintDelay retrasa el efecto de cada clic aceptado, medido desde el up.
	RepaintDelay time.Duration
	// MinClickHold: un clic con menos tiempo entre down y up se ignora.
	MinClickHold time.Duration
}

// Event es un PointerEvent o KeyEvent recibido.
type Event struct {
	Time    time.Time
	Kind    string // "pointer" o "key"
	X, Y    int    // pointer
	Buttons uint8  // pointer: mascara RFB
	Keysym  uint32 // key
	Down    bool   // key: down/up; pointer: algun boton presionado
}

// Click es un par down/up de un boton, derivado de los PointerEvent.
type Click struct {
	Button   uint8 // numero de boton 1..8
	X, Y     int
	Down, Up time.Time
	Ignored  bool // hold menor que MinClickHold
}

// Hold es el tiempo entre down y up.
func (c Click) Hold() time.Duration { return c.Up.Sub(c.Down) }

// ClickFunc es el efecto de un clic aceptado del boton 1. Corre RepaintDelay
// despues del up y puede pintar con Server.FillRect.
type ClickFunc func(s *Server, c Click)

type copyOp struct {
	src image.Rectangle
	dst image.Point
}

type conn struct {
	nc      net.Conn
	wmu     sync.Mutex
	dirty   image.Rectangle
	copies  []copyOp
	pf      pixelFormat
	encs    map[int32]bool
	pending bool
}

type field struct {
	rect     image.Rectangle
	text     []rune
	selected bool
	focused  bool
}

// Server es el servidor falso.
type Server struct {
	opts    Options
	http    *httptest.Server
	mu      sync.Mutex
	fb      *image.RGBA
	conns   map[*conn]bool
	events  []Event
	clicks  []Click
	buttons uint8
	downAt  map[uint8]time.Time
	downXY  map[uint8]image.Point
	onClick ClickFunc
	onFeed  ClickFunc
	animate bool
	frame   int
	field   *field
	ctrl    map[uint32]bool
	accepts []time.Time
	opened  int
	closed  int
	connCh  chan struct{}
}

// Start arranca el servidor en 127.0.0.1 con un puerto libre.
func Start(opts Options) *Server {
	if opts.Width == 0 {
		opts.Width = 1280
	}
	if opts.Height == 0 {
		opts.Height = 800
	}
	if opts.Name == "" {
		opts.Name = "screen-operator fake"
	}
	if opts.Background == (color.RGBA{}) {
		opts.Background = color.RGBA{240, 240, 240, 255}
	}
	s := &Server{
		opts:   opts,
		fb:     image.NewRGBA(image.Rect(0, 0, opts.Width, opts.Height)),
		conns:  map[*conn]bool{},
		downAt: map[uint8]time.Time{},
		downXY: map[uint8]image.Point{},
		ctrl:   map[uint32]bool{},
		connCh: make(chan struct{}, 64),
	}
	draw.Draw(s.fb, s.fb.Rect, &image.Uniform{opts.Background}, image.Point{}, draw.Src)
	mux := http.NewServeMux()
	mux.HandleFunc("/websockify", s.handle)
	s.http = httptest.NewServer(mux)
	return s
}

// URL devuelve ws://127.0.0.1:<puerto>/websockify.
func (s *Server) URL() string {
	return "ws" + strings.TrimPrefix(s.http.URL, "http") + "/websockify"
}

// Close detiene el servidor y corta las conexiones abiertas.
func (s *Server) Close() {
	s.mu.Lock()
	for c := range s.conns {
		c.nc.Close()
	}
	s.mu.Unlock()
	s.http.CloseClientConnections()
	s.http.Close()
}

// OnClick fija el efecto de los clics aceptados del boton 1.
func (s *Server) OnClick(fn ClickFunc) {
	s.mu.Lock()
	s.onClick = fn
	s.mu.Unlock()
}

// OnClickFeedback fija un efecto inmediato de los clics aceptados del boton 1,
// que se pinta sin esperar RepaintDelay (p. ej. el resaltado de un item de
// menu). Junto con OnClick emula la trampa "feedback inmediato pequeno y luego
// el repintado real con retraso" del escritorio.
func (s *Server) OnClickFeedback(fn ClickFunc) {
	s.mu.Lock()
	s.onFeed = fn
	s.mu.Unlock()
}

// SetAnimate hace que cada actualizacion pedida traiga un cuadro distinto
// (pantalla que cambia continuamente).
func (s *Server) SetAnimate(on bool) {
	s.mu.Lock()
	s.animate = on
	s.mu.Unlock()
}

// FillRect pinta un rectangulo de inmediato.
func (s *Server) FillRect(r image.Rectangle, c color.RGBA) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fillLocked(r, c)
}

func (s *Server) fillLocked(r image.Rectangle, c color.RGBA) {
	r = r.Intersect(s.fb.Rect)
	draw.Draw(s.fb, r, &image.Uniform{c}, image.Point{}, draw.Src)
	s.markDirty(r)
}

func (s *Server) markDirty(r image.Rectangle) {
	for c := range s.conns {
		c.dirty = c.dirty.Union(r)
	}
}

// SetPixel cambia un solo pixel de inmediato.
func (s *Server) SetPixel(x, y int, c color.RGBA) {
	s.FillRect(image.Rect(x, y, x+1, y+1), c)
}

// CopyRect copia src a dst en el framebuffer y, a cada cliente que pidio
// CopyRect y tiene esa zona al dia, se lo envia como CopyRect en la proxima
// actualizacion.
func (s *Server) CopyRect(src image.Rectangle, dst image.Point) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tmp := image.NewRGBA(src)
	draw.Draw(tmp, src, s.fb, src.Min, draw.Src)
	dr := image.Rectangle{dst, dst.Add(src.Size())}
	draw.Draw(s.fb, dr, tmp, src.Min, draw.Src)
	for c := range s.conns {
		if c.encs[1] && !c.dirty.Overlaps(src) && !c.dirty.Overlaps(dr) {
			c.copies = append(c.copies, copyOp{src, dst})
		} else {
			c.dirty = c.dirty.Union(dr)
		}
	}
}

// Snapshot devuelve una copia del framebuffer.
func (s *Server) Snapshot() *image.RGBA {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := image.NewRGBA(s.fb.Rect)
	copy(out.Pix, s.fb.Pix)
	return out
}

// AddTextField crea un campo de texto con contenido inicial. Un clic aceptado
// dentro lo enfoca; tres clics seguidos seleccionan todo; ctrl+a selecciona
// todo; escribir con todo seleccionado reemplaza y sin seleccion concatena.
func (s *Server) AddTextField(r image.Rectangle, initial string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.field = &field{rect: r, text: []rune(initial)}
	s.renderField()
}

// FieldText devuelve el contenido del campo de texto.
func (s *Server) FieldText() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.field == nil {
		return ""
	}
	return string(s.field.text)
}

func (s *Server) renderField() {
	f := s.field
	s.fillLocked(f.rect, color.RGBA{255, 255, 255, 255})
	bar := f.rect
	bar.Max.X = min(f.rect.Min.X+8*len(f.text), f.rect.Max.X)
	bar = bar.Inset(2)
	if len(f.text) > 0 && !bar.Empty() {
		c := color.RGBA{30, 30, 30, 255}
		if f.selected {
			c = color.RGBA{40, 90, 200, 255}
		}
		s.fillLocked(bar, c)
	}
}

// Events devuelve una copia de los eventos recibidos.
func (s *Server) Events() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Event(nil), s.events...)
}

// Clicks devuelve los clics (pares down/up) recibidos.
func (s *Server) Clicks() []Click {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Click(nil), s.clicks...)
}

// ResetEvents borra eventos y clics registrados.
func (s *Server) ResetEvents() {
	s.mu.Lock()
	s.events, s.clicks = nil, nil
	s.mu.Unlock()
}

// Connections devuelve cuantas conexiones websocket se abrieron y cuantas se cerraron.
func (s *Server) Connections() (opened, closed int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.opened, s.closed
}

// WaitClosed espera hasta que todas las conexiones abiertas se cierren.
func (s *Server) WaitClosed(timeout time.Duration) bool {
	deadline := time.After(timeout)
	for {
		o, c := s.Connections()
		if o > 0 && o == c {
			return true
		}
		select {
		case <-s.connCh:
		case <-deadline:
			return false
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	wsc, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{"binary"}})
	if err != nil {
		return
	}
	nc := websocket.NetConn(context.Background(), wsc, websocket.MessageBinary)
	c := &conn{nc: nc, encs: map[int32]bool{}, pf: defaultPF}
	s.mu.Lock()
	s.opened++
	s.mu.Unlock()
	defer func() {
		nc.Close()
		s.mu.Lock()
		delete(s.conns, c)
		s.closed++
		s.mu.Unlock()
		select {
		case s.connCh <- struct{}{}:
		default:
		}
	}()
	br := bufio.NewReader(nc)
	if err := s.handshake(c, br); err != nil {
		return
	}
	s.mu.Lock()
	c.dirty = s.fb.Rect
	s.conns[c] = true
	s.mu.Unlock()
	s.serve(c, br)
}

func (s *Server) handshake(c *conn, br *bufio.Reader) error {
	if _, err := c.nc.Write([]byte("RFB 003.008\n")); err != nil {
		return err
	}
	ver := make([]byte, 12)
	if _, err := io.ReadFull(br, ver); err != nil {
		return err
	}
	if string(ver) != "RFB 003.008\n" {
		return fmt.Errorf("version no soportada por el falso: %q", ver)
	}
	secType := byte(1)
	if s.opts.Password != "" {
		secType = 2
	}
	if _, err := c.nc.Write([]byte{1, secType}); err != nil {
		return err
	}
	chosen := make([]byte, 1)
	if _, err := io.ReadFull(br, chosen); err != nil {
		return err
	}
	if chosen[0] != secType {
		s.fail(c, "tipo de seguridad no ofrecido")
		return errors.New("tipo de seguridad invalido")
	}
	if secType == 2 {
		challenge := make([]byte, 16)
		if _, err := rand.Read(challenge); err != nil {
			return err
		}
		if _, err := c.nc.Write(challenge); err != nil {
			return err
		}
		resp := make([]byte, 16)
		if _, err := io.ReadFull(br, resp); err != nil {
			return err
		}
		want := encryptChallenge(challenge, s.opts.Password)
		if string(want) != string(resp) {
			s.fail(c, "autenticacion fallida")
			return errors.New("auth fallida")
		}
	}
	if _, err := c.nc.Write([]byte{0, 0, 0, 0}); err != nil {
		return err
	}
	if _, err := io.ReadFull(br, make([]byte, 1)); err != nil { // ClientInit
		return err
	}
	init := make([]byte, 24, 24+len(s.opts.Name))
	binary.BigEndian.PutUint16(init[0:2], uint16(s.opts.Width))
	binary.BigEndian.PutUint16(init[2:4], uint16(s.opts.Height))
	copy(init[4:20], defaultPF.bytes())
	binary.BigEndian.PutUint32(init[20:24], uint32(len(s.opts.Name)))
	init = append(init, s.opts.Name...)
	_, err := c.nc.Write(init)
	return err
}

// fail envia SecurityResult 1 con una razon. La conexion se cierra al volver.
func (s *Server) fail(c *conn, reason string) {
	b := make([]byte, 8+len(reason))
	binary.BigEndian.PutUint32(b[0:4], 1)
	binary.BigEndian.PutUint32(b[4:8], uint32(len(reason)))
	copy(b[8:], reason)
	c.nc.Write(b)
}

// encryptChallenge: DES-ECB con la clave de VNC (bits invertidos por byte).
func encryptChallenge(ch []byte, password string) []byte {
	key := make([]byte, 8)
	for i := 0; i < 8 && i < len(password); i++ {
		b := password[i]
		var r byte
		for bit := 0; bit < 8; bit++ {
			if b&(1<<bit) != 0 {
				r |= 0x80 >> bit
			}
		}
		key[i] = r
	}
	blk, _ := des.NewCipher(key)
	out := make([]byte, 16)
	blk.Encrypt(out[0:8], ch[0:8])
	blk.Encrypt(out[8:16], ch[8:16])
	return out
}

func (s *Server) serve(c *conn, br *bufio.Reader) {
	for {
		t, err := br.ReadByte()
		if err != nil {
			return
		}
		switch t {
		case 0: // SetPixelFormat
			b := make([]byte, 19)
			if _, err := io.ReadFull(br, b); err != nil {
				return
			}
			pf := parsePF(b[3:])
			if pf.bpp != 32 || !pf.trueColor {
				return
			}
			s.mu.Lock()
			c.pf = pf
			s.mu.Unlock()
		case 2: // SetEncodings
			b := make([]byte, 3)
			if _, err := io.ReadFull(br, b); err != nil {
				return
			}
			n := int(binary.BigEndian.Uint16(b[1:3]))
			list := make([]byte, 4*n)
			if _, err := io.ReadFull(br, list); err != nil {
				return
			}
			s.mu.Lock()
			for i := 0; i < n; i++ {
				c.encs[int32(binary.BigEndian.Uint32(list[4*i:]))] = true
			}
			s.mu.Unlock()
		case 3: // FramebufferUpdateRequest
			b := make([]byte, 9)
			if _, err := io.ReadFull(br, b); err != nil {
				return
			}
			if err := s.sendUpdate(c, b[0] == 1); err != nil {
				return
			}
		case 4: // KeyEvent
			b := make([]byte, 7)
			if _, err := io.ReadFull(br, b); err != nil {
				return
			}
			s.key(binary.BigEndian.Uint32(b[3:7]), b[0] == 1)
		case 5: // PointerEvent
			b := make([]byte, 5)
			if _, err := io.ReadFull(br, b); err != nil {
				return
			}
			s.pointer(int(binary.BigEndian.Uint16(b[1:3])), int(binary.BigEndian.Uint16(b[3:5])), b[0])
		case 6: // ClientCutText
			b := make([]byte, 7)
			if _, err := io.ReadFull(br, b); err != nil {
				return
			}
			if _, err := io.CopyN(io.Discard, br, int64(binary.BigEndian.Uint32(b[3:7]))); err != nil {
				return
			}
		default:
			return
		}
	}
}

// sendUpdate responde de inmediato: CopyRect pendientes y luego la zona sucia
// en Raw (todo el framebuffer si la peticion no es incremental). Sin cambios
// envia una actualizacion con 0 rectangulos.
func (s *Server) sendUpdate(c *conn, incremental bool) error {
	s.mu.Lock()
	if s.animate {
		s.frame++
		v := uint8(s.frame * 37)
		s.fillLocked(image.Rect(0, 0, 200, 200), color.RGBA{v, 255 - v, v / 2, 255})
	}
	region := c.dirty
	if !incremental {
		region = s.fb.Rect
	}
	copies := c.copies
	if !incremental {
		copies = nil
	}
	c.copies = nil
	c.dirty = image.Rectangle{}
	nrects := len(copies)
	if !region.Empty() {
		nrects++
	}
	msg := make([]byte, 4, 4+nrects*12+region.Dx()*region.Dy()*4)
	binary.BigEndian.PutUint16(msg[2:4], uint16(nrects))
	for _, op := range copies {
		msg = appendRectHeader(msg, image.Rectangle{op.dst, op.dst.Add(op.src.Size())}, 1)
		msg = binary.BigEndian.AppendUint16(msg, uint16(op.src.Min.X))
		msg = binary.BigEndian.AppendUint16(msg, uint16(op.src.Min.Y))
	}
	if !region.Empty() {
		msg = appendRectHeader(msg, region, 0)
		for y := region.Min.Y; y < region.Max.Y; y++ {
			for x := region.Min.X; x < region.Max.X; x++ {
				o := s.fb.PixOffset(x, y)
				msg = c.pf.appendPixel(msg, s.fb.Pix[o], s.fb.Pix[o+1], s.fb.Pix[o+2])
			}
		}
	}
	s.mu.Unlock()
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_, err := c.nc.Write(msg)
	return err
}

func appendRectHeader(b []byte, r image.Rectangle, enc int32) []byte {
	b = binary.BigEndian.AppendUint16(b, uint16(r.Min.X))
	b = binary.BigEndian.AppendUint16(b, uint16(r.Min.Y))
	b = binary.BigEndian.AppendUint16(b, uint16(r.Dx()))
	b = binary.BigEndian.AppendUint16(b, uint16(r.Dy()))
	return binary.BigEndian.AppendUint32(b, uint32(enc))
}

func (s *Server) key(keysym uint32, down bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, Event{Time: time.Now(), Kind: "key", Keysym: keysym, Down: down})
	if keysym == ksControlL || keysym == ksControlR {
		s.ctrl[keysym] = down
		return
	}
	f := s.field
	if !down || f == nil || !f.focused {
		return
	}
	ctrl := s.ctrl[ksControlL] || s.ctrl[ksControlR]
	switch {
	case ctrl && (keysym == 'a' || keysym == 'A'):
		f.selected = true
	case ctrl:
	case keysym == ksBackSpace:
		if f.selected {
			f.text = nil
		} else if len(f.text) > 0 {
			f.text = f.text[:len(f.text)-1]
		}
		f.selected = false
	case keysym == ksDelete:
		if f.selected {
			f.text = nil
		}
		f.selected = false
	case printable(keysym):
		if f.selected {
			f.text = nil
			f.selected = false
		}
		f.text = append(f.text, keysymRune(keysym))
	default:
		f.selected = false
	}
	s.renderField()
}

func printable(k uint32) bool {
	return (k >= 0x20 && k <= 0x7e) || (k >= 0xa0 && k <= 0xff) || k&0xff000000 == 0x01000000
}

func keysymRune(k uint32) rune {
	if k&0xff000000 == 0x01000000 {
		return rune(k & 0x00ffffff)
	}
	return rune(k)
}

func (s *Server) pointer(x, y int, mask uint8) {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, Event{Time: now, Kind: "pointer", X: x, Y: y, Buttons: mask, Down: mask != 0})
	prev := s.buttons
	s.buttons = mask
	for bit := uint8(0); bit < 8; bit++ {
		b := uint8(1) << bit
		switch {
		case mask&b != 0 && prev&b == 0:
			s.downAt[b] = now
			s.downXY[b] = image.Pt(x, y)
		case mask&b == 0 && prev&b != 0:
			p := s.downXY[b]
			c := Click{Button: bit + 1, X: p.X, Y: p.Y, Down: s.downAt[b], Up: now}
			c.Ignored = s.opts.MinClickHold > 0 && c.Hold() < s.opts.MinClickHold
			s.clicks = append(s.clicks, c)
			if b == button1 && !c.Ignored {
				s.acceptClick(c)
			}
		}
	}
}

// acceptClick aplica el efecto de un clic valido del boton 1. Se llama con mu tomado.
func (s *Server) acceptClick(c Click) {
	if f := s.field; f != nil {
		in := image.Pt(c.X, c.Y).In(f.rect)
		f.focused = in
		if in {
			s.accepts = append(s.accepts, c.Down)
			n := len(s.accepts)
			if n >= 3 && c.Down.Sub(s.accepts[n-3]) <= 2*tripleWindow &&
				s.accepts[n-1].Sub(s.accepts[n-2]) <= tripleWindow &&
				s.accepts[n-2].Sub(s.accepts[n-3]) <= tripleWindow {
				f.selected = true
				s.accepts = nil
			} else {
				f.selected = false
			}
			s.renderField()
		} else {
			s.accepts = nil
		}
	}
	if fb := s.onFeed; fb != nil {
		go fb(s, c)
	}
	fn := s.onClick
	if fn == nil {
		return
	}
	apply := func() { fn(s, c) }
	if s.opts.RepaintDelay > 0 {
		time.AfterFunc(s.opts.RepaintDelay, apply)
	} else {
		go apply()
	}
}

type pixelFormat struct {
	bpp, depth             uint8
	bigEndian, trueColor   bool
	rmax, gmax, bmax       uint16
	rshift, gshift, bshift uint8
}

// defaultPF: el formato habitual de los servidores (BGRX little-endian).
var defaultPF = pixelFormat{32, 24, false, true, 255, 255, 255, 16, 8, 0}

func (p pixelFormat) bytes() []byte {
	b := make([]byte, 16)
	b[0], b[1] = p.bpp, p.depth
	if p.bigEndian {
		b[2] = 1
	}
	if p.trueColor {
		b[3] = 1
	}
	binary.BigEndian.PutUint16(b[4:6], p.rmax)
	binary.BigEndian.PutUint16(b[6:8], p.gmax)
	binary.BigEndian.PutUint16(b[8:10], p.bmax)
	b[10], b[11], b[12] = p.rshift, p.gshift, p.bshift
	return b
}

func parsePF(b []byte) pixelFormat {
	return pixelFormat{
		bpp: b[0], depth: b[1], bigEndian: b[2] != 0, trueColor: b[3] != 0,
		rmax: binary.BigEndian.Uint16(b[4:6]), gmax: binary.BigEndian.Uint16(b[6:8]), bmax: binary.BigEndian.Uint16(b[8:10]),
		rshift: b[10], gshift: b[11], bshift: b[12],
	}
}

func (p pixelFormat) appendPixel(b []byte, r, g, bl uint8) []byte {
	v := uint32(r)*uint32(p.rmax)/255<<p.rshift |
		uint32(g)*uint32(p.gmax)/255<<p.gshift |
		uint32(bl)*uint32(p.bmax)/255<<p.bshift
	if p.bigEndian {
		return binary.BigEndian.AppendUint32(b, v)
	}
	return binary.LittleEndian.AppendUint32(b, v)
}
