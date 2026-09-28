package convert

import (
	"errors"
	"math"
	"strings"
	"testing"
)

func TestParseAmountExpressions(t *testing.T) {
	tests := []struct {
		input          string
		commaThousands bool
		want           float64
	}{
		{"100+50 usd", false, 150},
		{"100 + 50", false, 150},
		{"(12*3)+5", false, 41},
		{"1 000 / 4", false, 250},
		{"2+3*4", false, 14},
		{"(2+3)*4", false, 20},
		{"10 - 2 - 3", false, 5},
		{"100 / 4 / 5", false, 5},
		{"((1+2)*(3+4))", false, 21},
		{"1,5 + 2,5", false, 4},
		{"1,000+2,000", true, 3000},
		{"1,000+2,000", false, 3},
		{"100 usd + 50 usd", false, 150},
		{"100$ + 50$", false, 150},
		{"$100 + $50", false, 150},
		{"итого 100 + 50 рублей", false, 150},
		{"100 - 100", false, 0},
		{"100 -50", false, 50},
		{"(100-50)*2", false, 100},
		{"100-50+3", false, 53},
		{"1/2", false, 0.5},
		{"5 000 − 1 000", false, 4000},
		{"6 × 7", false, 42},
		{"84 ÷ 2", false, 42},
		{"2 х 3 + 4", false, 10},
		{"100 usd x 9 + 1", false, 901},
	}
	for _, tt := range tests {
		got, err := ParseAmountWith(tt.input, Options{CommaThousands: tt.commaThousands})
		if err != nil {
			t.Fatalf("ParseAmountWith(%q, %v): %v", tt.input, tt.commaThousands, err)
		}
		if math.Abs(got-tt.want) > 1e-9 {
			t.Fatalf("ParseAmountWith(%q, %v) = %v, want %v", tt.input, tt.commaThousands, got, tt.want)
		}
	}
}

// Inputs that contain operator characters but are not arithmetic keep the
// behavior they had before expressions were supported.
func TestParseAmountNonExpressionsKeepOldBehavior(t *testing.T) {
	for input, want := range map[string]float64{
		"-100":      100,
		"+100":      100,
		"100-":      100,
		"100 /":     100,
		"100 / мес": 100,
		"(100)":     100,
		"100 x":     100,
		"100 (usd)": 100,
	} {
		got, err := ParseAmount(input)
		if err != nil {
			t.Fatalf("ParseAmount(%q): %v", input, err)
		}
		if got != want {
			t.Fatalf("ParseAmount(%q) = %v, want %v", input, got, want)
		}
	}
}

func TestParseAmountDatesAndPhonesAreNotSubtraction(t *testing.T) {
	for _, input := range []string{
		"12-05-2024",
		"2024-05-12",
		"12/05/2024",
		"12/05",
		"01-12",
		"8-800-555-35-35",
		"+7 999 123-45-67",
		"+7 (999) 123-45-67",
		"10-15 usd",
		"100-200",
		"2024-05-12 + 1",
		"10:30",
	} {
		_, err := ParseAmount(input)
		var ambiguous *AmbiguousError
		if !errors.As(err, &ambiguous) {
			t.Fatalf("ParseAmount(%q) error = %v, want AmbiguousError", input, err)
		}
	}
}

func TestParseAmountSeveralExpressionsAreAmbiguous(t *testing.T) {
	for _, input := range []string{
		"100+50 usd за 2 дня",
		"2 кофе по 350 + 1",
	} {
		_, err := ParseAmount(input)
		var ambiguous *AmbiguousError
		if !errors.As(err, &ambiguous) {
			t.Fatalf("ParseAmount(%q) error = %v, want AmbiguousError", input, err)
		}
	}
}

func TestParseAmountExpressionErrors(t *testing.T) {
	deep := strings.Repeat("(", MaxExpressionDepth+1) + "1" + strings.Repeat(")", MaxExpressionDepth+1) + "+1"
	notTooDeep := strings.Repeat("(", MaxExpressionDepth) + "1" + strings.Repeat(")", MaxExpressionDepth) + "+1"
	long := strings.Repeat("1+", MaxExpressionLength/2) + "1"

	tests := []struct {
		input string
		want  error
	}{
		{"1/0", ErrDivisionByZero},
		{"100 / (5 - 5)", ErrDivisionByZero},
		{"50 - 100", ErrNegativeAmount},
		{"999 999 999 999 999 * 10", ErrTooLarge},
		{"99999999999999999999 - 1", ErrTooLarge},
		{deep, ErrExpressionTooComplex},
		{long, ErrExpressionTooComplex},
	}
	for _, tt := range tests {
		if _, err := ParseAmount(tt.input); !errors.Is(err, tt.want) {
			t.Fatalf("ParseAmount(%q) error = %v, want %v", tt.input, err, tt.want)
		}
	}

	if got, err := ParseAmount(notTooDeep); err != nil || got != 2 {
		t.Fatalf("ParseAmount(%q) = %v, %v, want 2", notTooDeep, got, err)
	}
	// A long line of text around a short expression is fine.
	if got, err := ParseAmount(strings.Repeat("слово ", 100) + "2+2"); err != nil || got != 4 {
		t.Fatalf("ParseAmount(long text + 2+2) = %v, %v, want 4", got, err)
	}
}

func TestParseAmountsSumsExpressionLines(t *testing.T) {
	total, count, err := ParseAmounts("100+50\n70 х 5 литров молока\n(12*3)+5\n200")
	if err != nil {
		t.Fatalf("ParseAmounts() error = %v", err)
	}
	if total != 741 || count != 4 {
		t.Fatalf("ParseAmounts() = %v, %d, want 741, 4", total, count)
	}
	if _, _, err := ParseAmounts("100\n1/0"); !errors.Is(err, ErrDivisionByZero) {
		t.Fatalf("ParseAmounts() error = %v, want ErrDivisionByZero", err)
	}
}

func FuzzParseAmount(f *testing.F) {
	for _, seed := range []string{"100+50 usd", "(12*3)+5", "1 000 / 4", "70 х 5 литров", "12-05-2024", "1/0", "((((1))))", "100 usd x 9"} {
		f.Add(seed, false)
	}
	f.Fuzz(func(t *testing.T, input string, commaThousands bool) {
		value, err := ParseAmountWith(input, Options{CommaThousands: commaThousands})
		if err == nil && (value < 0 || value > MaxAmount || math.IsNaN(value)) {
			t.Fatalf("ParseAmountWith(%q) = %v, want a value in [0, MaxAmount]", input, value)
		}
	})
}
