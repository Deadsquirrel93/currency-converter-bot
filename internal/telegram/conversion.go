package telegram

import (
	"context"
	"currency-converter-bot/internal/convert"
	"currency-converter-bot/internal/rates"
	"errors"
	"fmt"
	"html"
	"math"
	"strconv"
	"strings"
)

func (b *Bot) convertMessage(ctx context.Context, chatID, userID int64, text string) {
	s := b.getSession(userID)
	language := b.userLanguage(userID)
	request, err := parseConversionInput(text, s)
	if err != nil {
		_ = b.sendMessage(ctx, chatID, amountErrorText(err, language))
		return
	}

	snapshot, err := b.rates.Get(ctx)
	if err != nil {
		b.log.Error("rates unavailable", "error", err)
		_ = b.sendMessage(ctx, chatID, ratesUnavailableText(language))
		return
	}

	settings := conversionSettingsForInput(s, request)
	reply, err := conversionReply(request.Amount, request.AmountCount, request.From, request.To, settings.Multiplier, settings.ModifyFromPercent, settings.ModifyToPercent, settings.UseModify, s.Round, snapshot, language)
	if err != nil {
		_ = b.sendMessage(ctx, chatID, checkCurrenciesText(language, errorText(err, language)))
		return
	}

	var markup *inlineKeyboardMarkup
	if len(s.With) > 0 {
		buttonSession := s
		buttonSession.From = request.From
		buttonSession.Multiplier = settings.Multiplier
		markup = withReplyMarkup(request.Amount, buttonSession, language)
	}
	_ = b.sendMessageWithMarkup(ctx, chatID, reply, markup, "HTML")
}

type conversionInput struct {
	Amount      float64
	AmountCount int
	From        string
	To          string
	Inline      bool
}

func parseConversionInput(text string, s session) (conversionInput, error) {
	options := convert.Options{CommaThousands: normalizeLanguage(s.Language) == languageEnglish}
	amount, amountCount, err := convert.ParseAmounts(text, options)
	if err != nil {
		return conversionInput{}, err
	}

	s = normalizeSession(s, "", "")
	from := s.From
	to := s.To
	codes := currencyCodesFromText(text)
	switch len(codes) {
	case 0:
	case 1:
		from = codes[0]
	default:
		from = codes[0]
		to = codes[1]
	}

	return conversionInput{
		Amount:      amount,
		AmountCount: amountCount,
		From:        from,
		To:          to,
		Inline:      len(codes) > 0,
	}, nil
}

type conversionSettings struct {
	Multiplier        float64
	ModifyFromPercent float64
	ModifyToPercent   float64
	UseModify         bool
}

func conversionSettingsForInput(s session, input conversionInput) conversionSettings {
	s = normalizeSession(s, "", "")
	settings := conversionSettings{
		Multiplier:        s.Multiplier,
		ModifyFromPercent: s.ModifyFromPercent,
		ModifyToPercent:   s.ModifyToPercent,
		UseModify:         true,
	}
	if input.Inline {
		settings.Multiplier = 1
		settings.UseModify = s.InlineModify
	}
	return settings
}

func applyPercent(value, percent float64) float64 {
	return value * (1 + percent/100)
}

