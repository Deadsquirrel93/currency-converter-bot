package telegram

import (
	"context"
	"currency-converter-bot/internal/rates"
	"fmt"
	"math"
	"strings"
	"time"
)

func (b *Bot) showRate(ctx context.Context, chatID, userID int64, text string) {
	language := b.userLanguage(userID)
	s := b.getSession(userID)
	request, err := parseRateRequest(text, s)
	if err != nil {
		_ = b.sendMessage(ctx, chatID, errorText(err, language))
		return
	}

	b.sendRate(ctx, chatID, request.From, request.To, language)
}

func (b *Bot) sendRate(ctx context.Context, chatID int64, from, to, language string) {
	snapshot, err := b.rates.Get(ctx)
	if err != nil {
		b.log.Error("rates unavailable", "error", err)
		_ = b.sendMessage(ctx, chatID, ratesUnavailableText(language))
		return
	}

	location := b.subscriptionLocation
	if location == nil {
		location = time.Local
	}
	historicalSnapshots := b.subscriptionHistoricalSnapshots(ctx, snapshot, time.Now().In(location))
	reply, err := rateReplyWithHistoryForLanguage(from, to, snapshot, historicalSnapshots, language)
	if err != nil {
		_ = b.sendMessage(ctx, chatID, checkCurrenciesText(language, errorText(err, language)))
		return
	}
	_ = b.sendMessage(ctx, chatID, reply)
}

// historyTimeout bounds how long collecting 30 days of history may take; past
// days are cached by the provider, so normally only a few requests are made.
const historyTimeout = 20 * time.Second

func (b *Bot) subscriptionHistoricalSnapshots(ctx context.Context, current rates.Snapshot, localNow time.Time) []subscriptionHistoricalSnapshot {
	ctx, cancel := context.WithTimeout(ctx, historyTimeout)
	defer cancel()

	localDate := startOfLocalDay(localNow)
	snapshots := make([]subscriptionHistoricalSnapshot, 0, 30)
	var failed []string
	var lastErr error
	for daysAgo := 0; daysAgo < 30; daysAgo++ {
		date := localDate.AddDate(0, 0, -daysAgo)
		if daysAgo == 0 {
			snapshots = append(snapshots, subscriptionHistoricalSnapshot{Date: date, Snapshot: current})
			continue
		}

		snapshot, err := b.rates.GetForDate(ctx, date)
		if err != nil {
			failed = append(failed, date.Format("2006-01-02"))
			lastErr = err
		}
		snapshots = append(snapshots, subscriptionHistoricalSnapshot{Date: date, Snapshot: snapshot, Err: err})
	}
	if len(failed) > 0 {
		b.log.Warn("historical rates unavailable", "days", len(failed), "first", failed[0], "last", failed[len(failed)-1], "error", lastErr)
	}
	return snapshots
}

func startOfLocalDay(value time.Time) time.Time {
	year, month, day := value.Date()
	return time.Date(year, month, day, 0, 0, 0, 0, value.Location())
}

type rateRequest struct {
	From string
	To   string
}

func parseRateRequest(text string, s session) (rateRequest, error) {
	s = normalizeSession(s, "", "")
	args := commandArgs(text)
	if unknown := firstUnknownCurrencyCodeToken(args); unknown != "" {
		return rateRequest{}, unknownCurrencyCodeError(unknown)
	}
	codes := currencyCodesFromText(args)
	switch len(codes) {
	case 0:
		return rateRequest{From: s.From, To: s.To}, nil
	case 1:
		return rateRequest{From: codes[0], To: s.To}, nil
	default:
		return rateRequest{From: codes[0], To: codes[1]}, nil
	}
}

func rateReply(from, to string, snapshot rates.Snapshot) (string, error) {
	return rateReplyForLanguage(from, to, snapshot, languageRussian)
}

func rateReplyForLanguage(from, to string, snapshot rates.Snapshot, language string) (string, error) {
	direct, err := rates.Convert(1, from, to, snapshot)
	if err != nil {
		return "", err
	}
	reverse, err := rates.Convert(1, to, from, snapshot)
	if err != nil {
		return "", err
	}

	updatedAt := tr(language, "нет данных", "no data")
	if !snapshot.FetchedAt.IsZero() {
		updatedAt = snapshot.FetchedAt.UTC().Format("2006-01-02 15:04:05 UTC")
	}

	return fmt.Sprintf(
		tr(language, "Курс:\n1 %s = %s %s%s\n1 %s = %s %s%s\nОбновлено: %s", "Rate:\n1 %s = %s %s%s\n1 %s = %s %s%s\nUpdated: %s"),
		from,
		formatRate(direct),
		to,
		convenientRateSuffix(from, to, direct),
		to,
		formatRate(reverse),
		from,
		convenientRateSuffix(to, from, reverse),
		updatedAt,
	), nil
}

type rateHistoricalSnapshot = subscriptionHistoricalSnapshot

func rateReplyWithHistory(from, to string, snapshot rates.Snapshot, historicalSnapshots []rateHistoricalSnapshot) (string, error) {
	return rateReplyWithHistoryForLanguage(from, to, snapshot, historicalSnapshots, languageRussian)
}

func rateReplyWithHistoryForLanguage(from, to string, snapshot rates.Snapshot, historicalSnapshots []rateHistoricalSnapshot, language string) (string, error) {
	reply, err := rateReplyForLanguage(from, to, snapshot, language)
	if err != nil {
		return "", err
	}
	history := subscriptionRateHistoryFromSnapshots(from, to, historicalSnapshots)
	return reply + "\n\n" + formatSubscriptionRateHistoryForLanguage(from, to, history, language), nil
}

