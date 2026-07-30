// SPDX-FileCopyrightText: 2026 tokajer <tokajer@tokajer.at>
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build linux

package tray

import (
	"math"
	"sync"
)

// This file reproduces the app's four-colour "sync" mark (two rotating arrows,
// see internal/window/icon.svg) as ARGB32 tray pixmaps, so the icon can be
// rotated while syncing and recoloured (red blink) on error. All geometry is in
// the SVG's 256x256 viewBox: centre (128,128), arc radius 74, stroke width 30.

const (
	logoCenter = 128.0
	logoRadius = 74.0
	logoStroke = 30.0
)

// Brand colours, taken verbatim from internal/window/icon.svg.
var (
	colYellow = rgb{0xf5, 0xb3, 0x01}
	colRed    = rgb{0xea, 0x4d, 0x3d}
	colBlue   = rgb{0x3d, 0x7b, 0xf4}
	colGreen  = rgb{0x1f, 0xa4, 0x63}
	colGrey   = rgb{0x9a, 0xa0, 0xa6}
)

// logoSizes are the pixmap sizes offered to the tray host, which picks the best
// match for the panel and downscales as needed.
var logoSizes = []int{22, 32, 48}

type rgb struct{ R, G, B byte }

// arcSeg is one coloured stretch of the ring, swept from Start down to End
// degrees (decreasing), where angle = atan2(dy, dx) in the SVG's y-down space.
type arcSeg struct {
	Start, End float64
	Col        rgb
}

// tri is an arrowhead: three vertices relative to the logo centre.
type tri struct {
	X, Y [3]float64
	Col  rgb
}

type capPt struct {
	x, y float64
	c    rgb
}

// makeLogo renders the mark at the given rotation, with the four ring segments
// coloured by cols (yellow, red, blue, green slots) and scaled to alpha opacity,
// at every size in logoSizes.
func makeLogo(rotDeg float64, cols [4]rgb, alpha float64) []iconPix {
	out := make([]iconPix, len(logoSizes))
	for i, sz := range logoSizes {
		out[i] = renderLogo(sz, rotDeg, cols, alpha)
	}
	return out
}

func renderLogo(size int, rotDeg float64, cols [4]rgb, alpha float64) iconPix {
	const ss = 3 // supersampling per axis for anti-aliasing
	buf := make([]byte, size*size*4)

	a := -rotDeg * math.Pi / 180
	sinA, cosA := math.Sin(a), math.Cos(a)
	scale := 256.0 / float64(size)

	arcs := []arcSeg{
		{79, 1, cols[0]},      // yellow
		{1, -74, cols[1]},     // red
		{-101, -181, cols[2]}, // blue
		{-181, -254, cols[3]}, // green
	}
	caps := []capPt{
		{logoRadius * cosd(79), logoRadius * sind(79), cols[0]},    // yellow tail
		{logoRadius * cosd(-101), logoRadius * sind(-101), cols[2]}, // blue tail
	}
	topHead := tri{X: [3]float64{-3.87, 26.19, 14.61}, Y: [3]float64{-73.90, -91.32, -50.95}, Col: cols[1]}
	botHead := tri{X: [3]float64{3.87, -26.19, -14.61}, Y: [3]float64{73.90, 91.32, 50.95}, Col: cols[3]}

	for py := 0; py < size; py++ {
		for px := 0; px < size; px++ {
			var sr, sg, sb, cov int
			for j := 0; j < ss; j++ {
				for k := 0; k < ss; k++ {
					vx := (float64(px)+(float64(k)+0.5)/ss)*scale - logoCenter
					vy := (float64(py)+(float64(j)+0.5)/ss)*scale - logoCenter
					dx := vx*cosA - vy*sinA
					dy := vx*sinA + vy*cosA
					if c, ok := sampleLogo(dx, dy, arcs, caps, topHead, botHead); ok {
						sr += int(c.R)
						sg += int(c.G)
						sb += int(c.B)
						cov++
					}
				}
			}
			if cov == 0 {
				continue
			}
			i := (py*size + px) * 4
			aa := float64(cov) / float64(ss*ss) * alpha
			buf[i] = byte(aa*255 + 0.5) // A (ARGB32, network byte order)
			buf[i+1] = byte(sr / cov)   // R
			buf[i+2] = byte(sg / cov)   // G
			buf[i+3] = byte(sb / cov)   // B
		}
	}
	return iconPix{W: int32(size), H: int32(size), Bytes: buf}
}

