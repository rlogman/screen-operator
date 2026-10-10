package screen

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"math"
)

// EncodeJPEG codifica img como JPEG con la calidad dada (1..100).
func EncodeJPEG(img image.Image, quality int) ([]byte, error) {
	if quality < 1 || quality > 100 {
		return nil, fmt.Errorf("screen: calidad JPEG %d fuera de 1..100", quality)
	}
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// EncodePNG codifica img como PNG.
func EncodePNG(img image.Image) ([]byte, error) {
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// Encode codifica segun perception.screenshot y devuelve el tipo MIME.
func (p Perception) Encode(img image.Image) ([]byte, string, error) {
	switch p.Screenshot.Format {
	case "png":
		b, err := EncodePNG(img)
		return b, "image/png", err
	case "jpeg", "":
		q := p.Screenshot.JPEGQuality
		if q == 0 {
			q = 80
		}
		b, err := EncodeJPEG(img, q)
		return b, "image/jpeg", err
	}
	return nil, "", fmt.Errorf("screen: formato de captura %q no soportado", p.Screenshot.Format)
}

// ModelShot es la captura que ve el modelo: posiblemente reducida, con la
// escala para volver a coordenadas de pantalla.
type ModelShot struct {
	Image   *image.RGBA
	ScreenW int
	ScreenH int
}

// ForModel reduce la captura de pantalla a maxWidth de ancho conservando la
// proporcion. Si ya cabe, la devuelve tal cual.
func ForModel(screen *image.RGBA, maxWidth int) ModelShot {
	b := screen.Bounds()
	out := ModelShot{Image: screen, ScreenW: b.Dx(), ScreenH: b.Dy()}
	if maxWidth <= 0 || b.Dx() <= maxWidth {
		return out
	}
	h := int(math.Round(float64(b.Dy()) * float64(maxWidth) / float64(b.Dx())))
	out.Image = Resize(screen, maxWidth, h)
	return out
}

// Scale es pantalla/imagen en X (1 si no hubo reduccion).
func (m ModelShot) Scale() float64 {
	return float64(m.ScreenW) / float64(m.Image.Bounds().Dx())
}

// ToScreen convierte un punto de la imagen del modelo a coordenadas de pantalla,
// recortado a los limites de la pantalla.
func (m ModelShot) ToScreen(x, y int) (int, int) {
	iw, ih := m.Image.Bounds().Dx(), m.Image.Bounds().Dy()
	sx := int(math.Round(float64(x) * float64(m.ScreenW) / float64(iw)))
	sy := int(math.Round(float64(y) * float64(m.ScreenH) / float64(ih)))
	return clamp(sx, 0, m.ScreenW-1), clamp(sy, 0, m.ScreenH-1)
}

// FromScreen convierte un punto de pantalla a coordenadas de la imagen del modelo.
func (m ModelShot) FromScreen(x, y int) (int, int) {
	iw, ih := m.Image.Bounds().Dx(), m.Image.Bounds().Dy()
	ix := int(math.Round(float64(x) * float64(iw) / float64(m.ScreenW)))
	iy := int(math.Round(float64(y) * float64(ih) / float64(m.ScreenH)))
	return clamp(ix, 0, iw-1), clamp(iy, 0, ih-1)
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// Zoom recorta la region r (coordenadas de pantalla) y la amplia por factor.
// El resultado mide round(w*factor) x round(h*factor).
func Zoom(src *image.RGBA, r image.Rectangle, factor float64) (*image.RGBA, error) {
	if factor < 1 {
		return nil, fmt.Errorf("screen: factor de zoom %.2f menor que 1", factor)
	}
	r = r.Canon()
	if !r.In(src.Bounds()) || r.Empty() {
		return nil, fmt.Errorf("screen: region de zoom %v fuera de la pantalla %v", r, src.Bounds())
	}
	crop := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	for y := 0; y < r.Dy(); y++ {
		o := src.PixOffset(r.Min.X, r.Min.Y+y)
		copy(crop.Pix[y*crop.Stride:], src.Pix[o:o+r.Dx()*4])
	}
	w := int(math.Round(float64(r.Dx()) * factor))
	h := int(math.Round(float64(r.Dy()) * factor))
	return Resize(crop, w, h), nil
}

// Resize escala src a w x h. Al reducir promedia el area de origen de cada
// pixel (mantiene legible el texto); al ampliar usa vecino mas cercano (bordes
// nitidos, sin inventar colores).
func Resize(src *image.RGBA, w, h int) *image.RGBA {
	b := src.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	sw, sh := b.Dx(), b.Dy()
	if w >= sw && h >= sh {
		for y := 0; y < h; y++ {
			sy := b.Min.Y + y*sh/h
			for x := 0; x < w; x++ {
				sx := b.Min.X + x*sw/w
				so := src.PixOffset(sx, sy)
				do := dst.PixOffset(x, y)
				copy(dst.Pix[do:do+4], src.Pix[so:so+4])
			}
		}
		return dst
	}
	for y := 0; y < h; y++ {
		y0, y1 := y*sh/h, (y+1)*sh/h
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for x := 0; x < w; x++ {
			x0, x1 := x*sw/w, (x+1)*sw/w
			if x1 <= x0 {
				x1 = x0 + 1
			}
			var r, g, bl, a, n uint32
			for yy := y0; yy < y1; yy++ {
				o := src.PixOffset(b.Min.X+x0, b.Min.Y+yy)
				for xx := x0; xx < x1; xx++ {
					r += uint32(src.Pix[o])
					g += uint32(src.Pix[o+1])
					bl += uint32(src.Pix[o+2])
					a += uint32(src.Pix[o+3])
					o += 4
					n++
				}
			}
			do := dst.PixOffset(x, y)
			dst.Pix[do] = uint8(r / n)
			dst.Pix[do+1] = uint8(g / n)
			dst.Pix[do+2] = uint8(bl / n)
			dst.Pix[do+3] = uint8(a / n)
		}
	}
	return dst
}
