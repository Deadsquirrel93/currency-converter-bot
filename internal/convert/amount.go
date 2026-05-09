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
	var cleaned []rune
	separatorIndex := -1

	for _, r := range input {
		switch {
		case unicode.IsDigit(r):
			cleaned = append(cleaned, r)
		case r == '.' || r == ',':
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
