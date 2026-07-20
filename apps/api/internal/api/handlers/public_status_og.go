package handlers

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"net/http"
	"strings"
	"time"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
)

// Live-rendered Open Graph card (1200×630) for the public status page: the
// same dark card as the HTML — name, online/offline, availability numbers,
// and the 90-day strip — drawn with pure-Go font rendering so cross-compiling
// stays cgo-free. Crawlers fetch this once per unfurl, so it shows status as
// of the moment a link is posted.

const ogW, ogH = 1200, 630

var (
	ogFontRegular = mustParseFont(goregular.TTF)
	ogFontBold    = mustParseFont(gobold.TTF)

	ogBG      = hexRGBA("#101010")
	ogCard    = hexRGBA("#181818")
	ogBorder  = hexRGBA("#2a2a2a")
	ogText    = hexRGBA("#f0f0f0")
	ogMuted   = hexRGBA("#8a8a8a")
	ogFaint   = hexRGBA("#666666")
	ogUpDot   = hexRGBA("#22c55e")
	ogUpText  = hexRGBA("#4ade80")
	ogDnDot   = hexRGBA("#ef4444")
	ogDnText  = hexRGBA("#f87171")
)

func mustParseFont(ttf []byte) *sfnt.Font {
	f, err := opentype.Parse(ttf)
	if err != nil {
		panic(err)
	}
	return f
}

func hexRGBA(s string) color.RGBA {
	var r, g, b uint8
	fmt.Sscanf(s, "#%02x%02x%02x", &r, &g, &b)
	return color.RGBA{R: r, G: g, B: b, A: 255}
}

// serveOGImage renders (or serves the cached) preview card for a slug.
func (h *PublicStatusHandlers) serveOGImage(w http.ResponseWriter, r *http.Request, slug string) {
	v := h.view(r.Context(), slug)
	if v == nil {
		http.NotFound(w, r)
		return
	}
	h.mu.Lock()
	e, ok := h.ogCache[slug]
	h.mu.Unlock()
	if !ok || !time.Now().Before(e.expires) {
		e = ogCacheEntry{png: renderStatusCard(v, time.Now().Unix()), expires: time.Now().Add(publicCacheTTL)}
		h.mu.Lock()
		h.ogCache[slug] = e
		h.mu.Unlock()
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=30")
	w.Write(e.png)
}

func renderStatusCard(v *publicStatusView, now int64) []byte {
	// Faces are created per render: sfnt.Font is read-only, but opentype
	// faces carry scratch buffers and are not safe for concurrent draws.
	faceName := mustFace(ogFontBold, 56)
	faceStatus := mustFace(ogFontBold, 36)
	faceSub := mustFace(ogFontRegular, 28)
	faceStat := mustFace(ogFontBold, 46)
	faceSmall := mustFace(ogFontRegular, 20)

	img := image.NewRGBA(image.Rect(0, 0, ogW, ogH))
	draw.Draw(img, img.Bounds(), image.NewUniform(ogBG), image.Point{}, draw.Src)
	fillRoundedRect(img, 40, 40, ogW-40, ogH-40, 20, ogBorder)
	fillRoundedRect(img, 42, 42, ogW-42, ogH-42, 18, ogCard)

	const left, right = 96.0, ogW - 96.0

	// Status pill, top right: glow + dot + Online/Offline.
	statusTxt, dotC, txtC := "Offline", ogDnDot, ogDnText
	if v.Online {
		statusTxt, dotC, txtC = "Online", ogUpDot, ogUpText
	}
	statusW := stringWidth(faceStatus, statusTxt)
	statusX := right - statusW
	drawString(img, faceStatus, statusX, 144, txtC, statusTxt)
	dotCX, dotCY := statusX-30, 131.0
	if v.Online {
		glowCircle(img, dotCX, dotCY, 13, 34, ogUpDot)
	}
	fillCircle(img, dotCX, dotCY, 13, dotC)

	// Name, truncated to keep clear of the status pill.
	name := truncateToWidth(faceName, v.Name, statusX-60-left)
	drawString(img, faceName, left, 152, ogText, name)

	// Secondary line: version, players, how long up/down.
	var sub []string
	if v.MCVersion != "" {
		sub = append(sub, "Minecraft "+v.MCVersion)
	}
	if v.Online {
		if v.Players != nil {
			if *v.Players == 1 {
				sub = append(sub, "1 player online")
			} else {
				sub = append(sub, fmt.Sprintf("%d players online", *v.Players))
			}
		}
		if v.OnlineSince > 0 {
			sub = append(sub, "up "+fmtDurShort(now-v.OnlineSince))
		}
	} else if v.OfflineSince > 0 {
		sub = append(sub, "down for "+fmtDurShort(now-v.OfflineSince))
	}
	if len(sub) > 0 {
		drawString(img, faceSub, left, 204, ogMuted, strings.Join(sub, " · "))
	}

	// Availability columns.
	cols := []struct {
		val, label string
	}{
		{pctText(v.Avail24h), "24H UPTIME"},
		{pctText(v.Avail7d), "7D UPTIME"},
		{pctText(v.Avail90d), "90D UPTIME"},
	}
	for i, c := range cols {
		x := left + float64(i)*336
		drawString(img, faceStat, x, 330, ogText, c.val)
		drawString(img, faceSmall, x, 366, ogMuted, c.label)
	}

	// 90-day strip, newest on the right; short histories pad with no-data.
	days := v.Days
	if len(days) > publicDays {
		days = days[len(days)-publicDays:]
	}
	pad := publicDays - len(days)
	const barTop, barBot = 412.0, 488.0
	span := right - left
	barW := (span - float64(publicDays-1)*4) / publicDays
	for i := 0; i < publicDays; i++ {
		d := publicDay{}
		if i >= pad {
			d = days[i-pad]
		}
		x := left + float64(i)*(barW+4)
		fillRoundedRect(img, x, barTop, x+barW, barBot, 2, hexRGBA(barColorHex(d)))
	}
	drawString(img, faceSmall, left, 526, ogFaint, "90 days ago")
	today := "today"
	drawString(img, faceSmall, right-stringWidth(faceSmall, today), 526, ogFaint, today)

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil
	}
	return buf.Bytes()
}

