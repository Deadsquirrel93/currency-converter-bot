package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"strings"
	"time"

	"currency-converter-bot/internal/rates"
)

// newRateSubscription sends the rates as soon as the Bank of Russia sets the
// next ones (usually the afternoon before they come into force).
type newRateSubscription struct {
	ChatID int64  `json:"chat_id"`
	From   string `json:"from"`
	To     string `json:"to"`
	// LastRateDate is the Date of the last rates announced to this user.
	// Empty means "not known yet": the scheduler fills it in without sending.
	LastRateDate string `json:"last_rate_date,omitempty"`
}

// rateWatch remembers the latest rates document and when to ask again.
type rateWatch struct {
	latest    rates.Snapshot
	nextCheck time.Time
}

// The Bank of Russia sets rates from data as of 15:30 Moscow time on business
// days and does not promise a publication time; they are usually out by 18:00.
// Inside the window the bot asks often; outside it hourly, which catches late
// publications and restarts.
const (
	newRateWindowStart   = 15*time.Hour + 30*time.Minute
	newRateWindowEnd     = 20 * time.Hour
	newRateCheckInWindow = 10 * time.Minute
	newRateCheckOutside  = time.Hour
	newRateFetchTimeout  = 30 * time.Second
)

// moscowTime is the Bank of Russia's time zone. Russia has no DST.
var moscowTime = time.FixedZone("MSK", 3*60*60)

func moscowDate(now time.Time) string {
	return now.In(moscowTime).Format("2006-01-02")
}

// nextRateCheck returns when to ask the Bank of Russia after a check at now.
func nextRateCheck(now time.Time) time.Time {
	msk := now.In(moscowTime)
	midnight := startOfLocalDay(msk)
	sinceMidnight := msk.Sub(midnight)
	if sinceMidnight >= newRateWindowStart && sinceMidnight < newRateWindowEnd {
		return now.Add(newRateCheckInWindow)
	}
	next := now.Add(newRateCheckOutside)
	if windowStart := midnight.Add(newRateWindowStart); msk.Before(windowStart) && windowStart.Before(next) {
		return windowStart
	}
	return next
}

// latestRates returns the latest rates document, asking the Bank of Russia
// only when nextRateCheck says so.
func (b *Bot) latestRates(ctx context.Context, now time.Time) (rates.Snapshot, bool) {
	b.newRateMu.Lock()
	due := !now.Before(b.rateWatch.nextCheck)
	latest := b.rateWatch.latest
	b.newRateMu.Unlock()

	if due {
		fetchCtx, cancel := context.WithTimeout(ctx, newRateFetchTimeout)
		snapshot, err := b.rates.FetchLatest(fetchCtx)
		cancel()
		b.newRateMu.Lock()
		b.rateWatch.nextCheck = nextRateCheck(now)
		if err == nil {
			if snapshot.Date != "" && snapshot.Date != latest.Date {
				b.log.Info("bank of russia rates date", "date", snapshot.Date)
			}
			latest = snapshot
			b.rateWatch.latest = snapshot
		}
		b.newRateMu.Unlock()
		if err != nil && ctx.Err() == nil {
			b.log.Warn("check for new rates failed", "error", err)
		}
	}
	return latest, latest.Date != ""
}

// watchRates runs every minute from the scheduler: it asks the Bank of
// Russia for new rates on the nextRateCheck schedule, and only while someone
// is waiting for them, then serves the new rate subscriptions and alerts.
func (b *Bot) watchRates(ctx context.Context, now time.Time) {
	if b.rates == nil || (!b.hasNewRateSubscriptions() && !b.hasAlerts()) {
		return
	}
	latest, ok := b.latestRates(ctx, now)
	if !ok {
		return
	}
	b.sendNewRates(ctx, now, latest)
	b.sendAlerts(ctx, now, latest)
}

// sendNewRates announces rates that are set but not yet in force to every
// subscriber who has not had them.
func (b *Bot) sendNewRates(ctx context.Context, now time.Time, latest rates.Snapshot) {
	due := b.dueNewRates(latest.Date, now)
	// Once the rates are in force they are no longer news: a restart after
	// midnight must not announce yesterday's publication.
	if len(due) == 0 || latest.Date <= moscowDate(now) {
		return
	}

	previous := b.previousRates(ctx, latest.Date)
	for userID, subscription := range due {
		language := b.userLanguage(userID)
		text, err := newRateText(subscription.From, subscription.To, latest, previous, language)
		if err != nil {
			// The Bank of Russia may stop publishing a currency; do not retry
			// the same failure every minute.
			b.log.Error("new rate reply failed", "user_id", userID, "from", subscription.From, "to", subscription.To, "error", err)
			b.markNewRateSent(userID, subscription, latest.Date)
			continue
		}
		if err := b.sendMessage(ctx, subscription.ChatID, text); err != nil {
			b.handleNewRateSendError(userID, subscription, latest.Date, now, err)
			continue
		}
		b.markNewRateSent(userID, subscription, latest.Date)
	}
}

