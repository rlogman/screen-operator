package rfb

// Cliente RFB 3.3/3.7/3.8 (RFC 6143) sobre websocket (websockify). Implementa el
// subconjunto que necesita el agente: auth None y VNC, ServerInit, formato de
// pixel fijo de 32 bits, encodings Raw y CopyRect, PointerEvent y KeyEvent.

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"io"
	"net"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

// Errores de la sesion. Ninguno contiene la contrasena.
var (
	ErrAuth     = errors.New("rfb: autenticacion VNC rechazada")
	ErrTimeout  = errors.New("rfb: tiempo de conexion agotado")
	ErrClosed   = errors.New("rfb: sesion cerrada")
	ErrProtocol = errors.New("rfb: error de protocolo")
)

// Encodings RFB soportados.
const (
	EncodingRaw      int32 = 0
	EncodingCopyRect int32 = 1
)

// Valores por defecto de Options.
const (
	DefaultConnectTimeout = 10 * time.Second
	DefaultCaptureWait    = 300 * time.Millisecond
)

// Options configura Dial. Se llena desde surface.rfb.* y la peticion de la tarea.
type Options struct {
	// Password de VNC Authentication. Nunca aparece en errores.
	Password string
	// Auth exige un tipo de seguridad: "vnc", "none" o "" (el mejor ofrecido).
	Auth string
	// Shared es el flag de ClientInit: true no desconecta a otros clientes.
	Shared bool
	// Encodings por nombre de configuracion ("raw", "copyrect"). Vacio = ambos.
	// Raw se pide siempre: el protocolo lo exige.
	Encodings []string
	// ConnectTimeout acota websocket + handshake + primer framebuffer.
	ConnectTimeout time.Duration
	// CaptureWait es cuanto espera Capture la respuesta a su peticion de
	// actualizacion. Un servidor que no tiene cambios puede retener la peticion;
	// en ese caso el framebuffer local ya esta al dia.
	CaptureWait time.Duration
}

// Session es una conexion RFB abierta. Es segura para uso concurrente.
type Session struct {
	conn     net.Conn
	br       *bufio.Reader
	secret   string
	width    int
	height   int
	name     string
	encs     map[int32]bool
	capWait  time.Duration
	wmu      sync.Mutex
	mu       sync.Mutex
	fb       *image.RGBA
	seq      uint64
	stats    Stats
	notify   chan struct{}
	closed   atomic.Bool
	done     chan struct{}
	readErr  error
	closeOne sync.Once
}

// Dial abre el websocket, negocia RFB, autentica y espera el primer framebuffer
// completo. Todo el proceso queda acotado por opt.ConnectTimeout.
func Dial(ctx context.Context, rawURL string, opt Options) (*Session, error) {
	if opt.ConnectTimeout <= 0 {
		opt.ConnectTimeout = DefaultConnectTimeout
	}
	if opt.CaptureWait <= 0 {
		opt.CaptureWait = DefaultCaptureWait
	}
	encs, err := encodingList(opt.Encodings)
	if err != nil {
		return nil, err
	}
	where, err := describeURL(rawURL)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, opt.ConnectTimeout)
	defer cancel()

	wsc, _, err := websocket.Dial(ctx, rawURL, &websocket.DialOptions{Subprotocols: []string{"binary"}})
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("%w: %s sin respuesta en %s", ErrTimeout, where, opt.ConnectTimeout)
		}
		return nil, fmt.Errorf("rfb: no se pudo abrir el websocket de %s: %s", where, redact(err.Error(), opt.Password))
	}
	nc := websocket.NetConn(context.Background(), wsc, websocket.MessageBinary)
	s := &Session{
		conn:    nc,
		br:      bufio.NewReaderSize(nc, 64<<10),
		secret:  opt.Password,
		capWait: opt.CaptureWait,
		notify:  make(chan struct{}),
		done:    make(chan struct{}),
		encs:    map[int32]bool{},
	}
	for _, e := range encs {
		s.encs[e] = true
	}

	// Vigilante del plazo: si vence durante el handshake cierra la conexion, lo
	// que desbloquea cualquier lectura pendiente.
	var timedOut atomic.Bool
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		select {
		case <-ctx.Done():
			timedOut.Store(true)
			nc.Close()
		case <-stop:
		}
	}()
	err = s.handshake(opt)
	if err == nil {
		err = s.setup(encs)
	}
	if err == nil {
		go s.readLoop()
		err = s.waitFirstFrame(ctx)
	}
	close(stop)
	wg.Wait()
	if err != nil || timedOut.Load() {
		s.Close()
		if timedOut.Load() {
			return nil, fmt.Errorf("%w: %s no completo el handshake RFB en %s", ErrTimeout, where, opt.ConnectTimeout)
		}
		return nil, err
	}
	return s, nil
}