func pctText(p *float64) string {
	if p == nil {
		return "—"
	}
	return fmtPct(*p)
}

// ── drawing primitives ───────────────────────────────────────────────────────

func mustFace(f *sfnt.Font, size float64) font.Face {
	face, err := opentype.NewFace(f, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		panic(err)
	}
	return face
}

func drawString(dst *image.RGBA, face font.Face, x, y float64, c color.RGBA, s string) {
	d := font.Drawer{
		Dst:  dst,
		Src:  image.NewUniform(c),
		Face: face,
		Dot:  fixed.Point26_6{X: fixed.Int26_6(x * 64), Y: fixed.Int26_6(y * 64)},
	}
	d.DrawString(s)
}

func stringWidth(face font.Face, s string) float64 {
	return float64(font.MeasureString(face, s)) / 64
}

func truncateToWidth(face font.Face, s string, maxW float64) string {
	if stringWidth(face, s) <= maxW {
		return s
	}
	r := []rune(s)
	for len(r) > 0 && stringWidth(face, string(r)+"…") > maxW {
		r = r[:len(r)-1]
	}
	return string(r) + "…"
}

// blendPx composites c over the pixel with extra coverage alpha a (0..1).
func blendPx(img *image.RGBA, x, y int, c color.RGBA, a float64) {
	if a <= 0 || !(image.Point{X: x, Y: y}.In(img.Bounds())) {
		return
	}
	if a > 1 {
		a = 1
	}
	sa := float64(c.A) / 255 * a
	i := img.PixOffset(x, y)
	p := img.Pix[i : i+4 : i+4]
	p[0] = uint8(float64(c.R)*sa + float64(p[0])*(1-sa) + 0.5)
	p[1] = uint8(float64(c.G)*sa + float64(p[1])*(1-sa) + 0.5)
	p[2] = uint8(float64(c.B)*sa + float64(p[2])*(1-sa) + 0.5)
	p[3] = uint8(255*sa + float64(p[3])*(1-sa) + 0.5)
}

// fillRoundedRect fills the axis-aligned rounded rectangle [x0,y0]-[x1,y1]
// with corner radius rad, antialiased via a signed-distance edge.
func fillRoundedRect(img *image.RGBA, x0, y0, x1, y1, rad float64, c color.RGBA) {
	cx, cy := (x0+x1)/2, (y0+y1)/2
	bx, by := (x1-x0)/2-rad, (y1-y0)/2-rad
	if bx < 0 {
		bx = 0
	}
	if by < 0 {
		by = 0
	}
	for y := int(y0) - 1; y <= int(y1)+1; y++ {
		for x := int(x0) - 1; x <= int(x1)+1; x++ {
			qx := math.Abs(float64(x)+0.5-cx) - bx
			qy := math.Abs(float64(y)+0.5-cy) - by
			d := math.Hypot(math.Max(qx, 0), math.Max(qy, 0)) - rad
			if qx < 0 && qy < 0 {
				d = math.Max(qx, qy) - rad
			}
			blendPx(img, x, y, c, 0.5-d)
		}
	}
}

func fillCircle(img *image.RGBA, cx, cy, r float64, c color.RGBA) {
	for y := int(cy - r - 1); y <= int(cy+r+1); y++ {
		for x := int(cx - r - 1); x <= int(cx+r+1); x++ {
			d := math.Hypot(float64(x)+0.5-cx, float64(y)+0.5-cy) - r
			blendPx(img, x, y, c, 0.5-d)
		}
	}
}

// glowCircle lays a soft radial halo from r0 out to r1 under a status dot.
func glowCircle(img *image.RGBA, cx, cy, r0, r1 float64, c color.RGBA) {
	for y := int(cy - r1 - 1); y <= int(cy+r1+1); y++ {
		for x := int(cx - r1 - 1); x <= int(cx+r1+1); x++ {
			d := math.Hypot(float64(x)+0.5-cx, float64(y)+0.5-cy)
			if d >= r1 {
				continue
			}
			t := 0.0
			if d > r0 {
				t = (d - r0) / (r1 - r0)
			}
			blendPx(img, x, y, c, 0.35*(1-t)*(1-t))
		}
	}
}
