// Package chart draws a small line chart of exchange rates as a PNG, using
// only the standard library. It draws at twice the output size and scales
// down, which smooths lines and text without a separate anti-aliasing step.
package chart

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Output size. Telegram accepts photos up to 10000 for width plus height.
const (
	Width  = 1000
	Height = 560
	// supersample is how many canvas pixels make one output pixel per side.
	supersample = 2
)

// Plot area inside the image, in output pixels.
const (
	plotLeft   = 110
	plotRight  = Width - 36
	plotTop    = 84
	plotBottom = Height - 56
)

var (
	colorBackground = color.RGBA{255, 255, 255, 255}
	colorGrid       = color.RGBA{229, 231, 235, 255}
	colorAxisText   = color.RGBA{107, 114, 128, 255}
	colorTitle      = color.RGBA{17, 24, 39, 255}
	colorLine       = color.RGBA{37, 99, 235, 255}
	colorFill       = color.RGBA{37, 99, 235, 30}
	colorMin        = color.RGBA{220, 38, 38, 255}
	colorMax        = color.RGBA{22, 163, 74, 255}
)

// Point is the rate on one day.
type Point struct {
	Date  time.Time
	Value float64
}

var ErrNotEnoughData = errors.New("a chart needs at least two points")

// Render draws points (in any order) under title, for example "USD/RUB".
func Render(title string, points []Point) ([]byte, error) {
	if len(points) < 2 {
		return nil, ErrNotEnoughData
	}
	points = append([]Point(nil), points...)
	sort.Slice(points, func(i, j int) bool { return points[i].Date.Before(points[j].Date) })
	for _, p := range points {
		if math.IsNaN(p.Value) || math.IsInf(p.Value, 0) {
			return nil, errors.New("chart values must be finite")
		}
	}
	first, last := points[0], points[len(points)-1]
	days := last.Date.Sub(first.Date).Hours() / 24
	if days <= 0 {
		return nil, ErrNotEnoughData
	}

	minIndex, maxIndex := 0, 0
	for i, p := range points {
		if p.Value < points[minIndex].Value {
			minIndex = i
		}
		if p.Value > points[maxIndex].Value {
			maxIndex = i
		}
	}
	axis := niceAxis(points[minIndex].Value, points[maxIndex].Value, 5)

	c := newCanvas(Width, Height)
	c.fillRect(0, 0, Width, Height, colorBackground)

	x := func(date time.Time) float64 {
		return plotLeft + (plotRight-plotLeft)*date.Sub(first.Date).Hours()/24/days
	}
	y := func(value float64) float64 {
		return plotBottom - (plotBottom-plotTop)*(value-axis.min)/(axis.max-axis.min)
	}

	// Horizontal grid with value labels.
	for i := 0; i < axis.count; i++ {
		value := axis.min + float64(i)*axis.step
		gy := y(value)
		c.fillRect(plotLeft, gy-0.5, plotRight, gy+0.5, colorGrid)
		c.text(plotLeft-12, gy, formatValue(value, axis.decimals), 2, colorAxisText, alignRight)
	}
	// Date labels, at most about seven, with light vertical lines.
	every := max(1, int(math.Ceil(float64(len(points))/7)))
	for i := len(points) - 1; i >= 0; i -= every {
		gx := x(points[i].Date)
		c.fillRect(gx-0.5, plotTop, gx+0.5, plotBottom, colorGrid)
		c.text(gx, plotBottom+24, points[i].Date.Format("02.01"), 2, colorAxisText, alignCenter)
	}

	// Area under the line, then the line itself.
	for i := 1; i < len(points); i++ {
		c.fillUnder(x(points[i-1].Date), y(points[i-1].Value), x(points[i].Date), y(points[i].Value), plotBottom, colorFill)
	}
	for i := 1; i < len(points); i++ {
		c.line(x(points[i-1].Date), y(points[i-1].Value), x(points[i].Date), y(points[i].Value), 3, colorLine)
	}
	c.disc(x(points[minIndex].Date), y(points[minIndex].Value), 5, colorMin)
	c.disc(x(points[maxIndex].Date), y(points[maxIndex].Value), 5, colorMax)
	c.disc(x(last.Date), y(last.Value), 6, colorLine)

	c.text(24, 36, strings.ToUpper(title), 4, colorTitle, alignLeft)
	c.text(plotRight, 36, formatValue(last.Value, max(2, axis.decimals+1)), 4, colorLine, alignRight)
	c.text(24+float64(textWidth(strings.ToUpper(title))*4)+24, 40, first.Date.Format("02.01.2006")+" - "+last.Date.Format("02.01.2006"), 2, colorAxisText, alignLeft)

	var out bytes.Buffer
	if err := png.Encode(&out, c.downsample()); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

type axisRange struct {
	min, max, step float64
	count          int
	decimals       int
}

// niceAxis spans lo..hi with about target steps of 1, 2 or 5 times a power of
// ten, so the labels are round numbers.
func niceAxis(lo, hi float64, target int) axisRange {
	span := hi - lo
	if span <= 0 {
		span = math.Max(math.Abs(hi)*0.02, 1e-6)
		lo, hi = lo-span/2, hi+span/2
	}
	raw := span / float64(target)
	magnitude := math.Pow(10, math.Floor(math.Log10(raw)))
	step := magnitude * 10
	switch normalized := raw / magnitude; {
	case normalized < 1.5:
		step = magnitude
	case normalized < 3:
		step = magnitude * 2
	case normalized < 7:
		step = magnitude * 5
	}
	start := math.Floor(lo/step) * step
	end := math.Ceil(hi/step) * step
	if end == start {
		end = start + step
	}
	return axisRange{
		min:      start,
		max:      end,
		step:     step,
		count:    int(math.Round((end-start)/step)) + 1,
		decimals: max(0, int(-math.Floor(math.Log10(step)))),
	}
}

// formatValue writes value the way the bot does: "12 345,67".
func formatValue(value float64, decimals int) string {
	formatted := strconv.FormatFloat(value, 'f', decimals, 64)
	sign := ""
	if strings.HasPrefix(formatted, "-") {
		sign, formatted = "-", formatted[1:]
	}
	whole, fraction, hasFraction := strings.Cut(formatted, ".")
	var grouped strings.Builder
	for i, r := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			grouped.WriteByte(' ')
		}
		grouped.WriteRune(r)
	}
	if hasFraction {
		return sign + grouped.String() + "," + fraction
	}
	return sign + grouped.String()
}

