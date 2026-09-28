package telegram

import (
	"currency-converter-bot/internal/convert"
	"math"
	"strconv"
	"strings"
)

func formatRate(value float64) string {
	if math.Abs(value) >= 1 {
		return convert.FormatMoney(value)
	}
	return formatSmallDecimal(value, 8)
}

func formatConvertedAmount(value float64) string {
	if value == 0 || math.Abs(value) >= 0.01 {
		return convert.FormatMoney(value)
	}
	return formatSmallDecimal(value, 8)
}

func formatConvertedAmountForMode(value float64, roundMode string) string {
	precision, ok := roundPrecision(roundMode)
	if !ok {
		return formatConvertedAmount(value)
	}
	return formatFixedDecimal(value, precision)
}

func formatFixedDecimal(value float64, precision int) string {
	if precision <= 0 {
		return groupWholeNumber(value)
	}
	formatted := strconv.FormatFloat(value, 'f', precision, 64)
	whole, fraction, ok := strings.Cut(formatted, ".")
	if !ok {
		return groupDigits(whole)
	}
	return groupDigits(whole) + "," + fraction
}

func groupWholeNumber(value float64) string {
	rounded := math.Round(value)
	return groupDigits(strconv.FormatInt(int64(rounded), 10))
}

func groupDigits(raw string) string {
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

func formatSmallDecimal(value float64, precision int) string {
	formatted := strconv.FormatFloat(value, 'f', precision, 64)
	formatted = strings.TrimRight(formatted, "0")
	formatted = strings.TrimRight(formatted, ".")
	if formatted == "" || formatted == "-0" {
		formatted = "0"
	}
	return strings.ReplaceAll(formatted, ".", ",")
}

func formatNumber(value float64) string {
	formatted := convert.FormatMoney(value)
	formatted = strings.TrimRight(formatted, "0")
	return strings.TrimRight(formatted, ",")
}

func formatPercent(value float64) string {
	formatted := formatNumber(value)
	if value > 0 {
		return "+" + formatted + "%"
	}
	return formatted + "%"
}