// describeURL devuelve esquema://host/ruta sin userinfo ni query, para errores.
func describeURL(rawURL string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("rfb: URL del escritorio invalida (se espera ws:// o wss://)")
	}
	switch strings.ToLower(u.Scheme) {
	case "ws", "wss", "http", "https":
	default:
		return "", fmt.Errorf("rfb: esquema %q no soportado (se espera ws:// o wss://)", u.Scheme)
	}
	return u.Scheme + "://" + u.Host + u.Path, nil
}

func encodingList(names []string) ([]int32, error) {
	if len(names) == 0 {
		names = []string{"copyrect", "raw"}
	}
	var out []int32
	for _, n := range names {
		switch strings.ToLower(n) {
		case "raw":
		case "copyrect":
			out = append(out, EncodingCopyRect)
		default:
			return nil, fmt.Errorf("rfb: encoding %q no implementado (soportados: raw, copyrect)", n)
		}
	}
	// Raw al final: el servidor usa el primero que soporta, y Raw siempre esta.
	return append(out, EncodingRaw), nil
}

func (s *Session) protoErr(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrProtocol, redact(fmt.Sprintf(format, a...), s.secret))
}

func (s *Session) readFull(n int) ([]byte, error) {
	b := make([]byte, n)
	if _, err := io.ReadFull(s.br, b); err != nil {
		return nil, s.protoErr("lectura incompleta: %v", err)
	}
	return b, nil
}

func (s *Session) readU32() (uint32, error) {
	b, err := s.readFull(4)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint32(b), nil
}

func (s *Session) readReason() string {
	n, err := s.readU32()
	if err != nil || n > 4096 {
		return ""
	}
	b, err := s.readFull(int(n))
	if err != nil {
		return ""
	}
	return redact(string(b), s.secret)
}

