package telegram

import (
	"strings"
	"unicode"
)

type currencyInfo struct {
	Code    string
	Name    string
	Country string
}

var supportedCurrencies = []currencyInfo{
	{Code: "RUB", Name: "российский рубль", Country: "Россия"},
	{Code: "USD", Name: "доллар США", Country: "США"},
	{Code: "EUR", Name: "евро", Country: "страны еврозоны"},
	{Code: "GBP", Name: "фунт стерлингов", Country: "Великобритания"},
	{Code: "CHF", Name: "швейцарский франк", Country: "Швейцария"},
	{Code: "CNY", Name: "китайский юань", Country: "Китай"},
	{Code: "JPY", Name: "японская иена", Country: "Япония"},
	{Code: "KRW", Name: "южнокорейская вона", Country: "Республика Корея"},
	{Code: "TRY", Name: "турецкая лира", Country: "Турция"},
	{Code: "AED", Name: "дирхам ОАЭ", Country: "ОАЭ"},
	{Code: "KZT", Name: "казахстанский тенге", Country: "Казахстан"},
	{Code: "BYN", Name: "белорусский рубль", Country: "Беларусь"},
	{Code: "AMD", Name: "армянский драм", Country: "Армения"},
	{Code: "GEL", Name: "грузинский лари", Country: "Грузия"},
	{Code: "KGS", Name: "киргизский сом", Country: "Киргизия"},
	{Code: "TJS", Name: "таджикский сомони", Country: "Таджикистан"},
	{Code: "UZS", Name: "узбекский сум", Country: "Узбекистан"},
	{Code: "TMT", Name: "туркменский манат", Country: "Туркменистан"},
	{Code: "AZN", Name: "азербайджанский манат", Country: "Азербайджан"},
	{Code: "MDL", Name: "молдавский лей", Country: "Молдова"},
	{Code: "UAH", Name: "украинская гривна", Country: "Украина"},
	{Code: "PLN", Name: "польский злотый", Country: "Польша"},
	{Code: "CZK", Name: "чешская крона", Country: "Чехия"},
	{Code: "HUF", Name: "венгерский форинт", Country: "Венгрия"},
	{Code: "RON", Name: "румынский лей", Country: "Румыния"},
	{Code: "BGN", Name: "болгарский лев", Country: "Болгария"},
	{Code: "RSD", Name: "сербский динар", Country: "Сербия"},
	{Code: "SEK", Name: "шведская крона", Country: "Швеция"},
	{Code: "NOK", Name: "норвежская крона", Country: "Норвегия"},
	{Code: "DKK", Name: "датская крона", Country: "Дания"},
	{Code: "CAD", Name: "канадский доллар", Country: "Канада"},
	{Code: "AUD", Name: "австралийский доллар", Country: "Австралия"},
	{Code: "NZD", Name: "новозеландский доллар", Country: "Новая Зеландия"},
	{Code: "SGD", Name: "сингапурский доллар", Country: "Сингапур"},
	{Code: "HKD", Name: "гонконгский доллар", Country: "Гонконг"},
	{Code: "INR", Name: "индийская рупия", Country: "Индия"},
	{Code: "IDR", Name: "индонезийская рупия", Country: "Индонезия"},
	{Code: "THB", Name: "тайский бат", Country: "Таиланд"},
	{Code: "VND", Name: "вьетнамский донг", Country: "Вьетнам"},
	{Code: "QAR", Name: "катарский риал", Country: "Катар"},
	{Code: "EGP", Name: "египетский фунт", Country: "Египет"},
	{Code: "BRL", Name: "бразильский реал", Country: "Бразилия"},
	{Code: "ZAR", Name: "южноафриканский рэнд", Country: "ЮАР"},
}

