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
		return convert.GroupDigits(whole)
	}
	return convert.GroupDigits(whole) + "," + fraction
}

func groupWholeNumber(value float64) string {
	return convert.GroupDigits(strconv.FormatFloat(math.Round(value), 'f', 0, 64))
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
