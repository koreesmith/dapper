package api

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"testing"
)

func testJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 128, 255})
		}
	}
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestThumbnailKeepsTheShapeAndShrinks(t *testing.T) {
	for _, c := range []struct{ w, h, wantW, wantH int }{{300, 200, 72, 48}, {200, 300, 48, 72}, {500, 500, 72, 72}} {
		src := testJPEG(t, c.w, c.h)
		out, err := thumbnail(src, 72)
		if err != nil {
			t.Fatal(err)
		}
		cfg, err := jpeg.DecodeConfig(bytes.NewReader(out))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Width != c.wantW || cfg.Height != c.wantH {
			t.Errorf("%dx%d became %dx%d, want %dx%d", c.w, c.h, cfg.Width, cfg.Height, c.wantW, c.wantH)
		}
		if len(out) >= len(src) {
			t.Errorf("%dx%d thumbnail (%d bytes) isn't smaller than the original (%d)", c.w, c.h, len(out), len(src))
		}
	}
}

func TestThumbnailLeavesSmallPicturesAlone(t *testing.T) {
	src := testJPEG(t, 40, 30)
	out, err := thumbnail(src, 72)
	if err != nil || !bytes.Equal(out, src) {
		t.Errorf("a picture already small enough should come back unchanged (err %v)", err)
	}
	if _, err := thumbnail([]byte("not a jpeg"), 72); err == nil {
		t.Error("garbage should be an error")
	}
}