type alignment int

const (
	alignLeft alignment = iota
	alignCenter
	alignRight
)

// canvas draws in output coordinates onto a supersampled image.
type canvas struct {
	img *image.RGBA
}

func newCanvas(width, height int) *canvas {
	return &canvas{img: image.NewRGBA(image.Rect(0, 0, width*supersample, height*supersample))}
}

// blend paints one canvas pixel with col over what is there.
func (c *canvas) blend(px, py int, col color.RGBA) {
	if !(image.Point{px, py}).In(c.img.Rect) {
		return
	}
	i := c.img.PixOffset(px, py)
	pix := c.img.Pix[i : i+4 : i+4]
	a := uint32(col.A)
	pix[0] = uint8((uint32(col.R)*a + uint32(pix[0])*(255-a)) / 255)
	pix[1] = uint8((uint32(col.G)*a + uint32(pix[1])*(255-a)) / 255)
	pix[2] = uint8((uint32(col.B)*a + uint32(pix[2])*(255-a)) / 255)
	pix[3] = 255
}

func (c *canvas) fillRect(x0, y0, x1, y1 float64, col color.RGBA) {
	for py := int(math.Round(y0 * supersample)); py < int(math.Round(y1*supersample)); py++ {
		for px := int(math.Round(x0 * supersample)); px < int(math.Round(x1*supersample)); px++ {
			c.blend(px, py, col)
		}
	}
}

// line draws a segment of the given width with round ends.
func (c *canvas) line(x0, y0, x1, y1, width float64, col color.RGBA) {
	s := float64(supersample)
	x0, y0, x1, y1, half := x0*s, y0*s, x1*s, y1*s, width*s/2
	minX, maxX := int(math.Floor(math.Min(x0, x1)-half)), int(math.Ceil(math.Max(x0, x1)+half))
	minY, maxY := int(math.Floor(math.Min(y0, y1)-half)), int(math.Ceil(math.Max(y0, y1)+half))
	dx, dy := x1-x0, y1-y0
	length2 := dx*dx + dy*dy
	for py := minY; py <= maxY; py++ {
		for px := minX; px <= maxX; px++ {
			cx, cy := float64(px)+0.5, float64(py)+0.5
			t := 0.0
			if length2 > 0 {
				t = math.Max(0, math.Min(1, ((cx-x0)*dx+(cy-y0)*dy)/length2))
			}
			ex, ey := cx-(x0+t*dx), cy-(y0+t*dy)
			if ex*ex+ey*ey <= half*half {
				c.blend(px, py, col)
			}
		}
	}
}

// fillUnder fills the area between a segment and the baseline.
func (c *canvas) fillUnder(x0, y0, x1, y1, baseline float64, col color.RGBA) {
	s := float64(supersample)
	for px := int(math.Round(x0 * s)); px < int(math.Round(x1*s)); px++ {
		t := (float64(px)/s - x0) / (x1 - x0)
		top := y0 + t*(y1-y0)
		for py := int(math.Round(top * s)); py < int(math.Round(baseline*s)); py++ {
			c.blend(px, py, col)
		}
	}
}

func (c *canvas) disc(x, y, radius float64, col color.RGBA) {
	c.line(x, y, x, y, radius*2, col)
}

// text draws s with each font dot size output pixels wide, vertically
// centered on y.
func (c *canvas) text(x, y float64, s string, size int, col color.RGBA, align alignment) {
	width := float64(textWidth(s) * size)
	switch align {
	case alignCenter:
		x -= width / 2
	case alignRight:
		x -= width
	}
	top := y - float64(glyphHeight*size)/2
	dot := float64(size)
	for _, r := range s {
		glyph, ok := glyphs[r]
		if !ok {
			continue
		}
		for row, bits := range glyph {
			for column := 0; column < glyphWidth; column++ {
				if bits&(1<<(glyphWidth-1-column)) != 0 {
					gx, gy := x+float64(column)*dot, top+float64(row)*dot
					c.fillRect(gx, gy, gx+dot, gy+dot, col)
				}
			}
		}
		x += glyphAdvance * dot
	}
}

// downsample averages each supersample x supersample block into one pixel.
func (c *canvas) downsample() *image.RGBA {
	bounds := c.img.Rect
	out := image.NewRGBA(image.Rect(0, 0, bounds.Dx()/supersample, bounds.Dy()/supersample))
	const n = supersample * supersample
	for y := 0; y < out.Rect.Dy(); y++ {
		for x := 0; x < out.Rect.Dx(); x++ {
			var sum [4]int
			for dy := 0; dy < supersample; dy++ {
				i := c.img.PixOffset(x*supersample, y*supersample+dy)
				for dx := 0; dx < supersample; dx++ {
					for k := 0; k < 4; k++ {
						sum[k] += int(c.img.Pix[i+dx*4+k])
					}
				}
			}
			o := out.PixOffset(x, y)
			for k := 0; k < 4; k++ {
				out.Pix[o+k] = uint8(sum[k] / n)
			}
		}
	}
	return out
}
