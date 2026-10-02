package chart

import (
	"bytes"
	"errors"
	"image/color"
	"image/png"
	"math"
	"testing"
	"time"
)

func series(values ...float64) []Point {
	start := time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC)
	points := make([]Point, len(values))
	for i, value := range values {
		points[i] = Point{Date: start.AddDate(0, 0, i), Value: value}
	}
	return points
}

func TestRenderProducesPNG(t *testing.T) {
	values := make([]float64, 30)
	for i := range values {
		values[i] = 82 + 2.5*math.Sin(float64(i)/4)
	}
	points := series(values...)
	// Order must not matter.
	points[0], points[29] = points[29], points[0]

	raw, err := Render("USD/RUB", points)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > 200<<10 {
		t.Fatalf("png is %d bytes, want a small image", len(raw))
	}
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != Width || b.Dy() != Height {
		t.Fatalf("size = %v, want %dx%d", b, Width, Height)
	}
	// The line colour must appear inside the plot area.
	found := false
	for y := plotTop; y < plotBottom && !found; y++ {
		for x := plotLeft; x < plotRight; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			if c := (color.RGBA{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), 255}); c == colorLine {
				found = true
				break
			}
		}
	}
	if !found {
		t.Fatal("line colour not found in the plot area")
	}
}

func TestRenderEdgeCases(t *testing.T) {
	if _, err := Render("USD/RUB", series(80, 80, 80)); err != nil {
		t.Fatalf("flat series: %v", err)
	}
	if _, err := Render("RUB/USD", series(0.0118, 0.0119)); err != nil {
		t.Fatalf("small values: %v", err)
	}
	if _, err := Render("USD/UZS", series(12500, 12650, 12400)); err != nil {
		t.Fatalf("large values: %v", err)
	}
	for name, points := range map[string][]Point{
		"empty":     nil,
		"one point": series(80),
		"same day":  {{Date: time.Unix(0, 0), Value: 1}, {Date: time.Unix(0, 0), Value: 2}},
	} {
		if _, err := Render("USD/RUB", points); !errors.Is(err, ErrNotEnoughData) {
			t.Fatalf("%s: err = %v, want ErrNotEnoughData", name, err)
		}
	}
	if _, err := Render("USD/RUB", series(80, math.NaN())); err == nil {
		t.Fatal("NaN must be rejected")
	}
}

func TestNiceAxis(t *testing.T) {
	tests := []struct {
		lo, hi         float64
		min, max, step float64
		decimals       int
	}{
		{80.4, 85.5, 80, 86, 1, 0},
		{0.01172, 0.01205, 0.0117, 0.01205, 0.00005, 5},
		{12400, 12650, 12400, 12650, 50, 0},
		{80, 80, 79, 81, 0.5, 1},
	}
	for _, tt := range tests {
		got := niceAxis(tt.lo, tt.hi, 5)
		near := func(a, b float64) bool { return math.Abs(a-b) < 1e-9*math.Max(1, math.Abs(b)) }
		if !near(got.min, tt.min) || !near(got.max, tt.max) || !near(got.step, tt.step) || got.decimals != tt.decimals {
			t.Fatalf("niceAxis(%v, %v) = %+v, want %v..%v step %v decimals %d", tt.lo, tt.hi, got, tt.min, tt.max, tt.step, tt.decimals)
		}
		if got.min > tt.lo || got.max < tt.hi {
			t.Fatalf("niceAxis(%v, %v) = %v..%v does not cover the data", tt.lo, tt.hi, got.min, got.max)
		}
	}
}

func TestFontCoversChartText(t *testing.T) {
	for _, r := range "0123456789.,-+/:%() ABCDEFGHIJKLMNOPQRSTUVWXYZ" {
		if _, ok := glyphs[r]; !ok {
			t.Fatalf("no glyph for %q", r)
		}
	}
	if got := textWidth("USD"); got != 17 {
		t.Fatalf("textWidth(USD) = %d, want 17", got)
	}
	if got := textWidth("Юань"); got != 0 {
		t.Fatalf("textWidth of unsupported text = %d, want 0", got)
	}
}