// sampleLogo returns the colour of the mark at a point (relative to the centre),
// or false where the mark is transparent.
func sampleLogo(dx, dy float64, arcs []arcSeg, caps []capPt, top, bot tri) (rgb, bool) {
	if inTri(dx, dy, top) {
		return top.Col, true
	}
	if inTri(dx, dy, bot) {
		return bot.Col, true
	}
	if rr := math.Hypot(dx, dy); math.Abs(rr-logoRadius) <= logoStroke/2 {
		phi := math.Atan2(dy, dx) * 180 / math.Pi
		for _, seg := range arcs {
			if segContains(seg, phi) {
				return seg.Col, true
			}
		}
	}
	for _, c := range caps {
		if math.Hypot(dx-c.x, dy-c.y) <= logoStroke/2 {
			return c.c, true
		}
	}
	return rgb{}, false
}

// segContains reports whether phi lies within a decreasing arc (Start >= End),
// accounting for the atan2 wrap at ±180°.
func segContains(a arcSeg, phi float64) bool {
	for k := -1; k <= 1; k++ {
		p := phi + float64(k)*360
		if p <= a.Start && p >= a.End {
			return true
		}
	}
	return false
}

func inTri(px, py float64, t tri) bool {
	d1 := edge(px, py, t.X[0], t.Y[0], t.X[1], t.Y[1])
	d2 := edge(px, py, t.X[1], t.Y[1], t.X[2], t.Y[2])
	d3 := edge(px, py, t.X[2], t.Y[2], t.X[0], t.Y[0])
	neg := d1 < 0 || d2 < 0 || d3 < 0
	pos := d1 > 0 || d2 > 0 || d3 > 0
	return !(neg && pos)
}

func edge(px, py, ax, ay, bx, by float64) float64 {
	return (px-bx)*(ay-by) - (ax-bx)*(py-by)
}

func cosd(deg float64) float64 { return math.Cos(deg * math.Pi / 180) }
func sind(deg float64) float64 { return math.Sin(deg * math.Pi / 180) }

// ---------------- cached frame sets ----------------
//
// Frames are rendered once on first use and reused: a spinning sync ring, a
// two-frame red blink, and the two static (idle / grey) icons.

var (
	idleOnce sync.Once
	idlePix  []iconPix
	greyOnce sync.Once
	greyPix  []iconPix
	spinOnce sync.Once
	spinPix  [][]iconPix
	errOnce  sync.Once
	errPix   [][]iconPix
)

func brandCols() [4]rgb { return [4]rgb{colYellow, colRed, colBlue, colGreen} }

// idleFrame is the full-colour mark, shown when up to date.
func idleFrame() []iconPix {
	idleOnce.Do(func() { idlePix = makeLogo(0, brandCols(), 1.0) })
	return idlePix
}

// greyFrame is the desaturated mark, shown when disconnected or paused.
func greyFrame() []iconPix {
	greyOnce.Do(func() {
		greyPix = makeLogo(0, [4]rgb{colGrey, colGrey, colGrey, colGrey}, 0.9)
	})
	return greyPix
}

// spinFrames are one full revolution of the full-colour mark.
func spinFrames() [][]iconPix {
	spinOnce.Do(func() {
		const n = 24
		cols := brandCols()
		spinPix = make([][]iconPix, n)
		for i := 0; i < n; i++ {
			spinPix[i] = makeLogo(float64(i)/float64(n)*360, cols, 1.0)
		}
	})
	return spinPix
}

// errorFrames alternate a bright and a dim all-red mark to blink on error.
func errorFrames() [][]iconPix {
	errOnce.Do(func() {
		red := [4]rgb{colRed, colRed, colRed, colRed}
		errPix = [][]iconPix{
			makeLogo(0, red, 1.0),
			makeLogo(0, red, 0.22),
		}
	})
	return errPix
}
