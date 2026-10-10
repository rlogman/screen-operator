package screen

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"testing"
)

func patterned(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, color.RGBA{uint8(x / 10 * 7), uint8(y / 10 * 11), uint8((x/10 + y/10) * 5), 255})
		}
	}
	return img
}

func TestE2_3_S3_JPEGCalidad80(t *testing.T) {
	img := patterned(1280, 800)
	b, err := EncodeJPEG(img, 80)
	if err != nil {
		t.Fatal(err)
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil || format != "jpeg" {
		t.Fatalf("no es un JPEG valido: %v (%s)", err, format)
	}
	if cfg.Width != 1280 || cfg.Height != 800 {
		t.Fatalf("JPEG de %dx%d", cfg.Width, cfg.Height)
	}
	if _, err := jpeg.Decode(bytes.NewReader(b)); err != nil {
		t.Fatalf("decodificar: %v", err)
	}
	p := Perception{}
	p.Screenshot.Format, p.Screenshot.JPEGQuality = "jpeg", 80
	if _, mime, err := p.Encode(img); err != nil || mime != "image/jpeg" {
		t.Fatalf("Perception.Encode: %v %s", err, mime)
	}
}

func TestE2_3_S4_ZoomRegionFactor2(t *testing.T) {
	img := patterned(1280, 800)
	r := image.Rect(100, 100, 300, 200) // (100,100,200,100) en x,y,w,h
	z, err := Zoom(img, r, 2)
	if err != nil {
		t.Fatal(err)
	}
	if z.Bounds().Dx() != 400 || z.Bounds().Dy() != 200 {
		t.Fatalf("zoom de %v", z.Bounds())
	}
	for y := 0; y < 200; y++ {
		for x := 0; x < 400; x++ {
			if got, want := z.RGBAAt(x, y), img.RGBAAt(100+x/2, 100+y/2); got != want {
				t.Fatalf("zoom (%d,%d)=%v, region (%d,%d)=%v", x, y, got, 100+x/2, 100+y/2, want)
			}
		}
	}
	if _, err := Zoom(img, image.Rect(1200, 700, 1400, 900), 2); err == nil {
		t.Fatal("zoom fuera de la pantalla no dio error")
	}
}

func TestE2_3_S5_CapturaParaModeloYEscala(t *testing.T) {
	img := patterned(1280, 800)
	// Marca en (640,400) de pantalla para comprobar que la reduccion conserva contenido.
	draw.Draw(img, image.Rect(630, 390, 650, 410), &image.Uniform{color.RGBA{255, 0, 0, 255}}, image.Point{}, draw.Src)
	p := Perception{}
	p.Screenshot.MaxWidth = 640
	shot := ForModel(img, p.Screenshot.MaxWidth)
	if b := shot.Image.Bounds(); b.Dx() != 640 || b.Dy() != 400 {
		t.Fatalf("captura para el modelo de %dx%d", b.Dx(), b.Dy())
	}
	if x, y := shot.ToScreen(320, 200); x != 640 || y != 400 {
		t.Fatalf("ToScreen(320,200) = (%d,%d), se esperaba (640,400)", x, y)
	}
	if got := shot.Image.RGBAAt(320, 200); got != (color.RGBA{255, 0, 0, 255}) {
		t.Fatalf("pixel (320,200) de la imagen reducida = %v", got)
	}
	if x, y := shot.FromScreen(640, 400); x != 320 || y != 200 {
		t.Fatalf("FromScreen(640,400) = (%d,%d)", x, y)
	}
	if s := shot.Scale(); s != 2 {
		t.Fatalf("escala %v", s)
	}
}
