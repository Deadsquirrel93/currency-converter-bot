package convert

import "testing"

func TestParseAmount(t *testing.T) {
	tests := map[string]float64{
		"12 345,67 usd": 12345.67,
		"1.234,56":      1234.56,
		"1,234.56":      1234.56,
		"abc99":         99,
	}

	for input, want := range tests {
		got, err := ParseAmount(input)
		if err != nil {
			t.Fatalf("ParseAmount(%q): %v", input, err)
		}
		if got != want {
			t.Fatalf("ParseAmount(%q) = %v, want %v", input, got, want)
		}
	}
}

func TestFormatMoney(t *testing.T) {
	got := FormatMoney(1234567.895)
	want := "1 234 567,90"
	if got != want {
		t.Fatalf("FormatMoney() = %q, want %q", got, want)
	}
}