func conversionReply(amount float64, amountCount int, from, to string, multiplier, modifyFromPercent, modifyToPercent float64, useModify bool, roundMode string, snapshot rates.Snapshot, language string) (string, error) {
	multipliedAmount := amount * multiplier
	effectiveAmount := multipliedAmount
	if useModify {
		effectiveAmount = applyPercent(multipliedAmount, modifyFromPercent)
	}
	baseResult, err := rates.Convert(effectiveAmount, from, to, snapshot)
	if err != nil {
		return "", err
	}
	result := baseResult
	if useModify {
		result = applyPercent(baseResult, modifyToPercent)
	}

	rawUnitRate, err := rates.Convert(1, from, to, snapshot)
	if err != nil {
		return "", err
	}
	unitRate := rawUnitRate
	unitRate *= multiplier
	if useModify {
		unitRate = applyPercent(applyPercent(unitRate, modifyFromPercent), modifyToPercent)
	}

	amountText := formatAmountWithSettings(amount, multipliedAmount, effectiveAmount, from)
	resultText := fmt.Sprintf("%s %s", formatConvertedAmountForMode(result, roundMode), to)
	replyPrefix := fmt.Sprintf("%s = <b>%s</b>", html.EscapeString(amountText), html.EscapeString(resultText))
	if amountCount > 1 {
		replyPrefix = fmt.Sprintf(tr(language, "Итого: %s = <b>%s</b>\nСтрок учтено: %d", "Total: %s = <b>%s</b>\nLines included: %d"), html.EscapeString(amountText), html.EscapeString(resultText), amountCount)
	}

	lines := []string{
		replyPrefix,
		fmt.Sprintf(tr(language, "Курс: 1 %s = %s %s", "Rate: 1 %s = %s %s"), html.EscapeString(from), html.EscapeString(formatRate(rawUnitRate)), html.EscapeString(to)),
	}
	if suffix := convenientRateSuffix(from, to, rawUnitRate); suffix != "" {
		lines = append(lines, html.EscapeString(strings.TrimPrefix(suffix, "\n")))
	}
	if multiplier != 1 || (useModify && (modifyFromPercent != 0 || modifyToPercent != 0)) {
		lines = append(lines, fmt.Sprintf(tr(language, "Расчет для 1 введенной единицы = %s %s", "Result for 1 entered unit = %s %s"), html.EscapeString(formatConvertedAmountForMode(unitRate, roundMode)), html.EscapeString(to)))
	}
	return strings.Join(lines, "\n"), nil
}

func formatAmountWithSettings(amount, multipliedAmount, effectiveAmount float64, currency string) string {
	if amount == multipliedAmount && amount == effectiveAmount {
		return fmt.Sprintf("%s %s", convert.FormatMoney(amount), currency)
	}
	if multipliedAmount == effectiveAmount {
		return fmt.Sprintf("%s (%s) %s", convert.FormatMoney(amount), convert.FormatMoney(multipliedAmount), currency)
	}
	if amount == multipliedAmount {
		return fmt.Sprintf("%s (%s) %s", convert.FormatMoney(amount), convert.FormatMoney(effectiveAmount), currency)
	}
	return fmt.Sprintf("%s (%s -> %s) %s", convert.FormatMoney(amount), convert.FormatMoney(multipliedAmount), convert.FormatMoney(effectiveAmount), currency)
}

func withReplyMarkup(amount float64, s session, language string) *inlineKeyboardMarkup {
	buttons := make([]inlineKeyboardButton, 0, len(s.With))
	for _, to := range s.With {
		data, ok := withCallbackData(amount, s, to)
		if !ok {
			continue
		}
		buttons = append(buttons, inlineKeyboardButton{Text: tr(language, "в ", "to ") + to, CallbackData: data})
	}
	if len(buttons) == 0 {
		return nil
	}

	return &inlineKeyboardMarkup{
		InlineKeyboard: [][]inlineKeyboardButton{
			buttons,
		},
	}
}

func inlineConversionResult(reply, language string) inlineQueryResultArticle {
	plain := stripTelegramHTML(reply)
	title := firstLine(plain)
	if title == "" {
		title = tr(language, "Конвертация", "Conversion")
	}
	return inlineQueryResultArticle{
		Type:        "article",
		ID:          "conversion",
		Title:       title,
		Description: strings.ReplaceAll(plain, "\n", " · "),
		InputMessageContent: inputTextMessageContent{
			MessageText: reply,
			ParseMode:   "HTML",
		},
	}
}

func stripTelegramHTML(text string) string {
	text = strings.ReplaceAll(text, "<b>", "")
	text = strings.ReplaceAll(text, "</b>", "")
	return html.UnescapeString(text)
}

func firstLine(text string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	return line
}

func withCallbackData(amount float64, s session, to string) (string, bool) {
	parts := []string{
		"w",
		s.From,
		to,
		formatFloatForCallback(amount),
		formatFloatForCallback(s.Multiplier),
		"0",
	}
	if s.WithModify {
		parts[5] = "1"
		parts = append(parts, formatFloatForCallback(s.ModifyFromPercent), formatFloatForCallback(s.ModifyToPercent))
	}
	data := strings.Join(parts, "|")
	return data, len(data) <= 64
}

