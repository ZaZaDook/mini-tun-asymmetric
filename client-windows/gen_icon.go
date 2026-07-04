//go:build ignore

// One-shot icon generator: draws the Mini-Tun Asymmetric logo (the recycling
// loop PC -> server A -> server B -> PC) on a rounded gradient tile and writes
// a multi-size Windows .ico (16/32/48/64/128/256). Pure stdlib.
//
//   go run gen_icon.go   (run from client-windows/, writes assets/icon.ico)
package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"sort"
)

const ss = 4 // supersampling factor for smooth edges

type rgb struct{ r, g, b float64 }

func hex(h uint32) rgb {
	return rgb{float64((h >> 16) & 255), float64((h >> 8) & 255), float64(h & 255)}
}
func lerp(a, b rgb, t float64) rgb {
	return rgb{a.r + (b.r-a.r)*t, a.g + (b.g-a.g)*t, a.b + (b.b-a.b)*t}
}

// brand gradient (matches the dark preset bar: #3b82f6 -> #6366f1)
var gA, gB = hex(0x3b82f6), hex(0x6366f1)

// render draws the icon at the given pixel size (already supersampled).
func render(size int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	fs := float64(size)
	radius := fs * 0.22 // rounded-corner radius

	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			fx, fy := float64(x), float64(y)
			// rounded-rect alpha (1 inside, 0 outside, soft 1px edge)
			a := roundedAlpha(fx, fy, fs, radius)
			if a <= 0 {
				continue
			}
			// diagonal gradient fill
			t := (fx/fs + fy/fs) / 2
			c := lerp(gA, gB, t)
			img.SetRGBA(x, y, color.RGBA{uint8(c.r), uint8(c.g), uint8(c.b), uint8(a * 255)})
		}
	}

	// Logo geometry traced from НоваяИконкаПриложенияТреяТаскБара.svg (coords
	// are the SVG's /100). A downward triangle, nodes at the 3 vertices, and
	// filled arrowheads sitting on the edge midpoints. Scaled to the tile.
	pad := fs * 0.16
	span := fs - 2*pad
	P := func(vx, vy float64) (float64, float64) {
		return pad + vx/100*span, pad + vy/100*span
	}

	white := color.RGBA{255, 255, 255, 255}
	stroke := fs * 0.028

	// triangle vertices (top-left, top-right, bottom apex)
	tl := [2]float64{16.6, 18.3}
	tr := [2]float64{84.1, 18.3}
	ap := [2]float64{50.4, 76.8}

	// three loop edges (the recycling triangle outline)
	drawLine(img, P, tl[0], tl[1], tr[0], tr[1], stroke, white) // top
	drawLine(img, P, tr[0], tr[1], ap[0], ap[1], stroke, white) // right
	drawLine(img, P, ap[0], ap[1], tl[0], tl[1], stroke, white) // left

	// filled arrowheads on the edge midpoints (traced from the SVG polygons)
	fillPolygon(img, P, white, [][2]float64{{58.2, 18.4}, {50.2, 14.4}, {42.2, 10.4}, {42.2, 18.4}, {42.2, 26.4}, {50.2, 22.4}}) // top edge
	fillPolygon(img, P, white, [][2]float64{{61.9, 56.8}, {69.4, 51.8}, {76.8, 46.9}, {69.9, 42.9}, {63.0, 38.9}, {62.5, 47.8}}) // right edge
	fillPolygon(img, P, white, [][2]float64{{28.1, 38.3}, {28.7, 47.2}, {29.2, 56.1}, {36.1, 52.1}, {43.1, 48.1}, {35.6, 43.2}}) // left edge

	// three nodes on top (PC apex, server A top-left, server B top-right)
	for _, n := range [][2]float64{tl, tr, ap} {
		cx, cy := P(n[0], n[1])
		fillCircle(img, cx, cy, fs*0.062, white)
	}

	return img
}

func roundedAlpha(x, y, size, r float64) float64 {
	// distance into the rounded rectangle; returns soft alpha near the border.
	min := func(a, b float64) float64 {
		if a < b {
			return a
		}
		return b
	}
	dx := min(x, size-x)
	dy := min(y, size-y)
	// corner region
	if dx < r && dy < r {
		ddx, ddy := r-dx, r-dy
		d := math.Hypot(ddx, ddy)
		return clamp(r - d + 0.5)
	}
	return clamp(min(dx, dy) + 0.5)
}

func clamp(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func fillCircle(img *image.RGBA, cx, cy, rad float64, c color.RGBA) {
	x0, y0 := int(cx-rad-1), int(cy-rad-1)
	x1, y1 := int(cx+rad+1), int(cy+rad+1)
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			d := math.Hypot(float64(x)-cx, float64(y)-cy)
			a := clamp(rad - d + 0.5)
			if a > 0 {
				blend(img, x, y, c, a)
			}
		}
	}
}

func drawLine(img *image.RGBA, P func(float64, float64) (float64, float64), x1, y1, x2, y2, w float64, c color.RGBA) {
	ax, ay := P(x1, y1)
	bx, by := P(x2, y2)
	minx, maxx := int(math.Min(ax, bx)-w-1), int(math.Max(ax, bx)+w+1)
	miny, maxy := int(math.Min(ay, by)-w-1), int(math.Max(ay, by)+w+1)
	for y := miny; y <= maxy; y++ {
		for x := minx; x <= maxx; x++ {
			d := distToSeg(float64(x), float64(y), ax, ay, bx, by)
			a := clamp(w - d + 0.5)
			if a > 0 {
				blend(img, x, y, c, a)
			}
		}
	}
}

