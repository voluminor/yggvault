package view

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"strings"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

// // // // // // // // // //

const (
	OGBannerWidth  = 1200
	OGBannerHeight = 630

	cMaxLogoEdge        = 2048
	cMaxBannerTextBytes = 2048

	// OGBannerGenVersion invalidates cached OG-banner ETags on any change in renderer output.
	OGBannerGenVersion = "2"
)

var (
	cInkObj        = color.RGBA{R: 0x00, G: 0x00, B: 0x00, A: 0xff}
	cLineObj       = color.RGBA{R: 0x56, G: 0x58, B: 0x58, A: 0xff}
	cGreenObj      = color.RGBA{R: 0x00, G: 0xff, B: 0x50, A: 0xff}
	cCyanObj       = color.RGBA{R: 0x00, G: 0xf0, B: 0xff, A: 0xff}
	cYellowObj     = color.RGBA{R: 0xff, G: 0xf2, B: 0x2e, A: 0xff}
	cRedObj        = color.RGBA{R: 0xf0, G: 0x28, B: 0x28, A: 0xff}
	cSteelObj      = color.RGBA{R: 0xa7, G: 0xa2, B: 0x9a, A: 0xff}
	cBannerTextObj = color.RGBA{R: 0x00, G: 0x00, B: 0x00, A: 0xff}
)

// // // // // // // // // //

func validateLogoEdge(edge int) error {
	if edge <= 0 {
		return errors.New("logo edge must be positive")
	}
	if edge > cMaxLogoEdge {
		return fmt.Errorf("logo edge exceeds maximum %d", cMaxLogoEdge)
	}
	return nil
}

func logoSupersample(edge int) int {
	if edge < 128 {
		return 4
	}
	return 2
}

