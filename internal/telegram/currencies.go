package telegram

import "strings"

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

func isSupportedCurrency(code string) bool {
	code = strings.ToUpper(strings.TrimSpace(code))
	for _, currency := range supportedCurrencies {
		if currency.Code == code {
			return true
		}
	}
	return false
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