func drawPoly(img *image.RGBA, P func(float64, float64) (float64, float64), w float64, c color.RGBA, pts [][2]float64) {
	for i := 0; i+1 < len(pts); i++ {
		drawLine(img, P, pts[i][0], pts[i][1], pts[i+1][0], pts[i+1][1], w, c)
	}
}

// fillPolygon fills a closed polygon (given in viewBox coords, mapped via P)
// using the even-odd scanline rule. Edges are smoothed by the icon's overall
// supersampling, so a hard per-pixel test is enough here.
func fillPolygon(img *image.RGBA, P func(float64, float64) (float64, float64), c color.RGBA, pts [][2]float64) {
	n := len(pts)
	if n < 3 {
		return
	}
	xs := make([]float64, n)
	ys := make([]float64, n)
	minY, maxY := math.Inf(1), math.Inf(-1)
	for i, p := range pts {
		xs[i], ys[i] = P(p[0], p[1])
		if ys[i] < minY {
			minY = ys[i]
		}
		if ys[i] > maxY {
			maxY = ys[i]
		}
	}
	for y := int(minY); y <= int(maxY); y++ {
		fy := float64(y) + 0.5
		var nodes []float64
		j := n - 1
		for i := 0; i < n; i++ {
			if (ys[i] < fy && ys[j] >= fy) || (ys[j] < fy && ys[i] >= fy) {
				nodes = append(nodes, xs[i]+(fy-ys[i])/(ys[j]-ys[i])*(xs[j]-xs[i]))
			}
			j = i
		}
		sort.Float64s(nodes)
		for k := 0; k+1 < len(nodes); k += 2 {
			for x := int(nodes[k] + 0.5); x < int(nodes[k+1]+0.5); x++ {
				blend(img, x, y, c, 1)
			}
		}
	}
}

func distToSeg(px, py, ax, ay, bx, by float64) float64 {
	dx, dy := bx-ax, by-ay
	l2 := dx*dx + dy*dy
	if l2 == 0 {
		return math.Hypot(px-ax, py-ay)
	}
	t := ((px-ax)*dx + (py-ay)*dy) / l2
	t = clamp(t)
	return math.Hypot(px-(ax+t*dx), py-(ay+t*dy))
}

func blend(img *image.RGBA, x, y int, c color.RGBA, a float64) {
	if x < 0 || y < 0 || x >= img.Bounds().Dx() || y >= img.Bounds().Dy() {
		return
	}
	o := img.RGBAAt(x, y)
	na := a + float64(o.A)/255*(1-a)
	if na <= 0 {
		return
	}
	mix := func(s, d uint8) uint8 {
		return uint8((float64(s)*a + float64(d)/255*float64(o.A)/255*(1-a)*255) / na)
	}
	img.SetRGBA(x, y, color.RGBA{mix(c.R, o.R), mix(c.G, o.G), mix(c.B, o.B), uint8(na * 255)})
}

// downscale averages an ss-times-larger image down to size x size (box filter).
func downscale(src *image.RGBA, size int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			var r, g, b, a float64
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					p := src.RGBAAt(x*ss+sx, y*ss+sy)
					af := float64(p.A) / 255
					r += float64(p.R) * af
					g += float64(p.G) * af
					b += float64(p.B) * af
					a += af
				}
			}
			n := float64(ss * ss)
			if a > 0 {
				dst.SetRGBA(x, y, color.RGBA{uint8(r / a), uint8(g / a), uint8(b / a), uint8(a / n * 255)})
			}
		}
	}
	return dst
}

func main() {
	sizes := []int{16, 32, 48, 64, 128, 256}
	var pngs [][]byte
	for _, s := range sizes {
		big := render(s * ss)
		small := downscale(big, s)
		var buf bytes.Buffer
		if err := png.Encode(&buf, small); err != nil {
			panic(err)
		}
		pngs = append(pngs, buf.Bytes())
	}
	ico := buildICO(sizes, pngs)
	if err := os.WriteFile("assets/icon.ico", ico, 0644); err != nil {
		panic(err)
	}
	os.Stdout.WriteString("wrote assets/icon.ico with sizes 16/32/48/64/128/256\n")
}

// buildICO packs PNG-compressed images into an .ico container (PNG entries are
// valid for sizes; Windows Vista+ reads them natively).
func buildICO(sizes []int, pngs [][]byte) []byte {
	var b bytes.Buffer
	n := len(sizes)
	// ICONDIR
	binary.Write(&b, binary.LittleEndian, uint16(0)) // reserved
	binary.Write(&b, binary.LittleEndian, uint16(1)) // type: icon
	binary.Write(&b, binary.LittleEndian, uint16(n)) // count

	offset := 6 + 16*n
	for i, s := range sizes {
		dim := byte(0) // 0 means 256
		if s < 256 {
			dim = byte(s)
		}
		b.WriteByte(dim)                                          // width
		b.WriteByte(dim)                                          // height
		b.WriteByte(0)                                            // palette
		b.WriteByte(0)                                            // reserved
		binary.Write(&b, binary.LittleEndian, uint16(1))         // color planes
		binary.Write(&b, binary.LittleEndian, uint16(32))        // bpp
		binary.Write(&b, binary.LittleEndian, uint32(len(pngs[i]))) // size
		binary.Write(&b, binary.LittleEndian, uint32(offset))    // offset
		offset += len(pngs[i])
	}
	for _, p := range pngs {
		b.Write(p)
	}
	return b.Bytes()
}