var currencyAliases = map[string]string{
	"$":        "USD",
	"usd":      "USD",
	"доллар":   "USD",
	"доллара":  "USD",
	"доллары":  "USD",
	"долларов": "USD",
	"бакс":     "USD",
	"бакса":    "USD",
	"баксов":   "USD",

	"€":    "EUR",
	"eur":  "EUR",
	"евро": "EUR",

	"₽":      "RUB",
	"rub":    "RUB",
	"руб":    "RUB",
	"рубль":  "RUB",
	"рубля":  "RUB",
	"рубли":  "RUB",
	"рублей": "RUB",

	"£":      "GBP",
	"gbp":    "GBP",
	"фунт":   "GBP",
	"фунта":  "GBP",
	"фунтов": "GBP",

	"¥":     "CNY",
	"cny":   "CNY",
	"юань":  "CNY",
	"юаня":  "CNY",
	"юаней": "CNY",

	"jpy":  "JPY",
	"иена": "JPY",
	"иены": "JPY",
	"йена": "JPY",
	"йены": "JPY",

	"uzs":   "UZS",
	"сум":   "UZS",
	"сума":  "UZS",
	"сумов": "UZS",
	"sum":   "UZS",
	"som":   "UZS",
	"so'm":  "UZS",
	"so’m":  "UZS",

	"kzt":   "KZT",
	"тенге": "KZT",

	"kgs":   "KGS",
	"сом":   "KGS",
	"сома":  "KGS",
	"сомов": "KGS",

	"try":  "TRY",
	"лира": "TRY",
	"лиры": "TRY",
	"лир":  "TRY",

	"aed":      "AED",
	"дирхам":   "AED",
	"дирхама":  "AED",
	"дирхамов": "AED",
}

func isSupportedCurrency(code string) bool {
	code = strings.ToUpper(strings.TrimSpace(code))
	for _, currency := range supportedCurrencies {
		if currency.Code == code {
			return true
		}
	}
	return false
}

func resolveCurrencyToken(raw string) (string, bool) {
	token := normalizeCurrencyToken(raw)
	if token == "" {
		return "", false
	}
	if code, ok := currencyAliases[token]; ok {
		return code, true
	}
	code := strings.ToUpper(token)
	if isSupportedCurrency(code) {
		return code, true
	}
	return "", false
}

func currencyCodesFromText(text string) []string {
	var codes []string
	for _, token := range currencyTokens(text) {
		code, ok := resolveCurrencyToken(token)
		if ok {
			codes = append(codes, code)
		}
	}
	return codes
}

func firstUnknownCurrencyCodeToken(text string) string {
	for _, token := range currencyTokens(text) {
		normalized := normalizeCurrencyToken(token)
		if normalized == "" {
			continue
		}
		if _, ok := resolveCurrencyToken(normalized); ok {
			continue
		}
		if isCurrencyStopWord(normalized) {
			continue
		}
		if isASCIIAlphaCode(normalized) {
			return strings.ToUpper(normalized)
		}
	}
	return ""
}

func currencyTokens(text string) []string {
	var tokens []string
	var current strings.Builder
	flush := func() {
		if current.Len() == 0 {
			return
		}
		tokens = append(tokens, current.String())
		current.Reset()
	}

	for _, r := range text {
		switch {
		case isCurrencySymbol(r):
			flush()
			tokens = append(tokens, string(r))
		case unicode.IsLetter(r) || r == '\'' || r == '’':
			current.WriteRune(unicode.ToLower(r))
		default:
			flush()
		}
	}
	flush()
	return tokens
}

func isCurrencyStopWord(value string) bool {
	switch value {
	case "for", "per", "via":
		return true
	default:
		return false
	}
}

func isASCIIAlphaCode(value string) bool {
	if len(value) != 3 {
		return false
	}
	for _, r := range value {
		if r < 'a' || r > 'z' {
			return false
		}
	}
	return true
}

func normalizeCurrencyToken(raw string) string {
	token := strings.ToLower(strings.TrimSpace(raw))
	token = strings.TrimPrefix(token, "/")
	token = strings.Trim(token, " \t\r\n.,:;!?()[]{}\"`")
	token = strings.ReplaceAll(token, "ё", "е")
	return token
}

func isCurrencySymbol(r rune) bool {
	switch r {
	case '$', '€', '₽', '£', '¥':
		return true
	default:
		return false
	}
}

func supportedCurrenciesText() string {
	var b strings.Builder
	b.WriteString("Поддерживаемые валюты. Код пишите так: /from USD или /to EUR\n\n")
	for _, currency := range supportedCurrencies {
		b.WriteString(currency.Code)
		b.WriteString(" - ")
		b.WriteString(currency.Name)
		b.WriteString(" (")
		b.WriteString(currency.Country)
		b.WriteString(")\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
