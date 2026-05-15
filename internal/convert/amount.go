package convert

import (
	"errors"
	"math"
	"strconv"
	"strings"
	"unicode"
)

var ErrNoAmount = errors.New("no amount found")

func ParseAmount(input string) (float64, error) {
	if value, ok := parseMultiplication(input); ok {
		return value, nil
	}
	return parsePlainAmount(input)
}

func parsePlainAmount(input string) (float64, error) {
	var cleaned []rune
	separatorIndex := -1

	for _, r := range input {
		switch {
		case unicode.IsDigit(r):
			cleaned = append(cleaned, r)
		case (r == '.' || r == ',') && len(cleaned) > 0:
			cleaned = append(cleaned, '.')
			separatorIndex = len(cleaned) - 1
		}
	}

	if len(cleaned) == 0 {
		return 0, ErrNoAmount
	}

	var normalized []rune
	for i, r := range cleaned {
		if r == '.' && i != separatorIndex {
			continue
		}
		normalized = append(normalized, r)
	}

	value, err := strconv.ParseFloat(string(normalized), 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, ErrNoAmount
	}
	return value, nil
}

func parseMultiplication(input string) (float64, bool) {
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

		leftValue, err := parsePlainAmount(left)
		if err != nil {
			continue
		}
		rightValue, err := parsePlainAmount(right)
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
	var total float64
	count := 0

	for _, line := range strings.Split(input, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		amount, err := ParseAmount(line)
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