// previousRates returns the rates in force the day before date, the "current"
// rates the new ones are compared with. An error gives an empty snapshot.
func (b *Bot) previousRates(ctx context.Context, date string) rates.Snapshot {
	day, err := time.ParseInLocation("2006-01-02", date, moscowTime)
	if err != nil {
		return rates.Snapshot{}
	}
	ctx, cancel := context.WithTimeout(ctx, newRateFetchTimeout)
	defer cancel()
	snapshot, err := b.rates.GetForDate(ctx, day.AddDate(0, 0, -1))
	if err != nil {
		b.log.Warn("previous rates unavailable", "date", date, "error", err)
		return rates.Snapshot{}
	}
	return snapshot
}

func (b *Bot) hasNewRateSubscriptions() bool {
	b.newRateMu.Lock()
	defer b.newRateMu.Unlock()
	return len(b.newRateSubs) > 0
}

// dueNewRates returns the subscriptions that have not had the rates dated
// rateDate. Subscriptions that do not know the latest date yet get it without
// a message.
func (b *Bot) dueNewRates(rateDate string, now time.Time) map[int64]newRateSubscription {
	candidates := map[int64]newRateSubscription{}
	initialized := false
	b.newRateMu.Lock()
	for userID, subscription := range b.newRateSubs {
		switch {
		case subscription.LastRateDate == "":
			subscription.LastRateDate = rateDate
			b.newRateSubs[userID] = subscription
			initialized = true
		case subscription.LastRateDate >= rateDate:
		default:
			if retry, ok := b.newRateRetry[userID]; ok && retry.Date == rateDate && now.Before(retry.Next) {
				continue
			}
			candidates[userID] = subscription
		}
	}
	b.newRateMu.Unlock()
	if initialized {
		b.saveNewRateSubscriptionsLogged()
	}

	due := map[int64]newRateSubscription{}
	for userID, subscription := range candidates {
		// Access revoked with /disallow: keep the subscription, stop sending.
		if b.isAllowed(userID) {
			due[userID] = subscription
		}
	}
	return due
}

func (b *Bot) markNewRateSent(userID int64, sent newRateSubscription, rateDate string) {
	b.newRateMu.Lock()
	current, ok := b.newRateSubs[userID]
	if !ok || current.ChatID != sent.ChatID || current.From != sent.From || current.To != sent.To {
		b.newRateMu.Unlock()
		return
	}
	current.LastRateDate = rateDate
	b.newRateSubs[userID] = current
	delete(b.newRateRetry, userID)
	b.newRateMu.Unlock()
	b.saveNewRateSubscriptionsLogged()
}

// handleNewRateSendError follows the daily subscriptions: an unreachable chat
// drops the subscription, other errors back off and give up on this date.
func (b *Bot) handleNewRateSendError(userID int64, subscription newRateSubscription, rateDate string, now time.Time, err error) {
	if isChatUnreachable(err) {
		b.log.Warn("new rate subscription removed: chat unreachable", "user_id", userID, "chat_id", subscription.ChatID, "error", err)
		b.removeNewRateSubscription(userID)
		return
	}

	b.newRateMu.Lock()
	retry := b.newRateRetry[userID]
	if retry.Date != rateDate {
		retry = subscriptionRetry{Date: rateDate}
	}
	giveUp := retry.fail(now)
	if !giveUp {
		b.newRateRetry[userID] = retry
	}
	b.newRateMu.Unlock()

	if giveUp {
		b.log.Error("send new rate failed, skipping this date", "user_id", userID, "chat_id", subscription.ChatID, "rate_date", rateDate, "error", err)
		b.markNewRateSent(userID, subscription, rateDate)
		return
	}
	b.log.Warn("send new rate failed, will retry", "user_id", userID, "chat_id", subscription.ChatID, "retry_at", retry.Next.Format(time.RFC3339), "error", err)
}

func newRateText(from, to string, latest, previous rates.Snapshot, language string) (string, error) {
	direct, err := rates.Convert(1, from, to, latest)
	if err != nil {
		return "", err
	}
	reverse, err := rates.Convert(1, to, from, latest)
	if err != nil {
		return "", err
	}
	change := tr(language, "нет данных", "no data")
	if old, err := rates.Convert(1, from, to, previous); err == nil {
		change = formatRateDeltaForLanguage(direct, &subscriptionRatePoint{Rate: old}, to, language)
	}
	return fmt.Sprintf(
		tr(language, "ЦБ установил курс %s -> %s на %s\n1 %s = %s %s%s\n1 %s = %s %s%s\nК текущему курсу: %s",
			"The Bank of Russia has set the %s -> %s rate for %s\n1 %s = %s %s%s\n1 %s = %s %s%s\nChange from the current rate: %s"),
		from, to, formatRateDate(latest.Date),
		from, formatRate(direct), to, convenientRateSuffix(from, to, direct),
		to, formatRate(reverse), from, convenientRateSuffix(to, from, reverse),
		change,
	), nil
}