func minFloat(a float64, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func maxFloat(a float64, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func encodePNG(imgObj image.Image) ([]byte, error) {
	var bufObj bytes.Buffer
	encoderObj := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := encoderObj.Encode(&bufObj, imgObj); err != nil {
		return nil, fmt.Errorf("encode png: %w", err)
	}
	return bufObj.Bytes(), nil
}

func drawCircle(imgObj *image.RGBA, cx float64, cy float64, r float64, colObj color.RGBA) {
	minX := max(0, int(math.Floor(cx-r)))
	maxX := min(imgObj.Bounds().Dx()-1, int(math.Ceil(cx+r)))
	minY := max(0, int(math.Floor(cy-r)))
	maxY := min(imgObj.Bounds().Dy()-1, int(math.Ceil(cy+r)))
	r2 := r * r
	for y := minY; y <= maxY; y++ {
		py := float64(y) + 0.5
		for x := minX; x <= maxX; x++ {
			px := float64(x) + 0.5
			dx := px - cx
			dy := py - cy
			if dx*dx+dy*dy <= r2 {
				imgObj.SetRGBA(x, y, colObj)
			}
		}
	}
}

func drawRoundLine(imgObj *image.RGBA, ax float64, ay float64, bx float64, by float64, width float64, colObj color.RGBA) {
	r := width / 2
	minX := max(0, int(math.Floor(minFloat(ax, bx)-r)))
	maxX := min(imgObj.Bounds().Dx()-1, int(math.Ceil(maxFloat(ax, bx)+r)))
	minY := max(0, int(math.Floor(minFloat(ay, by)-r)))
	maxY := min(imgObj.Bounds().Dy()-1, int(math.Ceil(maxFloat(ay, by)+r)))
	vx := bx - ax
	vy := by - ay
	len2 := vx*vx + vy*vy
	r2 := r * r
	for y := minY; y <= maxY; y++ {
		py := float64(y) + 0.5
		for x := minX; x <= maxX; x++ {
			px := float64(x) + 0.5
			t := ((px-ax)*vx + (py-ay)*vy) / len2
			if t < 0 {
				t = 0
			} else if t > 1 {
				t = 1
			}
			cx := ax + vx*t
			cy := ay + vy*t
			dx := px - cx
			dy := py - cy
			if dx*dx+dy*dy <= r2 {
				imgObj.SetRGBA(x, y, colObj)
			}
		}
	}
}

func drawButtLine(imgObj *image.RGBA, ax float64, ay float64, bx float64, by float64, width float64, colObj color.RGBA) {
	r := width / 2
	minX := max(0, int(math.Floor(minFloat(ax, bx)-r)))
	maxX := min(imgObj.Bounds().Dx()-1, int(math.Ceil(maxFloat(ax, bx)+r)))
	minY := max(0, int(math.Floor(minFloat(ay, by)-r)))
	maxY := min(imgObj.Bounds().Dy()-1, int(math.Ceil(maxFloat(ay, by)+r)))
	vx := bx - ax
	vy := by - ay
	len2 := vx*vx + vy*vy
	if len2 == 0 {
		return
	}
	r2 := r * r
	for y := minY; y <= maxY; y++ {
		py := float64(y) + 0.5
		for x := minX; x <= maxX; x++ {
			px := float64(x) + 0.5
			t := ((px-ax)*vx + (py-ay)*vy) / len2
			if t < 0 || t > 1 {
				continue
			}
			cx := ax + vx*t
			cy := ay + vy*t
			dx := px - cx
			dy := py - cy
			if dx*dx+dy*dy <= r2 {
				imgObj.SetRGBA(x, y, colObj)
			}
		}
	}
}

func drawDashedLine(imgObj *image.RGBA, ax float64, ay float64, bx float64, by float64, width float64, dash float64, gap float64, colObj color.RGBA) {
	vx := bx - ax
	vy := by - ay
	length := math.Hypot(vx, vy)
	if length == 0 || dash <= 0 {
		return
	}
	ux := vx / length
	uy := vy / length
	step := dash + maxFloat(gap, 0)
	for pos := 0.0; pos < length; pos += step {
		end := minFloat(pos+dash, length)
		drawButtLine(imgObj, ax+ux*pos, ay+uy*pos, ax+ux*end, ay+uy*end, width, colObj)
	}
}

func downsampleRGBA(srcObj *image.RGBA, edge int, sample int) *image.RGBA {
	dstObj := image.NewRGBA(image.Rect(0, 0, edge, edge))
	for y := 0; y < edge; y++ {
		for x := 0; x < edge; x++ {
			var r, g, b, a uint32
			for sy := 0; sy < sample; sy++ {
				for sx := 0; sx < sample; sx++ {
					off := srcObj.PixOffset(x*sample+sx, y*sample+sy)
					r += uint32(srcObj.Pix[off])
					g += uint32(srcObj.Pix[off+1])
					b += uint32(srcObj.Pix[off+2])
					a += uint32(srcObj.Pix[off+3])
				}
			}
			div := uint32(sample * sample)
			dstObj.SetRGBA(x, y, color.RGBA{
				R: uint8(r / div),
				G: uint8(g / div),
				B: uint8(b / div),
				A: uint8(a / div),
			})
		}
	}
	return dstObj
}

func renderLogo(edge int) (*image.RGBA, error) {
	if err := validateLogoEdge(edge); err != nil {
		return nil, err
	}
	sample := logoSupersample(edge)
	hiEdge := edge * sample
	imgObj := image.NewRGBA(image.Rect(0, 0, hiEdge, hiEdge))
	scale := float64(hiEdge) / 32.0
	pt := func(v float64) float64 { return v * scale }

	drawRoundLine(imgObj, pt(4), pt(16), pt(16), pt(16), pt(2.6), cInkObj)
	drawDashedLine(imgObj, pt(16), pt(16), pt(24), pt(6), pt(2.6), pt(2.2), pt(1.6), cInkObj)
	drawRoundLine(imgObj, pt(16), pt(16), pt(24), pt(26), pt(2.6), cInkObj)
	drawRoundLine(imgObj, pt(16), pt(16), pt(28), pt(16), pt(2.6), cLineObj)

	nodeArr := []struct {
		x, y    float64
		ringObj color.RGBA
		fillObj color.RGBA
	}{
		{4, 16, cInkObj, cGreenObj},
		{16, 16, cInkObj, cCyanObj},
		{24, 6, cInkObj, cYellowObj},
		{24, 26, cInkObj, cCyanObj},
		{28, 16, cLineObj, cRedObj},
	}
	for _, nodeObj := range nodeArr {
		drawCircle(imgObj, pt(nodeObj.x), pt(nodeObj.y), pt(3.9), nodeObj.ringObj)
		drawCircle(imgObj, pt(nodeObj.x), pt(nodeObj.y), pt(2.25), nodeObj.fillObj)
	}

	return downsampleRGBA(imgObj, edge, sample), nil
}

func writeICOUint16(bufObj *bytes.Buffer, value uint16) {
	_ = binary.Write(bufObj, binary.LittleEndian, value)
}

func writeICOUint32(bufObj *bytes.Buffer, value uint32) {
	_ = binary.Write(bufObj, binary.LittleEndian, value)
}

func appendICOEntry(bufObj *bytes.Buffer, edge int, bodyArr []byte, offset int) {
	if edge == 256 {
		bufObj.WriteByte(0)
	} else {
		bufObj.WriteByte(byte(edge))
	}
	if edge == 256 {
		bufObj.WriteByte(0)
	} else {
		bufObj.WriteByte(byte(edge))
	}
	bufObj.WriteByte(0)
	bufObj.WriteByte(0)
	writeICOUint16(bufObj, 1)
	writeICOUint16(bufObj, 32)
	writeICOUint32(bufObj, uint32(len(bodyArr)))
	writeICOUint32(bufObj, uint32(offset))
}

func cleanBannerText(text string) string {
	if len(text) > cMaxBannerTextBytes {
		text = text[:cMaxBannerTextBytes]
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	var builderObj strings.Builder
	builderObj.Grow(len(text))
	lastSpace := false
	for _, r := range text {
		switch r {
		case '\n', '\r', '\t':
			r = ' '
		case '·', '•', '–', '—':
			r = '-'
		case '…':
			builderObj.WriteString("..")
			r = '.'
		}
		if r < 32 || r > 126 {
			continue
		}
		if r == ' ' {
			if lastSpace {
				continue
			}
			lastSpace = true
		} else {
			lastSpace = false
		}
		builderObj.WriteRune(r)
	}
	return strings.TrimSpace(builderObj.String())
}

func textWidth(text string, scale int) int {
	return font.MeasureString(basicfont.Face7x13, text).Ceil() * scale
}

func fitBannerText(text string, maxWidth int, scale int, minScale int) (string, int) {
	text = cleanBannerText(text)
	for scale > minScale && textWidth(text, scale) > maxWidth {
		scale--
	}
	if textWidth(text, scale) <= maxWidth {
		return text, scale
	}
	const suffix = "..."
	for len(text) > len(suffix) && textWidth(text+suffix, scale) > maxWidth {
		text = text[:len(text)-1]
	}
	if len(text) <= len(suffix) {
		return suffix, scale
	}
	return text + suffix, scale
}

func drawScaledText(imgObj *image.RGBA, x int, y int, scale int, text string, colObj color.RGBA) {
	if text == "" || scale <= 0 {
		return
	}
	faceObj := basicfont.Face7x13
	width := font.MeasureString(faceObj, text).Ceil()
	metricsObj := faceObj.Metrics()
	height := metricsObj.Height.Ceil()
	ascent := metricsObj.Ascent.Ceil()
	maskObj := image.NewAlpha(image.Rect(0, 0, width, height))
	drawerObj := font.Drawer{
		Dst:  maskObj,
		Src:  image.NewUniform(color.Alpha{A: 255}),
		Face: faceObj,
		Dot:  fixed.P(0, ascent),
	}
	drawerObj.DrawString(text)
	for py := 0; py < height; py++ {
		for px := 0; px < width; px++ {
			alpha := maskObj.AlphaAt(px, py).A
			if alpha == 0 {
				continue
			}
			outColObj := colObj
			outColObj.A = uint8((uint16(outColObj.A) * uint16(alpha)) / 255)
			for sy := 0; sy < scale; sy++ {
				for sx := 0; sx < scale; sx++ {
					tx := x + px*scale + sx
					ty := y + py*scale + sy
					if image.Pt(tx, ty).In(imgObj.Bounds()) {
						imgObj.SetRGBA(tx, ty, outColObj)
					}
				}
			}
		}
	}
}

func drawCenteredText(imgObj *image.RGBA, y int, maxWidth int, scale int, minScale int, text string, colObj color.RGBA) {
	fitText, fitScale := fitBannerText(text, maxWidth, scale, minScale)
	x := (imgObj.Bounds().Dx() - textWidth(fitText, fitScale)) / 2
	drawScaledText(imgObj, x, y, fitScale, fitText, colObj)
}

// // // // // // // // // //

// IconICO assembles the favicon from several PNG layers without SVG rendering.
func IconICO() ([]byte, error) {
	sizeArr := []int{16, 32, 48}
	bodyArr := make([][]byte, len(sizeArr))
	for i, edge := range sizeArr {
		imgObj, err := renderLogo(edge)
		if err != nil {
			return nil, err
		}
		bodyArr[i], err = encodePNG(imgObj)
		if err != nil {
			return nil, err
		}
	}

	headerLen := 6 + len(sizeArr)*16
	offset := headerLen
	var bufObj bytes.Buffer
	bufObj.Grow(headerLen + len(bodyArr[0]) + len(bodyArr[1]) + len(bodyArr[2]))
	writeICOUint16(&bufObj, 0)
	writeICOUint16(&bufObj, 1)
	writeICOUint16(&bufObj, uint16(len(sizeArr)))
	for i, edge := range sizeArr {
		appendICOEntry(&bufObj, edge, bodyArr[i], offset)
		offset += len(bodyArr[i])
	}
	for _, body := range bodyArr {
		bufObj.Write(body)
	}
	return bufObj.Bytes(), nil
}

// LogoPNG returns a square PNG with a transparent background.
func LogoPNG(edge int) ([]byte, error) {
	imgObj, err := renderLogo(edge)
	if err != nil {
		return nil, err
	}
	return encodePNG(imgObj)
}

func ogBanner(headline string, subLine string, bottomText string) ([]byte, error) {
	imgObj := image.NewRGBA(image.Rect(0, 0, OGBannerWidth, OGBannerHeight))
	draw.Draw(imgObj, imgObj.Bounds(), image.NewUniform(cSteelObj), image.Point{}, draw.Src)

	logoObj, err := renderLogo(260)
	if err != nil {
		return nil, err
	}
	draw.Draw(imgObj, image.Rect(92, 142, 352, 402), logoObj, image.Point{}, draw.Over)

	drawRoundLine(imgObj, 410, 170, 410, 430, 3, cLineObj)
	drawScaledText(imgObj, 470, 176, 4, "yggvault", cBannerTextObj)

	headlineText, headlineScale := fitBannerText(headline, 620, 7, 3)
	drawScaledText(imgObj, 470, 252, headlineScale, headlineText, cBannerTextObj)

	if subLine != "" {
		subText, subScale := fitBannerText(subLine, 700, 4, 2)
		drawScaledText(imgObj, 470, 360, subScale, subText, cBannerTextObj)
	}

	drawCenteredText(imgObj, 536, 960, 3, 2, bottomText, cBannerTextObj)
	return encodePNG(imgObj)
}

func overlaySuffix(overlayArr []string) string {
	if len(overlayArr) == 0 {
		return ""
	}
	return " - " + strings.Join(overlayArr, ", ")
}

// OGNodeBannerPNG returns the node banner: counters large, the tagline as sub-line, date/domain at the bottom.
func OGNodeBannerPNG(moduleCount int, versionCount uint64, tagline string, bottomText string) ([]byte, error) {
	headline := fmt.Sprintf("%d modules - %d versions", moduleCount, versionCount)
	return ogBanner(headline, tagline, bottomText)
}

// OGKeyBannerPNG returns the module banner: the key large, latest/count/overlays sub-line, date/domain at the bottom.
func OGKeyBannerPNG(key string, latest string, versionCount uint64, overlayArr []string, bottomText string) ([]byte, error) {
	subLine := fmt.Sprintf("latest %s - %d versions%s", latest, versionCount, overlaySuffix(overlayArr))
	return ogBanner(key, subLine, bottomText)
}

// OGVersionBannerPNG returns the version banner: the key large, version/size/overlays sub-line, date/domain at the bottom.
func OGVersionBannerPNG(key string, version string, sizeBytes uint64, overlayArr []string, bottomText string) ([]byte, error) {
	subLine := version + " - " + humanBytes(sizeBytes) + overlaySuffix(overlayArr)
	return ogBanner(key, subLine, bottomText)
}