func (s *Session) write(b []byte) error {
	if s.closed.Load() {
		return ErrClosed
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	if _, err := s.conn.Write(b); err != nil {
		if s.closed.Load() {
			return ErrClosed
		}
		return fmt.Errorf("rfb: no se pudo enviar al servidor: %s", redact(err.Error(), s.secret))
	}
	return nil
}

func (s *Session) handshake(opt Options) error {
	ver, err := s.readFull(12)
	if err != nil {
		return err
	}
	var major, minor int
	if _, err := fmt.Sscanf(string(ver), "RFB %03d.%03d\n", &major, &minor); err != nil || major != 3 {
		return s.protoErr("version de protocolo no reconocida")
	}
	switch {
	case minor >= 8:
		minor = 8
	case minor == 7:
	default:
		minor = 3
	}
	if err := s.write([]byte(fmt.Sprintf("RFB 003.%03d\n", minor))); err != nil {
		return err
	}

	var secType byte
	if minor == 3 {
		t, err := s.readU32()
		if err != nil {
			return err
		}
		if t == 0 {
			return s.protoErr("el servidor rechazo la conexion: %s", s.readReason())
		}
		if t > 255 {
			return s.protoErr("tipo de seguridad %d desconocido", t)
		}
		secType = byte(t)
		if !allowed(opt.Auth, secType) {
			return s.protoErr("el servidor exige seguridad tipo %d y la superficie pide %q", secType, opt.Auth)
		}
	} else {
		nb, err := s.readFull(1)
		if err != nil {
			return err
		}
		if nb[0] == 0 {
			return s.protoErr("el servidor rechazo la conexion: %s", s.readReason())
		}
		types, err := s.readFull(int(nb[0]))
		if err != nil {
			return err
		}
		secType = pickSecurity(types, opt)
		if secType == 0 {
			return s.protoErr("ningun tipo de seguridad compatible (ofrecidos %v, superficie pide %q)", types, opt.Auth)
		}
		if err := s.write([]byte{secType}); err != nil {
			return err
		}
	}

	if secType == 2 {
		ch, err := s.readFull(16)
		if err != nil {
			return err
		}
		resp, err := VNCAuthResponse(ch, opt.Password)
		if err != nil {
			return err
		}
		if err := s.write(resp); err != nil {
			return err
		}
	}
	if secType == 2 || minor == 8 {
		res, err := s.readU32()
		if err != nil {
			return err
		}
		if res != 0 {
			reason := ""
			if minor == 8 {
				reason = s.readReason()
			}
			if reason == "" {
				reason = "sin razon del servidor"
			}
			return fmt.Errorf("%w: %s", ErrAuth, reason)
		}
	}

	shared := byte(0)
	if opt.Shared {
		shared = 1
	}
	if err := s.write([]byte{shared}); err != nil {
		return err
	}
	hdr, err := s.readFull(24)
	if err != nil {
		return err
	}
	s.width = int(binary.BigEndian.Uint16(hdr[0:2]))
	s.height = int(binary.BigEndian.Uint16(hdr[2:4]))
	if s.width == 0 || s.height == 0 {
		return s.protoErr("ServerInit con tamano %dx%d", s.width, s.height)
	}
	nameLen := binary.BigEndian.Uint32(hdr[20:24])
	if nameLen > 1<<16 {
		return s.protoErr("nombre de escritorio demasiado largo (%d)", nameLen)
	}
	name, err := s.readFull(int(nameLen))
	if err != nil {
		return err
	}
	s.name = string(name)
	s.fb = image.NewRGBA(image.Rect(0, 0, s.width, s.height))
	return nil
}

func allowed(auth string, t byte) bool {
	switch auth {
	case "vnc":
		return t == 2
	case "none":
		return t == 1
	}
	return t == 1 || t == 2
}

func pickSecurity(types []byte, opt Options) byte {
	has := func(t byte) bool {
		for _, x := range types {
			if x == t {
				return true
			}
		}
		return false
	}
	switch opt.Auth {
	case "vnc":
		if has(2) {
			return 2
		}
	case "none":
		if has(1) {
			return 1
		}
	default:
		if has(2) {
			return 2
		}
		if has(1) {
			return 1
		}
	}
	return 0
}

// Formato de pixel que pide el cliente: 32 bpp, profundidad 24, little-endian,
// true color, R en el byte 0, G en el 1 y B en el 2. Asi un rectangulo Raw se
// copia tal cual a image.RGBA (solo hay que fijar alfa).
var pixelFormat = []byte{32, 24, 0, 1, 0, 255, 0, 255, 0, 255, 0, 8, 16, 0, 0, 0}

func (s *Session) setup(encs []int32) error {
	msg := append([]byte{0, 0, 0, 0}, pixelFormat...)
	if err := s.write(msg); err != nil {
		return err
	}
	se := make([]byte, 4+4*len(encs))
	se[0] = 2
	binary.BigEndian.PutUint16(se[2:4], uint16(len(encs)))
	for i, e := range encs {
		binary.BigEndian.PutUint32(se[4+4*i:], uint32(e))
	}
	if err := s.write(se); err != nil {
		return err
	}
	return s.requestUpdate(false)
}

func (s *Session) waitFirstFrame(ctx context.Context) error {
	s.mu.Lock()
	ch := s.notify
	s.mu.Unlock()
	select {
	case <-ch:
		return nil
	case <-s.done:
		return s.readErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Session) requestUpdate(incremental bool) error {
	b := make([]byte, 10)
	b[0] = 3
	if incremental {
		b[1] = 1
	}
	binary.BigEndian.PutUint16(b[6:8], uint16(s.width))
	binary.BigEndian.PutUint16(b[8:10], uint16(s.height))
	return s.write(b)
}

func (s *Session) readLoop() {
	err := s.serve()
	if s.closed.Load() || errors.Is(err, io.EOF) {
		err = ErrClosed
	}
	s.readErr = err
	close(s.done)
}

func (s *Session) serve() error {
	for {
		t, err := s.br.ReadByte()
		if err != nil {
			return err
		}
		switch t {
		case 0:
			if err := s.readUpdate(); err != nil {
				return err
			}
		case 1: // SetColourMapEntries: no aplica a true color, se descarta
			b, err := s.readFull(5)
			if err != nil {
				return err
			}
			n := int(binary.BigEndian.Uint16(b[3:5]))
			if _, err := s.readFull(6 * n); err != nil {
				return err
			}
		case 2: // Bell
		case 3: // ServerCutText: se descarta
			b, err := s.readFull(7)
			if err != nil {
				return err
			}
			n := binary.BigEndian.Uint32(b[3:7])
			if n > 1<<24 {
				return s.protoErr("ServerCutText de %d bytes", n)
			}
			if _, err := io.CopyN(io.Discard, s.br, int64(n)); err != nil {
				return err
			}
		default:
			return s.protoErr("mensaje del servidor desconocido: %d", t)
		}
	}
}

func (s *Session) readUpdate() error {
	b, err := s.readFull(3)
	if err != nil {
		return err
	}
	n := int(binary.BigEndian.Uint16(b[1:3]))
	for i := 0; i < n; i++ {
		h, err := s.readFull(12)
		if err != nil {
			return err
		}
		x := int(binary.BigEndian.Uint16(h[0:2]))
		y := int(binary.BigEndian.Uint16(h[2:4]))
		w := int(binary.BigEndian.Uint16(h[4:6]))
		ht := int(binary.BigEndian.Uint16(h[6:8]))
		enc := int32(binary.BigEndian.Uint32(h[8:12]))
		r := image.Rect(x, y, x+w, y+ht)
		if !r.In(s.fb.Rect) {
			return s.protoErr("rectangulo %v fuera del framebuffer %dx%d", r, s.width, s.height)
		}
		switch enc {
		case EncodingRaw:
			px, err := s.readFull(w * ht * 4)
			if err != nil {
				return err
			}
			s.mu.Lock()
			s.stats.RawRects++
			for row := 0; row < ht; row++ {
				dst := s.fb.Pix[s.fb.PixOffset(x, y+row):]
				src := px[row*w*4 : (row+1)*w*4]
				copy(dst, src)
				for k := 3; k < w*4; k += 4 {
					dst[k] = 255
				}
			}
			s.mu.Unlock()
		case EncodingCopyRect:
			if !s.encs[EncodingCopyRect] {
				return s.protoErr("el servidor envio CopyRect sin haberlo pedido")
			}
			sp, err := s.readFull(4)
			if err != nil {
				return err
			}
			sx := int(binary.BigEndian.Uint16(sp[0:2]))
			sy := int(binary.BigEndian.Uint16(sp[2:4]))
			src := image.Rect(sx, sy, sx+w, sy+ht)
			if !src.In(s.fb.Rect) {
				return s.protoErr("CopyRect desde %v fuera del framebuffer", src)
			}
			s.mu.Lock()
			s.stats.CopyRects++
			copyRect(s.fb, src, r.Min)
			s.mu.Unlock()
		default:
			return s.protoErr("encoding %d no pedido", enc)
		}
	}
	s.mu.Lock()
	s.seq++
	s.stats.Updates++
	close(s.notify)
	s.notify = make(chan struct{})
	s.mu.Unlock()
	return nil
}

// copyRect copia src a dst dentro de la misma imagen; tolera solapamiento.
func copyRect(img *image.RGBA, src image.Rectangle, dst image.Point) {
	w := src.Dx() * 4
	tmp := make([]byte, w*src.Dy())
	for row := 0; row < src.Dy(); row++ {
		o := img.PixOffset(src.Min.X, src.Min.Y+row)
		copy(tmp[row*w:], img.Pix[o:o+w])
	}
	for row := 0; row < src.Dy(); row++ {
		o := img.PixOffset(dst.X, dst.Y+row)
		copy(img.Pix[o:o+w], tmp[row*w:(row+1)*w])
	}
}

// Stats cuenta lo recibido del servidor desde la conexion.
type Stats struct {
	Updates   int // FramebufferUpdate recibidos
	RawRects  int // rectangulos Raw aplicados
	CopyRects int // rectangulos CopyRect aplicados
}

// Stats devuelve los contadores de la sesion.
func (s *Session) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stats
}

// Size devuelve el tamano anunciado en ServerInit.
func (s *Session) Size() (int, int) { return s.width, s.height }

// Name devuelve el nombre de escritorio de ServerInit.
func (s *Session) Name() string { return s.name }

// Snapshot devuelve una copia del framebuffer local sin pedir actualizaciones.
func (s *Session) Snapshot() *image.RGBA {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := image.NewRGBA(s.fb.Rect)
	copy(out.Pix, s.fb.Pix)
	return out
}

// Capture pide una actualizacion incremental, espera la respuesta hasta
// CaptureWait y devuelve una copia del framebuffer.
func (s *Session) Capture(ctx context.Context) (*image.RGBA, error) {
	s.mu.Lock()
	seq, ch := s.seq, s.notify
	s.mu.Unlock()
	if err := s.requestUpdate(true); err != nil {
		return nil, err
	}
	timer := time.NewTimer(s.capWait)
	defer timer.Stop()
wait:
	for {
		select {
		case <-ch:
			s.mu.Lock()
			got := s.seq > seq
			ch = s.notify
			s.mu.Unlock()
			if got {
				break wait
			}
		case <-timer.C:
			break wait
		case <-s.done:
			return nil, s.readErr
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return s.Snapshot(), nil
}

// Pointer envia un PointerEvent. buttons es la mascara RFB (bit 0 = boton 1).
func (s *Session) Pointer(x, y int, buttons uint8) error {
	if x < 0 || y < 0 || x >= s.width || y >= s.height {
		return fmt.Errorf("rfb: punto (%d,%d) fuera de la pantalla %dx%d", x, y, s.width, s.height)
	}
	b := make([]byte, 6)
	b[0] = 5
	b[1] = buttons
	binary.BigEndian.PutUint16(b[2:4], uint16(x))
	binary.BigEndian.PutUint16(b[4:6], uint16(y))
	return s.write(b)
}

// Key envia un KeyEvent con un keysym X11.
func (s *Session) Key(keysym uint32, down bool) error {
	b := make([]byte, 8)
	b[0] = 4
	if down {
		b[1] = 1
	}
	binary.BigEndian.PutUint32(b[4:8], keysym)
	return s.write(b)
}

// Err devuelve el error que cerro la sesion, o nil si sigue abierta.
func (s *Session) Err() error {
	select {
	case <-s.done:
		return s.readErr
	default:
		return nil
	}
}

// Close cierra el websocket. Los envios posteriores devuelven ErrClosed.
func (s *Session) Close() error {
	s.closeOne.Do(func() {
		s.closed.Store(true)
		s.conn.Close()
	})
	return nil
}
