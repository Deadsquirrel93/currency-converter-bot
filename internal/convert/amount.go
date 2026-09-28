package convert

import (
	"errors"
	"math"
	"strconv"
	"strings"
	"unicode"
)

var (
	ErrNoAmount  = errors.New("no amount found")
	ErrTooLarge  = errors.New("amount is too large")
	errNotNumber = errors.New("not a number")
)

// MaxAmount is the largest accepted amount. Beyond it results lose precision
// and stop being meaningful money values.
const MaxAmount = 1e15

// AmbiguousError means a line has several separate numbers, for example
// "iPhone 15 for 1000", so it is unclear which one to convert.
type AmbiguousError struct {
	Numbers []string
}

func (e *AmbiguousError) Error() string {
	return "several numbers in one line: " + strings.Join(e.Numbers, ", ")
}

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
	numbers := numberTokens(input)
	switch len(numbers) {
	case 0:
		return 0, ErrNoAmount
	case 1:
	default:
		return 0, &AmbiguousError{Numbers: numbers}
	}

	value, err := strconv.ParseFloat(normalizeSeparators([]rune(numbers[0]), opts), 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, errNotNumber
	}
	if value > MaxAmount {
		return 0, ErrTooLarge
	}
	return value, nil
}

// numberTokens finds the separate numbers in input. A number is a run of
// digits that may continue through '.' or ',' followed by a digit, or through
// a space followed by exactly three digits ("12 345,67"). Anything else, such
// as letters, ends it: "iPhone 15 за 1000" has two numbers, not 151000.
func numberTokens(input string) []string {
	runes := []rune(input)
	var tokens []string
	for i := 0; i < len(runes); {
		if !unicode.IsDigit(runes[i]) {
			i++
			continue
		}
		var token []rune
	scan:
		for i < len(runes) {
			r := runes[i]
			switch {
			case unicode.IsDigit(r):
				token = append(token, r)
			case isSeparator(r) && i+1 < len(runes) && unicode.IsDigit(runes[i+1]):
				token = append(token, r)
			case isGroupSpace(r) && isGroupOfThree(runes, i+1):
			default:
				break scan
			}
			i++
		}
		tokens = append(tokens, string(token))
	}
	return tokens
}

func isGroupSpace(r rune) bool {
	return r == ' ' || r == '\u00a0' || r == '\u202f'
}

func isGroupOfThree(runes []rune, start int) bool {
	if start+3 > len(runes) {
		return false
	}
	for _, r := range runes[start : start+3] {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return start+3 == len(runes) || !unicode.IsDigit(runes[start+3])
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
		if math.IsNaN(value) || math.IsInf(value, 0) || value > MaxAmount {
			continue
		}
		return value, true
	}
	return 0, false
}

// skipWordBefore steps over one word or currency symbol that stands right
// before a spaced multiplication sign, as in "100 usd x 9" or "100$ x 9".
// A word glued to the sign ("box") is not skipped, so "100 box 2" stays plain.
func skipWordBefore(runes []rune, i int) int {
	if i < 0 || !(unicode.IsLetter(runes[i]) || isCurrencySymbol(runes[i])) {
		return i
	}
	for i >= 0 && (unicode.IsLetter(runes[i]) || isCurrencySymbol(runes[i])) {
		i--
	}
	for i >= 0 && unicode.IsSpace(runes[i]) {
		i--
	}
	return i
}

func isCurrencySymbol(r rune) bool {
	switch r {
	case '$', '€', '₽', '£', '¥':
		return true
	default:
		return false
	}
}

func leftOperand(runes []rune, operatorIndex int) (string, bool) {
	i := operatorIndex - 1
	for i >= 0 && unicode.IsSpace(runes[i]) {
		i--
	}
	if i < operatorIndex-1 {
		i = skipWordBefore(runes, i)
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
			if errors.Is(err, ErrNoAmount) || errors.Is(err, errNotNumber) {
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
	if total > MaxAmount {
		return 0, 0, ErrTooLarge
	}
	return total, count, nil
}

// FormatMoney formats value with two decimals and space-grouped thousands.
// It works on the decimal string, so huge values do not overflow int64.
func FormatMoney(value float64) string {
	sign := ""
	if value < 0 {
		sign = "-"
		value = -value
	}
	formatted := strconv.FormatFloat(math.Round(value*100)/100, 'f', 2, 64)
	whole, fraction, _ := strings.Cut(formatted, ".")
	if whole == "0" && fraction == "00" {
		sign = ""
	}
	return sign + GroupDigits(whole) + "," + fraction
}

// GroupDigits inserts a space between groups of three digits: "1234567" ->
// "1 234 567". A leading minus sign is kept.
func GroupDigits(raw string) string {
	sign := ""
	if strings.HasPrefix(raw, "-") {
		sign = "-"
		raw = strings.TrimPrefix(raw, "-")
	}
	if len(raw) <= 3 {
		return sign + raw
	}
	var b strings.Builder
	firstGroup := len(raw) % 3
	if firstGroup == 0 {
		firstGroup = 3
	}
	b.WriteString(sign)
	b.WriteString(raw[:firstGroup])
	for i := firstGroup; i < len(raw); i += 3 {
		b.WriteByte(' ')
		b.WriteString(raw[i : i+3])
	}
	return b.String()
}
