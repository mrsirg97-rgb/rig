package view

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"math"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

const (
	sheetGap = 4
	labelH   = 13
	labelPad = 2
)

var (
	sheetBack = color.NRGBA{R: 18, G: 18, B: 20, A: 255}
	sheetInk  = color.NRGBA{R: 235, G: 235, B: 235, A: 255}
	labelFace = basicfont.Face7x13
)

type grid struct {
	cols, rows int
	tileW      int
	tileH      int
	scale      float64
	closeness  float64
}

func contactSheet(ctx context.Context, frames []frame) (*image.NRGBA, error) {
	if len(frames) == 0 {
		return nil, errors.New("no frames to tile")
	}
	b := frames[0].img.Bounds()
	fw, fh := b.Dx(), b.Dy()
	if fw <= 0 || fh <= 0 {
		return nil, errors.New("the frames have no pixels")
	}
	best := grid{scale: -1, closeness: math.Inf(1)}
	for cols := 1; cols <= len(frames); cols++ {
		rows := (len(frames) + cols - 1) / cols
		maxCellW := (maxSide - sheetGap*(cols+1)) / cols
		maxCellH := (maxSide - sheetGap*(rows+1)) / rows
		innerH := maxCellH - labelH - 2*labelPad
		if maxCellW < 1 || innerH < 1 {
			continue
		}
		s := math.Min(float64(maxCellW)/float64(fw), float64(innerH)/float64(fh))
		if s > 1 {
			s = 1
		}
		ratio := float64(cols) * float64(fw) / (float64(rows) * float64(fh))
		closeness := math.Abs(math.Log(ratio))
		if s > best.scale+1e-9 || (math.Abs(s-best.scale) <= 1e-9 && closeness < best.closeness) {
			best = grid{
				cols: cols, rows: rows,
				tileW: atLeastOne(int(math.Round(float64(fw) * s))),
				tileH: atLeastOne(int(math.Round(float64(fh) * s))),
				scale: s, closeness: closeness,
			}
		}
	}
	if best.scale < 0 {
		return nil, fmt.Errorf("the %dx%d frames do not fit a %d-pixel sheet", fw, fh, maxSide)
	}
	g := best
	sheet := image.NewNRGBA(image.Rect(0, 0, g.cols*g.tileW+sheetGap*(g.cols+1), g.rows*g.tileH+sheetGap*(g.rows+1)))
	fill(sheet, sheetBack)
	for i, f := range frames {
		col, row := i%g.cols, i/g.cols
		x0 := sheetGap + col*(g.tileW+sheetGap)
		y0 := sheetGap + row*(g.tileH+sheetGap)
		label(sheet, x0, y0+labelPad+labelFace.Metrics().Ascent.Round(), stampLabel(f.at))
		scaled, _, err := resampleTo(ctx, f.img, g.tileW, g.tileH)
		if err != nil {
			return nil, err
		}
		copyPixels(sheet, x0, y0+labelH+2*labelPad, scaled)
	}
	return sheet, nil
}

func fill(dst *image.NRGBA, c color.NRGBA) {
	for y := 0; y < dst.Bounds().Dy(); y++ {
		for x := 0; x < dst.Bounds().Dx(); x++ {
			dst.SetNRGBA(x, y, c)
		}
	}
}

func label(dst *image.NRGBA, x, y int, text string) {
	drawer := font.Drawer{Dst: dst, Src: image.NewUniform(sheetInk), Face: labelFace, Dot: fixed.P(x, y)}
	drawer.DrawString(text)
}

func copyPixels(dst *image.NRGBA, dx, dy int, src *image.NRGBA) {
	sb := src.Bounds()
	db := dst.Bounds()
	for y := 0; y < sb.Dy(); y++ {
		ty := dy + y
		if ty < db.Min.Y || ty >= db.Max.Y {
			continue
		}
		for x := 0; x < sb.Dx(); x++ {
			tx := dx + x
			if tx < db.Min.X || tx >= db.Max.X {
				continue
			}
			dst.SetNRGBA(tx, ty, src.NRGBAAt(sb.Min.X+x, sb.Min.Y+y))
		}
	}
}