// formatRateDate turns "2026-09-29" into "29.09.2026".
func formatRateDate(date string) string {
	parsed, err := time.Parse("2006-01-02", date)
	if err != nil {
		return date
	}
	return parsed.Format("02.01.2006")
}

func isNewRateMode(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "new", "новый", "нов":
		return true
	default:
		return false
	}
}

func (b *Bot) setNewRateSubscription(ctx context.Context, chatID, userID int64, currencyText string) {
	language := b.userLanguage(userID)
	from, to, err := parseSubscriptionPair(currencyText, b.getSession(userID))
	if err != nil {
		_ = b.sendMessage(ctx, chatID, errorText(err, language))
		return
	}
	subscription := newRateSubscription{ChatID: chatID, From: from, To: to}

	// Start from the latest published date, so only rates set after this
	// moment count as new; if tomorrow's are already out, send them now.
	fetchCtx, cancel := context.WithTimeout(ctx, newRateFetchTimeout)
	latest, fetchErr := b.rates.FetchLatest(fetchCtx)
	cancel()
	if fetchErr != nil {
		b.log.Warn("check for new rates failed", "error", fetchErr)
	}
	subscription.LastRateDate = latest.Date
	b.setNewRateSubscriptionState(userID, subscription)

	confirmation := fmt.Sprintf(tr(language,
		"Готово: пришлю курс %s -> %s, как только ЦБ установит новый. Обычно это происходит по рабочим дням после 15:30 МСК, курс вступает в силу на следующий день.",
		"Done: I will send the %s -> %s rate as soon as the Bank of Russia sets a new one. It usually happens on business days after 15:30 Moscow time; the rate comes into force the next day."), from, to)
	if latest.Date == "" || latest.Date <= moscowDate(time.Now()) {
		_ = b.sendMessage(ctx, chatID, confirmation)
		return
	}
	confirmation += tr(language, " Следующий курс уже установлен, присылаю его сейчас.", " The next rate is already set, here it is.")
	_ = b.sendMessage(ctx, chatID, confirmation)
	text, err := newRateText(from, to, latest, b.previousRates(ctx, latest.Date), language)
	if err != nil {
		_ = b.sendMessage(ctx, chatID, checkCurrenciesText(language, errorText(err, language)))
		return
	}
	_ = b.sendMessage(ctx, chatID, text)
}

func (b *Bot) getNewRateSubscription(userID int64) (newRateSubscription, bool) {
	b.newRateMu.Lock()
	defer b.newRateMu.Unlock()
	subscription, ok := b.newRateSubs[userID]
	return subscription, ok
}

func (b *Bot) setNewRateSubscriptionState(userID int64, subscription newRateSubscription) {
	b.newRateMu.Lock()
	b.newRateSubs[userID] = subscription
	delete(b.newRateRetry, userID)
	b.newRateMu.Unlock()
	b.saveNewRateSubscriptionsLogged()
}

func (b *Bot) removeNewRateSubscription(userID int64) bool {
	b.newRateMu.Lock()
	_, ok := b.newRateSubs[userID]
	delete(b.newRateSubs, userID)
	delete(b.newRateRetry, userID)
	b.newRateMu.Unlock()
	if ok {
		b.saveNewRateSubscriptionsLogged()
	}
	return ok
}

func (b *Bot) loadNewRateSubscriptions() error {
	return b.newRateStore.load(func(raw []byte) error {
		var subscriptions map[int64]newRateSubscription
		if err := json.Unmarshal(raw, &subscriptions); err != nil {
			return err
		}
		b.newRateMu.Lock()
		defer b.newRateMu.Unlock()
		for userID, subscription := range subscriptions {
			subscription.From = strings.ToUpper(strings.TrimSpace(subscription.From))
			subscription.To = strings.ToUpper(strings.TrimSpace(subscription.To))
			if subscription.ChatID == 0 || subscription.From == "" || subscription.To == "" {
				continue
			}
			b.newRateSubs[userID] = subscription
		}
		return nil
	})
}

func (b *Bot) saveNewRateSubscriptionsLogged() {
	err := b.newRateStore.save(func() any {
		b.newRateMu.Lock()
		defer b.newRateMu.Unlock()
		return maps.Clone(b.newRateSubs)
	})
	if err != nil {
		b.log.Error("save new rate subscriptions failed", "path", b.cfg.NewRateSubsFile, "error", err)
	}
}
