package convert

import (
	"errors"
	"math"
	"strconv"
	"strings"
	"unicode"
)

var ErrNoAmount = errors.New("no amount found")

// Options tunes how ambiguous number formats are read.
type Options struct {
	// CommaThousands reads a single comma followed by exactly three digits as a
	// thousands separator ("1,000" = 1000, English style). When false, such a
	// comma is decimal ("1,000" = 1, Russian style).
	CommaThousands bool
}

func ParseAmount(input string) (float64, error) {
	return ParseAmountWith(input, Options{})
}

func ParseAmountWith(input string, opts Options) (float64, error) {
	if value, ok := parseMultiplication(input, opts); ok {
		return value, nil
	}
	return parsePlainAmount(input, opts)
}

func parsePlainAmount(input string, opts Options) (float64, error) {
	var cleaned []rune
	for _, r := range input {
		switch {
		case unicode.IsDigit(r):
			cleaned = append(cleaned, r)
		case isSeparator(r) && len(cleaned) > 0:
			cleaned = append(cleaned, r)
		}
	}
	for len(cleaned) > 0 && isSeparator(cleaned[len(cleaned)-1]) {
		cleaned = cleaned[:len(cleaned)-1]
	}
	if len(cleaned) == 0 {
		return 0, ErrNoAmount
	}

	value, err := strconv.ParseFloat(normalizeSeparators(cleaned, opts), 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, ErrNoAmount
	}
	return value, nil
}

// normalizeSeparators turns digits with '.'/',' separators into a string that
// strconv.ParseFloat understands. By default the last separator is decimal and
// the others group thousands; repeated identical separators in groups of three
// ("1,000,000", "1.000.000") are all treated as thousands separators.
func normalizeSeparators(cleaned []rune, opts Options) string {
	var separators []int
	for i, r := range cleaned {
		if isSeparator(r) {
			separators = append(separators, i)
		}
	}
	if len(separators) == 0 {
		return string(cleaned)
	}

	decimal := separators[len(separators)-1]
	if thousandsGrouped(cleaned, separators) {
		multiple := len(separators) > 1
		englishThousands := opts.CommaThousands && cleaned[decimal] == ','
		if multiple || englishThousands {
			decimal = -1
		}
	}

	var normalized []rune
	for i, r := range cleaned {
		switch {
		case i == decimal:
			normalized = append(normalized, '.')
		case isSeparator(r):
		default:
			normalized = append(normalized, r)
		}
	}
	return string(normalized)
}

// thousandsGrouped reports whether all separators are the same character and
// split the number into a 1-3 digit head (not starting with 0) followed by
// groups of exactly three digits.
func thousandsGrouped(cleaned []rune, separators []int) bool {
	first := separators[0]
	if first == 0 || first > 3 || cleaned[0] == '0' {
		return false
	}
	for n, index := range separators {
		if cleaned[index] != cleaned[first] {
			return false
		}
		end := len(cleaned)
		if n+1 < len(separators) {
			end = separators[n+1]
		}
		if end-index-1 != 3 {
			return false
		}
	}
	return true
}

func isSeparator(r rune) bool {
	return r == '.' || r == ','
}

func parseMultiplication(input string, opts Options) (float64, bool) {
	runes := []rune(input)
	for i, r := range runes {
		if !isMultiplicationSign(r) {
			continue
		}

		left, ok := leftOperand(runes, i)
		if !ok {
			continue
		}
		right, ok := rightOperand(runes, i+1)
		if !ok {
			continue
		}

		leftValue, err := parsePlainAmount(left, opts)
		if err != nil {
			continue
		}
		rightValue, err := parsePlainAmount(right, opts)
		if err != nil {
			continue
		}
		value := leftValue * rightValue
		if math.IsNaN(value) || math.IsInf(value, 0) {
			continue
		}
		return value, true
	}
	return 0, false
}

func leftOperand(runes []rune, operatorIndex int) (string, bool) {
	i := operatorIndex - 1
	for i >= 0 && unicode.IsSpace(runes[i]) {
		i--
	}
	if i < 0 || !unicode.IsDigit(runes[i]) {
		return "", false
	}

	end := i + 1
	for i >= 0 && isAmountRune(runes[i]) {
		i--
	}
	return string(runes[i+1 : end]), true
}

func rightOperand(runes []rune, startIndex int) (string, bool) {
	i := startIndex
	for i < len(runes) && unicode.IsSpace(runes[i]) {
		i++
	}
	if i >= len(runes) || !unicode.IsDigit(runes[i]) {
		return "", false
	}

	start := i
	for i < len(runes) && isAmountRune(runes[i]) {
		i++
	}
	return string(runes[start:i]), true
}

func isMultiplicationSign(r rune) bool {
	return r == '*' || r == 'x' || r == 'X' || r == 'х' || r == 'Х'
}

func isAmountRune(r rune) bool {
	return unicode.IsDigit(r) || r == '.' || r == ',' || unicode.IsSpace(r)
}

func ParseAmounts(input string) (float64, int, error) {
	return ParseAmountsWith(input, Options{})
}

func ParseAmountsWith(input string, opts Options) (float64, int, error) {
	var total float64
	count := 0

	for _, line := range strings.Split(input, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		amount, err := ParseAmountWith(line, opts)
		if err != nil {
			if errors.Is(err, ErrNoAmount) {
				continue
			}
			return 0, 0, err
		}
		total += amount
		count++
	}

	if count == 0 {
		return 0, 0, ErrNoAmount
	}
	return total, count, nil
}

func FormatMoney(value float64) string {
	sign := ""
	if value < 0 {
		sign = "-"
		value = -value
	}

	rounded := math.Round(value*100) / 100
	whole := int64(rounded)
	fraction := int(math.Round((rounded - float64(whole)) * 100))
	if fraction == 100 {
		whole++
		fraction = 0
	}

	return sign + groupInt(whole) + "," + twoDigits(fraction)
}

func groupInt(value int64) string {
	raw := strconv.FormatInt(value, 10)
	if len(raw) <= 3 {
		return raw
	}

	var b strings.Builder
	firstGroup := len(raw) % 3
	if firstGroup == 0 {
		firstGroup = 3
	}
	b.WriteString(raw[:firstGroup])
	for i := firstGroup; i < len(raw); i += 3 {
		b.WriteByte(' ')
		b.WriteString(raw[i : i+3])
	}
	return b.String()
}

func twoDigits(value int) string {
	if value < 10 {
		return "0" + strconv.Itoa(value)
	}
	return strconv.Itoa(value)
}
