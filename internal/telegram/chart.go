package telegram

import (
	"context"
	"fmt"
	"strings"
	"time"

	"currency-converter-bot/internal/chart"
	"currency-converter-bot/internal/rates"
)

const chartCallbackPrefix = "chart:"

func (b *Bot) showChart(ctx context.Context, chatID, userID int64, text string) {
	language := b.userLanguage(userID)
	request, err := parseRateRequest(text, b.getSession(userID))
	if err != nil {
		_ = b.sendMessage(ctx, chatID, errorText(err, language))
		return
	}
	b.sendChart(ctx, chatID, request.From, request.To, language, b.userLocation(userID))
}

// chartButtonMarkup is the "chart" button under /rate.
func chartButtonMarkup(from, to, language string) *inlineKeyboardMarkup {
	return &inlineKeyboardMarkup{InlineKeyboard: [][]inlineKeyboardButton{{
		{Text: tr(language, "📈 График за 30 дней", "📈 30-day chart"), CallbackData: chartCallbackPrefix + from + ":" + to},
	}}}
}

// parseChartCallbackData reads "chart:USD:RUB". Callback data comes back from
// the client and can be forged, so the codes are checked.
func parseChartCallbackData(data string) (string, string, bool) {
	rest, ok := strings.CutPrefix(data, chartCallbackPrefix)
	if !ok {
		return "", "", false
	}
	from, to, ok := strings.Cut(rest, ":")
	if !ok || !isSupportedCurrency(from) || !isSupportedCurrency(to) {
		return "", "", false
	}
	return strings.ToUpper(from), strings.ToUpper(to), true
}

// sendChart sends a PNG chart of the last 30 days, counted in location like
// the /rate history, with the summary in the caption.
func (b *Bot) sendChart(ctx context.Context, chatID int64, from, to, language string, location *time.Location) {
	_ = b.sendChatAction(ctx, chatID, "upload_photo")
	snapshot, err := b.rates.Get(ctx)
	if err != nil {
		b.log.Error("rates unavailable", "error", err)
		_ = b.sendMessage(ctx, chatID, ratesUnavailableText(language))
		return
	}
	if _, err := rates.Convert(1, from, to, snapshot); err != nil {
		_ = b.sendMessage(ctx, chatID, checkCurrenciesText(language, errorText(err, language)))
		return
	}

	history := b.subscriptionHistoricalSnapshots(ctx, snapshot, time.Now().In(location))
	points := make([]chart.Point, 0, len(history))
	for i := range history {
		if point := subscriptionRatePointFromSnapshot(from, to, &history[i]); point != nil {
			points = append(points, chart.Point{Date: point.Date, Value: point.Rate})
		}
	}
	image, err := chart.Render(from+"/"+to, points)
	if err != nil {
		b.log.Warn("render chart failed", "from", from, "to", to, "points", len(points), "error", err)
		_ = b.sendMessage(ctx, chatID, tr(language, "Не хватает данных ЦБ для графика. Попробуйте чуть позже.", "Not enough Bank of Russia data for a chart. Please try again later."))
		return
	}
	if err := b.sendPhoto(ctx, chatID, image, chartCaption(from, to, points, language), nil); err != nil {
		b.log.Warn("send chart failed", "chat_id", chatID, "error", err)
	}
}

// chartCaption summarizes the chart in the user's language.
func chartCaption(from, to string, points []chart.Point, language string) string {
	newest, oldest, low, high := points[0], points[0], points[0], points[0]
	for _, p := range points {
		if p.Date.After(newest.Date) {
			newest = p
		}
		if p.Date.Before(oldest.Date) {
			oldest = p
		}
		if p.Value < low.Value {
			low = p
		}
		if p.Value > high.Value {
			high = p
		}
	}
	change := formatRateDeltaForLanguage(newest.Value, &subscriptionRatePoint{Date: oldest.Date, Rate: oldest.Value}, to, language)
	return fmt.Sprintf(
		tr(language,
			"Курс %s -> %s по данным ЦБ, %s – %s\nПоследний: 1 %s = %s %s\nЗа период: %s\nМинимум: %s (%s), максимум: %s (%s)",
			"%s -> %s Bank of Russia rate, %s – %s\nLatest: 1 %s = %s %s\nOver the period: %s\nMinimum: %s (%s), maximum: %s (%s)"),
		from, to, oldest.Date.Format("02.01.2006"), newest.Date.Format("02.01.2006"),
		from, formatRate(newest.Value), to,
		change,
		formatRate(low.Value), low.Date.Format("02.01"), formatRate(high.Value), high.Date.Format("02.01"),
	)
}
