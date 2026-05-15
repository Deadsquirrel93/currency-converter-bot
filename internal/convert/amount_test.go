package convert

import "testing"

func TestParseAmount(t *testing.T) {
	tests := map[string]float64{
		"12 345,67 usd": 12345.67,
		"1.234,56":      1234.56,
		"1,234.56":      1234.56,
		"3.5":           3.5,
		"3,5":           3.5,
		"abc99":         99,
		"1O0 usd":       10,
		"100lira":       100,
		"100 долларов":  100,
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

func TestParseAmountMultiplication(t *testing.T) {
	tests := map[string]float64{
		"100х9":                900,
		"100 x 9":              900,
		"100X9":                900,
		"100 * 9":              900,
		"70 х 5 литров молока": 350,
		"12 x 4 кг курицы":     48,
		"1 000 х 2":            2000,
		"1,5 х 2":              3,
		"1.234,56 * 2":         2469.12,
		"молоко 70 х 5 литров": 350,
		"100 usd х 9":          1009,
		"100x":                 100,
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

func TestParseAmountsSumsLines(t *testing.T) {
	total, count, err := ParseAmounts("100\n200,50\n3.5\n\nabc\n12")
	if err != nil {
		t.Fatalf("ParseAmounts() error = %v", err)
	}
	if total != 316 {
		t.Fatalf("ParseAmounts() total = %v, want %v", total, 316.0)
	}
	if count != 4 {
		t.Fatalf("ParseAmounts() count = %d, want %d", count, 4)
	}
}

func TestParseAmountsSumsMultiplications(t *testing.T) {
	total, count, err := ParseAmounts("70 х 5 литров молока\n12 x 4 кг курицы")
	if err != nil {
		t.Fatalf("ParseAmounts() error = %v", err)
	}
	if total != 398 {
		t.Fatalf("ParseAmounts() total = %v, want %v", total, 398.0)
	}
	if count != 2 {
		t.Fatalf("ParseAmounts() count = %d, want %d", count, 2)
	}
}

func TestParseAmountsDoesNotReplaceLetters(t *testing.T) {
	total, count, err := ParseAmounts("1O0\nl5\nЗ,5")
	if err != nil {
		t.Fatalf("ParseAmounts() error = %v", err)
	}
	if total != 20 {
		t.Fatalf("ParseAmounts() total = %v, want %v", total, 20.0)
	}
	if count != 3 {
		t.Fatalf("ParseAmounts() count = %d, want %d", count, 3)
	}
}

func TestParseAmountsNoAmount(t *testing.T) {
	_, _, err := ParseAmounts("abc\n\nusd")
	if err != ErrNoAmount {
		t.Fatalf("ParseAmounts() error = %v, want %v", err, ErrNoAmount)
	}
}

func TestFormatMoney(t *testing.T) {
	got := FormatMoney(1234567.895)
	want := "1 234 567,90"
	if got != want {
		t.Fatalf("FormatMoney() = %q, want %q", got, want)
	}
}
