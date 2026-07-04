package view

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// // // // // // // // // //

var mediaBenchSinkArr []byte

type icoEntryObj struct {
	width  int
	height int
	body   []byte
}

// // // // // // // // // //

func writeTempAsset(t *testing.T, dir string, name string, body []byte) {
	t.Helper()
	if len(body) == 0 {
		t.Fatalf("%s is empty", name)
	}
	if err := os.WriteFile(filepath.Join(dir, name), body, 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func decodePNGAsset(t *testing.T, body []byte) image.Image {
	t.Helper()
	imgObj, err := png.Decode(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("decode png: %v", err)
	}
	return imgObj
}

func assertImageSize(t *testing.T, imgObj image.Image, width int, height int) {
	t.Helper()
	boundsObj := imgObj.Bounds()
	if boundsObj.Dx() != width || boundsObj.Dy() != height {
		t.Fatalf("image size = %dx%d, want %dx%d", boundsObj.Dx(), boundsObj.Dy(), width, height)
	}
}

func hasVisiblePixel(imgObj image.Image) bool {
	boundsObj := imgObj.Bounds()
	for y := boundsObj.Min.Y; y < boundsObj.Max.Y; y++ {
		for x := boundsObj.Min.X; x < boundsObj.Max.X; x++ {
			_, _, _, a := imgObj.At(x, y).RGBA()
			if a != 0 {
				return true
			}
		}
	}
	return false
}

func hasPixelDifferentFrom(imgObj image.Image, bgObj color.RGBA) bool {
	boundsObj := imgObj.Bounds()
	br := uint32(bgObj.R) * 257
	bg := uint32(bgObj.G) * 257
	bb := uint32(bgObj.B) * 257
	ba := uint32(bgObj.A) * 257
	for y := boundsObj.Min.Y; y < boundsObj.Max.Y; y++ {
		for x := boundsObj.Min.X; x < boundsObj.Max.X; x++ {
			r, g, b, a := imgObj.At(x, y).RGBA()
			if r != br || g != bg || b != bb || a != ba {
				return true
			}
		}
	}
	return false
}

func parseICO(t *testing.T, body []byte) []icoEntryObj {
	t.Helper()
	if len(body) < 6 {
		t.Fatal("ico body is too small")
	}
	if binary.LittleEndian.Uint16(body[0:2]) != 0 || binary.LittleEndian.Uint16(body[2:4]) != 1 {
		t.Fatal("ico header is invalid")
	}
	count := int(binary.LittleEndian.Uint16(body[4:6]))
	if count == 0 {
		t.Fatal("ico has no images")
	}
	headerLen := 6 + count*16
	if len(body) < headerLen {
		t.Fatal("ico entries are truncated")
	}
	entryArr := make([]icoEntryObj, 0, count)
	for i := 0; i < count; i++ {
		pos := 6 + i*16
		width := int(body[pos])
		height := int(body[pos+1])
		if width == 0 {
			width = 256
		}
		if height == 0 {
			height = 256
		}
		size := int(binary.LittleEndian.Uint32(body[pos+8 : pos+12]))
		offset := int(binary.LittleEndian.Uint32(body[pos+12 : pos+16]))
		if size <= 0 || offset < headerLen || offset+size > len(body) {
			t.Fatalf("ico entry %d range is invalid", i)
		}
		entryArr = append(entryArr, icoEntryObj{
			width:  width,
			height: height,
			body:   body[offset : offset+size],
		})
	}
	return entryArr
}

// // // // // // // // // //

func TestIconICOGeneratesExpectedImages(t *testing.T) {
	dir := t.TempDir()
	body, err := IconICO()
	if err != nil {
		t.Fatalf("IconICO: %v", err)
	}
	writeTempAsset(t, dir, "favicon.ico", body)

	entryArr := parseICO(t, body)
	wantArr := []int{16, 32, 48}
	if len(entryArr) != len(wantArr) {
		t.Fatalf("ico image count = %d, want %d", len(entryArr), len(wantArr))
	}
	for i, want := range wantArr {
		entryObj := entryArr[i]
		if entryObj.width != want || entryObj.height != want {
			t.Fatalf("ico entry %d size = %dx%d, want %dx%d", i, entryObj.width, entryObj.height, want, want)
		}
		imgObj := decodePNGAsset(t, entryObj.body)
		assertImageSize(t, imgObj, want, want)
		if !hasVisiblePixel(imgObj) {
			t.Fatalf("ico entry %d has no visible pixels", i)
		}
	}
}

func TestLogoPNGGeneratesExpectedImage(t *testing.T) {
	dir := t.TempDir()
	body, err := LogoPNG(512)
	if err != nil {
		t.Fatalf("LogoPNG: %v", err)
	}
	writeTempAsset(t, dir, "logo-512.png", body)

	imgObj := decodePNGAsset(t, body)
	assertImageSize(t, imgObj, 512, 512)
	if !hasVisiblePixel(imgObj) {
		t.Fatal("logo has no visible pixels")
	}
}

func TestLogoPNGRejectsInvalidSize(t *testing.T) {
	if _, err := LogoPNG(0); err == nil {
		t.Fatal("LogoPNG accepted zero size")
	}
}

func TestOGBannerPNGGeneratesExpectedImage(t *testing.T) {
	dir := t.TempDir()
	body, err := OGVersionBannerPNG("example.com/module/path", "v1.2.3", 1372462, []string{"go"}, "updated 2026-07-02 - vault.test")
	if err != nil {
		t.Fatalf("OGVersionBannerPNG: %v", err)
	}
	writeTempAsset(t, dir, "og-version.png", body)

	imgObj := decodePNGAsset(t, body)
	assertImageSize(t, imgObj, OGBannerWidth, OGBannerHeight)
	if !hasPixelDifferentFrom(imgObj, cSteelObj) {
		t.Fatal("og banner contains only the background color")
	}
}

// Survivability guard: extreme strings must not break generation, only get trimmed.
func TestOGBannerPNGSurvivesHostileText(t *testing.T) {
	longText := strings.Repeat("very-long-segment/", 400)
	for _, caseObj := range []struct{ key, latest, bottom string }{
		{longText, longText, longText},
		{"clé-sans-ascii - seulement · unicode…", "version · build", "footer · rows"},
		{"", "", ""},
		{"a", strings.Repeat("\n\t\r", 100), strings.Repeat("·", 3000)},
	} {
		body, err := OGKeyBannerPNG(caseObj.key, caseObj.latest, 3, []string{"go", "composer"}, caseObj.bottom)
		if err != nil {
			t.Fatalf("OGKeyBannerPNG(%q...): %v", caseObj.key[:min(20, len(caseObj.key))], err)
		}
		imgObj := decodePNGAsset(t, body)
		assertImageSize(t, imgObj, OGBannerWidth, OGBannerHeight)
	}
}

// Middot and Unicode separators must not turn into question marks on the banner.
func TestCleanBannerTextASCII(t *testing.T) {
	got := cleanBannerText("latest v0.1.0 · 3 versions - test…")
	if strings.ContainsAny(got, "?·—…") {
		t.Fatalf("clean text must be ascii without question marks, got %q", got)
	}
	if got != "latest v0.1.0 - 3 versions - test..." {
		t.Fatalf("unexpected normalization: %q", got)
	}
}

func BenchmarkIconICO(b *testing.B) {
	b.ReportAllocs()
	body, err := IconICO()
	if err != nil {
		b.Fatalf("IconICO: %v", err)
	}
	b.SetBytes(int64(len(body)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		body, err = IconICO()
		if err != nil {
			b.Fatalf("IconICO: %v", err)
		}
	}
	mediaBenchSinkArr = body
}

func BenchmarkLogoPNG512(b *testing.B) {
	b.ReportAllocs()
	body, err := LogoPNG(512)
	if err != nil {
		b.Fatalf("LogoPNG: %v", err)
	}
	b.SetBytes(int64(len(body)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		body, err = LogoPNG(512)
		if err != nil {
			b.Fatalf("LogoPNG: %v", err)
		}
	}
	mediaBenchSinkArr = body
}

func BenchmarkOGBannerPNG(b *testing.B) {
	b.ReportAllocs()
	body, err := OGVersionBannerPNG("example.com/module/path", "v1.2.3", 1372462, []string{"go"}, "updated 2026-07-02 - vault.test")
	if err != nil {
		b.Fatalf("OGVersionBannerPNG: %v", err)
	}
	b.SetBytes(int64(len(body)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		body, err = OGVersionBannerPNG("example.com/module/path", "v1.2.3", 1372462, []string{"go"}, "updated 2026-07-02 - vault.test")
		if err != nil {
			b.Fatalf("OGVersionBannerPNG: %v", err)
		}
	}
	mediaBenchSinkArr = body
}