type withCallbackRequest struct {
	From              string
	To                string
	Amount            float64
	Multiplier        float64
	UseModify         bool
	ModifyFromPercent float64
	ModifyToPercent   float64
}

func parseWithCallbackData(data string) (withCallbackRequest, error) {
	parts := strings.Split(data, "|")
	if len(parts) != 6 && len(parts) != 8 {
		return withCallbackRequest{}, errors.New("invalid callback data")
	}
	if parts[0] != "w" {
		return withCallbackRequest{}, errors.New("invalid callback type")
	}
	from := strings.ToUpper(strings.TrimSpace(parts[1]))
	to := strings.ToUpper(strings.TrimSpace(parts[2]))
	if !isSupportedCurrency(from) || !isSupportedCurrency(to) {
		return withCallbackRequest{}, errors.New("invalid callback currency")
	}
	amount, err := parseCallbackFloat(parts[3])
	if err != nil {
		return withCallbackRequest{}, err
	}
	// Callback data comes back from the client and can be forged.
	if amount < 0 || amount > convert.MaxAmount {
		return withCallbackRequest{}, errors.New("invalid callback amount")
	}
	multiplier, err := parseMultiplier(parts[4])
	if err != nil {
		return withCallbackRequest{}, err
	}
	useModify := parts[5] == "1"

	request := withCallbackRequest{
		From:       from,
		To:         to,
		Amount:     amount,
		Multiplier: multiplier,
		UseModify:  useModify,
	}
	if useModify {
		if len(parts) != 8 {
			return withCallbackRequest{}, errors.New("missing modifiers")
		}
		request.ModifyFromPercent, err = parseModifierPercent(parts[6])
		if err != nil {
			return withCallbackRequest{}, err
		}
		request.ModifyToPercent, err = parseModifierPercent(parts[7])
		if err != nil {
			return withCallbackRequest{}, err
		}
	}
	return request, nil
}

func parseCallbackFloat(raw string) (float64, error) {
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, errors.New("invalid callback number")
	}
	return value, nil
}

func formatFloatForCallback(value float64) string {
	return strconv.FormatFloat(value, 'g', -1, 64)
}

func amountErrorText(err error, language string) string {
	var ambiguous *convert.AmbiguousError
	switch {
	case errors.As(err, &ambiguous):
		numbers := strings.Join(ambiguous.Numbers, ", ")
		if len(ambiguous.Numbers) == 2 {
			hint := ambiguous.Numbers[0] + " x " + ambiguous.Numbers[1]
			return fmt.Sprintf(tr(language,
				"В строке несколько чисел (%s), не понимаю, какое переводить. Оставьте одно число или напишите %s, если нужно перемножить.",
				"The line has several numbers (%s), so I am not sure which one to convert. Keep one number, or write %s to multiply them."), numbers, hint)
		}
		return fmt.Sprintf(tr(language,
			"В строке несколько чисел (%s), не понимаю, какое переводить. Оставьте одно число в строке.",
			"The line has several numbers (%s), so I am not sure which one to convert. Keep one number per line."), numbers)
	case errors.Is(err, convert.ErrTooLarge):
		return tr(language, "Слишком большая сумма.", "The amount is too large.")
	case errors.Is(err, convert.ErrDivisionByZero):
		return tr(language, "В выражении деление на ноль. Проверьте его.", "The expression divides by zero. Please check it.")
	case errors.Is(err, convert.ErrNegativeAmount):
		return tr(language, "Сумма получилась отрицательной. Проверьте выражение.", "The amount came out negative. Please check the expression.")
	case errors.Is(err, convert.ErrExpressionTooComplex):
		return fmt.Sprintf(tr(language,
			"Слишком сложное выражение: не длиннее %d символов и не больше %d уровней скобок.",
			"The expression is too complex: keep it within %d characters and %d levels of parentheses."), convert.MaxExpressionLength, convert.MaxExpressionDepth)
	default:
		return tr(language, "Не вижу сумму. Например: 12 345,67 или несколько сумм, каждая с новой строки.", "I cannot find an amount. For example: 12,345.67, or several amounts on separate lines.")
	}
}