type subscriptionRateHistory struct {
	CurrentRate float64
	Yesterday   *subscriptionRatePoint
	WeekAgo     *subscriptionRatePoint
	MonthMin    *subscriptionRatePoint
	MonthMax    *subscriptionRatePoint
}

type subscriptionHistoricalSnapshot struct {
	Date     time.Time
	Snapshot rates.Snapshot
	Err      error
}

type subscriptionRatePoint struct {
	Date time.Time
	Rate float64
}

func subscriptionRateHistoryFromSnapshots(from, to string, snapshots []subscriptionHistoricalSnapshot) subscriptionRateHistory {
	if len(snapshots) == 0 {
		return subscriptionRateHistory{}
	}

	currentRate, err := rates.Convert(1, from, to, snapshots[0].Snapshot)
	if err != nil {
		return subscriptionRateHistory{}
	}
	history := subscriptionRateHistory{CurrentRate: currentRate}
	history.Yesterday = subscriptionRatePointFromSnapshot(from, to, snapshotAtOffset(snapshots, 1))
	history.WeekAgo = subscriptionRatePointFromSnapshot(from, to, snapshotAtOffset(snapshots, 7))

	for i := range snapshots {
		point := subscriptionRatePointFromSnapshot(from, to, &snapshots[i])
		if point == nil {
			continue
		}
		if history.MonthMin == nil || point.Rate < history.MonthMin.Rate {
			candidate := *point
			history.MonthMin = &candidate
		}
		if history.MonthMax == nil || point.Rate > history.MonthMax.Rate {
			candidate := *point
			history.MonthMax = &candidate
		}
	}

	return history
}

func snapshotAtOffset(snapshots []subscriptionHistoricalSnapshot, daysAgo int) *subscriptionHistoricalSnapshot {
	if daysAgo < 0 || daysAgo >= len(snapshots) {
		return nil
	}
	return &snapshots[daysAgo]
}

func subscriptionRatePointFromSnapshot(from, to string, snapshot *subscriptionHistoricalSnapshot) *subscriptionRatePoint {
	if snapshot == nil || snapshot.Err != nil {
		return nil
	}
	rate, err := rates.Convert(1, from, to, snapshot.Snapshot)
	if err != nil {
		return nil
	}
	return &subscriptionRatePoint{Date: snapshot.Date, Rate: rate}
}

func formatSubscriptionRateHistory(from, to string, history subscriptionRateHistory) string {
	return formatSubscriptionRateHistoryForLanguage(from, to, history, languageRussian)
}

func formatSubscriptionRateHistoryForLanguage(from, to string, history subscriptionRateHistory, language string) string {
	lines := []string{
		fmt.Sprintf(tr(language, "Динамика %s -> %s:", "%s -> %s trend:"), from, to),
		fmt.Sprintf(tr(language, "Со вчера: %s", "Since yesterday: %s"), formatRateDeltaForLanguage(history.CurrentRate, history.Yesterday, to, language)),
		fmt.Sprintf(tr(language, "За 7 дней: %s", "Over 7 days: %s"), formatRateDeltaForLanguage(history.CurrentRate, history.WeekAgo, to, language)),
	}
	if history.MonthMin == nil {
		lines = append(lines, tr(language, "Минимум за 30 дней: нет данных", "30-day minimum: no data"))
	} else {
		lines = append(lines, fmt.Sprintf(tr(language, "Минимум за 30 дней: %s", "30-day minimum: %s"), formatRateExtremeForLanguage(history.CurrentRate, history.MonthMin, to, language)))
	}
	if history.MonthMax == nil {
		lines = append(lines, tr(language, "Максимум за 30 дней: нет данных", "30-day maximum: no data"))
	} else {
		lines = append(lines, fmt.Sprintf(tr(language, "Максимум за 30 дней: %s", "30-day maximum: %s"), formatRateExtremeForLanguage(history.CurrentRate, history.MonthMax, to, language)))
	}
	return strings.Join(lines, "\n")
}

func formatRateExtremeForLanguage(current float64, point *subscriptionRatePoint, to, language string) string {
	if point == nil {
		return tr(language, "нет данных", "no data")
	}
	return fmt.Sprintf(
		tr(language, "%s %s (%s), сейчас %s", "%s %s (%s), now %s"),
		formatRate(point.Rate),
		to,
		point.Date.Format("2006-01-02"),
		formatRateDeltaForLanguage(current, point, to, language),
	)
}

func formatRateDeltaForLanguage(current float64, point *subscriptionRatePoint, to, language string) string {
	if point == nil || point.Rate == 0 {
		return tr(language, "нет данных", "no data")
	}
	delta := current - point.Rate
	percent := delta / point.Rate * 100
	return fmt.Sprintf("%s %s (%s)", formatSignedRate(delta), to, formatPercent(percent))
}

func formatSignedRate(value float64) string {
	formatted := formatRate(math.Abs(value))
	if value > 0 {
		return "+" + formatted
	}
	if value < 0 {
		return "-" + formatted
	}
	return formatted
}

func convenientRateSuffix(from, to string, unitRate float64) string {
	nominal, ok := convenientRateNominal(unitRate)
	if !ok {
		return ""
	}
	return fmt.Sprintf("\n%s %s = %s %s", formatNumber(float64(nominal)), from, formatConvertedAmount(unitRate*float64(nominal)), to)
}

func convenientRateNominal(unitRate float64) (int, bool) {
	absRate := math.Abs(unitRate)
	if absRate == 0 || absRate >= 0.01 {
		return 0, false
	}
	for _, nominal := range []int{10, 100, 1000, 10000, 100000, 1000000} {
		if absRate*float64(nominal) >= 0.01 {
			return nominal, true
		}
	}
	return 1000000, true
}
