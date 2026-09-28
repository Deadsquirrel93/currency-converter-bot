package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type dailySubscription struct {
	ChatID       int64  `json:"chat_id"`
	From         string `json:"from"`
	To           string `json:"to"`
	Time         string `json:"time"`
	LastSentDate string `json:"last_sent_date,omitempty"`
}

func (b *Bot) setSubscription(ctx context.Context, chatID, userID int64, text string) {
	language := b.userLanguage(userID)
	fields := strings.Fields(text)
	if len(fields) == 2 && isOffValue(fields[1]) {
		b.deleteSubscription(ctx, chatID, userID, "")
		return
	}
	if len(fields) >= 2 && isNewRateMode(fields[1]) {
		b.setNewRateSubscription(ctx, chatID, userID, strings.Join(fields[2:], " "))
		return
	}

	s := b.getSession(userID)
	subscription, err := parseSubscription(text, s)
	if err != nil {
		_ = b.sendMessage(ctx, chatID, errorText(err, language))
		return
	}
	subscription.ChatID = chatID
	subscription.LastSentDate = ""
	location := b.userLocation(userID)
	now := time.Now().In(location)
	passedToday := dailyTimePassed(subscription.Time, now)
	if passedToday {
		subscription.LastSentDate = subscriptionDate(now)
	}

	b.setUserSubscription(userID, subscription)
	confirmation := fmt.Sprintf(tr(language, "Готово: буду присылать курс %s -> %s каждый день в %s (%s, изменить: /tz).", "Done: I will send the %s -> %s rate every day at %s (%s, change it with /tz)."), subscription.From, subscription.To, subscription.Time, location.String())
	if !passedToday {
		_ = b.sendMessage(ctx, chatID, confirmation)
		return
	}
	confirmation += tr(language, " Сегодня это время уже прошло, поэтому текущий курс присылаю сейчас, следующий — завтра.", " That time has already passed today, so here is the current rate now; the next one comes tomorrow.")
	_ = b.sendMessage(ctx, chatID, confirmation)
	b.sendRate(ctx, chatID, subscription.From, subscription.To, language, location)
}

func (b *Bot) showSubscription(ctx context.Context, chatID, userID int64) {
	language := b.userLanguage(userID)
	var parts []string
	if subscription, ok := b.getUserSubscription(userID); ok {
		parts = append(parts, fmt.Sprintf(tr(language, "Ежедневная подписка:\nПара: %s -> %s\nВремя: %s (%s)\nЧасовой пояс: /tz\nОтключить: /unsubscribe daily", "Daily subscription:\nPair: %s -> %s\nTime: %s (%s)\nTime zone: /tz\nDisable: /unsubscribe daily"), subscription.From, subscription.To, subscription.Time, b.timezoneLabel(userID, language)))
	}
	if subscription, ok := b.getNewRateSubscription(userID); ok {
		parts = append(parts, fmt.Sprintf(tr(language, "Новый курс ЦБ сразу после установки:\nПара: %s -> %s\nОтключить: /unsubscribe new", "New Bank of Russia rate as soon as it is set:\nPair: %s -> %s\nDisable: /unsubscribe new"), subscription.From, subscription.To))
	}
	if len(parts) == 0 {
		_ = b.sendMessage(ctx, chatID, tr(language, "Подписки выключены. Включить: /subscribe 09:00 USD RUB (каждый день) или /subscribe new USD RUB (сразу после установки нового курса ЦБ).", "Subscriptions are off. Enable one with /subscribe 09:00 USD RUB (every day) or /subscribe new USD RUB (as soon as the Bank of Russia sets a new rate)."))
		return
	}
	_ = b.sendMessage(ctx, chatID, strings.Join(parts, "\n\n"))
}

// deleteSubscription disables the daily subscription ("daily"), the new rate
// one ("new") or, without an argument, both.
func (b *Bot) deleteSubscription(ctx context.Context, chatID, userID int64, which string) {
	language := b.userLanguage(userID)
	switch strings.ToLower(strings.TrimSpace(which)) {
	case "":
		daily := b.removeUserSubscription(userID)
		newRate := b.removeNewRateSubscription(userID)
		if daily || newRate {
			_ = b.sendMessage(ctx, chatID, tr(language, "Подписки отключены.", "Subscriptions disabled."))
			return
		}
		_ = b.sendMessage(ctx, chatID, tr(language, "Подписки уже выключены.", "Subscriptions are already disabled."))
	case "daily", "ежедневная", "ежедневно":
		if b.removeUserSubscription(userID) {
			_ = b.sendMessage(ctx, chatID, tr(language, "Ежедневная подписка отключена.", "Daily subscription disabled."))
			return
		}
		_ = b.sendMessage(ctx, chatID, tr(language, "Ежедневная подписка уже выключена.", "Daily subscription is already disabled."))
	default:
		if !isNewRateMode(which) {
			_ = b.sendMessage(ctx, chatID, tr(language, "Укажите, что отключить: /unsubscribe, /unsubscribe daily или /unsubscribe new.", "Specify what to disable: /unsubscribe, /unsubscribe daily, or /unsubscribe new."))
			return
		}
		if b.removeNewRateSubscription(userID) {
			_ = b.sendMessage(ctx, chatID, tr(language, "Подписка на новый курс отключена.", "New rate subscription disabled."))
			return
		}
		_ = b.sendMessage(ctx, chatID, tr(language, "Подписка на новый курс уже выключена.", "New rate subscription is already disabled."))
	}
}

func (b *Bot) getUserSubscription(userID int64) (dailySubscription, bool) {
	b.subMu.RLock()
	subscription, ok := b.subscriptions[userID]
	b.subMu.RUnlock()
	if !ok {
		return dailySubscription{}, false
	}
	return normalizeSubscription(subscription), true
}

func (b *Bot) setUserSubscription(userID int64, subscription dailySubscription) {
	subscription = normalizeSubscription(subscription)
	b.subMu.Lock()
	b.subscriptions[userID] = subscription
	b.subMu.Unlock()

	if err := b.saveSubscriptions(); err != nil {
		b.log.Error("save subscriptions failed", "path", b.cfg.SubscriptionsFile, "error", err)
	}
}

func (b *Bot) removeUserSubscription(userID int64) bool {
	b.subMu.Lock()
	if _, ok := b.subscriptions[userID]; !ok {
		b.subMu.Unlock()
		return false
	}
	delete(b.subscriptions, userID)
	b.subMu.Unlock()

	if err := b.saveSubscriptions(); err != nil {
		b.log.Error("delete subscription failed", "path", b.cfg.SubscriptionsFile, "error", err)
	}
	return true
}

func (b *Bot) runSubscriptionScheduler(ctx context.Context) {
	b.sendDueSubscriptions(ctx, time.Now())
	b.watchRates(ctx, time.Now())

	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			b.sendDueSubscriptions(ctx, now)
			b.watchRates(ctx, now)
		}
	}
}

func (b *Bot) sendDueSubscriptions(ctx context.Context, now time.Time) {
	if b.rates == nil {
		return
	}

	due := b.dueSubscriptions(now)
	if len(due) == 0 {
		return
	}

	snapshot, err := b.rates.Get(ctx)
	if err != nil {
		b.log.Error("rates unavailable for subscriptions", "error", err)
		return
	}
	// Users in different zones can be on different local days; the history is
	// collected once per local date.
	histories := map[string][]subscriptionHistoricalSnapshot{}

	for userID, subscription := range due {
		today := subscriptionDate(subscription.localNow)
		historicalSnapshots, ok := histories[today]
		if !ok {
			historicalSnapshots = b.subscriptionHistoricalSnapshots(ctx, snapshot, subscription.localNow)
			histories[today] = historicalSnapshots
		}
		language := b.userLanguage(userID)
		reply, err := rateReplyWithHistoryForLanguage(subscription.From, subscription.To, snapshot, historicalSnapshots, language)
		if err != nil {
			b.log.Error("subscription rate reply failed", "user_id", userID, "from", subscription.From, "to", subscription.To, "error", err)
			continue
		}
		text := fmt.Sprintf(tr(language, "Ежедневный курс %s -> %s\n\n%s", "Daily rate %s -> %s\n\n%s"), subscription.From, subscription.To, reply)
		if err := b.sendMessage(ctx, subscription.ChatID, text); err != nil {
			b.handleSubscriptionSendError(userID, subscription.dailySubscription, subscription.localNow, err)
			continue
		}
		b.markSubscriptionSent(userID, subscription.dailySubscription, today)
	}
}

// dueSubscription is a subscription to send now, with the current time in the
// user's time zone.
type dueSubscription struct {
	dailySubscription
	localNow time.Time
}

// dueSubscriptions checks every subscription against the local time and date
// of its user, so LastSentDate and retries follow the user's own day.
func (b *Bot) dueSubscriptions(now time.Time) map[int64]dueSubscription {
	type candidate struct {
		subscription dailySubscription
		retry        subscriptionRetry
		retrying     bool
	}
	b.subMu.RLock()
	candidates := make(map[int64]candidate, len(b.subscriptions))
	for userID, subscription := range b.subscriptions {
		retry, retrying := b.subRetry[userID]
		candidates[userID] = candidate{subscription: normalizeSubscription(subscription), retry: retry, retrying: retrying}
	}
	b.subMu.RUnlock()

	due := map[int64]dueSubscription{}
	for userID, c := range candidates {
		if c.subscription.ChatID == 0 || c.subscription.Time == "" {
			continue
		}
		localNow := now.In(b.userLocation(userID))
		today := subscriptionDate(localNow)
		if c.subscription.LastSentDate == today {
			continue
		}
		// Access revoked with /disallow: keep the subscription (it resumes if
		// access is granted again) but stop sending.
		if !b.isAllowed(userID) {
			continue
		}
		if c.retrying && c.retry.Date == today && localNow.Before(c.retry.Next) {
			continue
		}
		if dailyTimePassed(c.subscription.Time, localNow) {
			due[userID] = dueSubscription{dailySubscription: c.subscription, localNow: localNow}
		}
	}
	return due
}

func (b *Bot) markSubscriptionSent(userID int64, sent dailySubscription, date string) {
	b.subMu.Lock()
	current, ok := b.subscriptions[userID]
	if !ok {
		b.subMu.Unlock()
		return
	}
	current = normalizeSubscription(current)
	if current.ChatID != sent.ChatID || current.From != sent.From || current.To != sent.To || current.Time != sent.Time {
		b.subMu.Unlock()
		return
	}
	current.LastSentDate = date
	b.subscriptions[userID] = current
	delete(b.subRetry, userID)
	b.subMu.Unlock()

	if err := b.saveSubscriptions(); err != nil {
		b.log.Error("mark subscription sent failed", "path", b.cfg.SubscriptionsFile, "error", err)
	}
}

func loadSubscriptionLocation(name string) (*time.Location, error) {
	name = strings.TrimSpace(name)
	if name == "" || strings.EqualFold(name, "local") {
		return time.Local, nil
	}
	location, err := time.LoadLocation(name)
	if err != nil {
		return time.Local, err
	}
	return location, nil
}

func parseSubscription(text string, s session) (dailySubscription, error) {
	s = normalizeSession(s, "", "")
	args := commandArgs(text)
	fields := strings.Fields(args)
	if len(fields) == 0 {
		return dailySubscription{}, userError{
			ru: "Укажите время: /subscribe 09:00 или /subscribe 09:00 USD RUB.",
			en: "Specify a time: /subscribe 09:00 or /subscribe 09:00 USD RUB.",
		}
	}

	dailyTime, err := parseDailyTime(fields[0])
	if err != nil {
		return dailySubscription{}, userError{
			ru: "Время должно быть в формате HH:MM, например /subscribe 09:00.",
			en: "The time must be in HH:MM format, for example /subscribe 09:00.",
		}
	}

	from, to, err := parseSubscriptionPair(strings.Join(fields[1:], " "), s)
	if err != nil {
		return dailySubscription{}, err
	}
	return dailySubscription{From: from, To: to, Time: dailyTime}, nil
}

// parseSubscriptionPair reads an optional "USD RUB" pair; missing currencies
// come from the user's settings.
func parseSubscriptionPair(currencyText string, s session) (string, string, error) {
	s = normalizeSession(s, "", "")
	if unknown := firstUnknownCurrencyCodeToken(currencyText); unknown != "" {
		return "", "", unknownCurrencyCodeError(unknown)
	}
	codes := currencyCodesFromText(currencyText)
	from, to := s.From, s.To
	switch len(codes) {
	case 0:
	case 1:
		from = codes[0]
	default:
		from = codes[0]
		to = codes[1]
	}
	return from, to, nil
}

func parseDailyTime(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	hourRaw, minuteRaw, ok := strings.Cut(raw, ":")
	if !ok {
		return "", errors.New("invalid daily time")
	}
	hour, err := strconv.Atoi(strings.TrimSpace(hourRaw))
	if err != nil {
		return "", err
	}
	minute, err := strconv.Atoi(strings.TrimSpace(minuteRaw))
	if err != nil {
		return "", err
	}
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return "", errors.New("invalid daily time")
	}
	return fmt.Sprintf("%02d:%02d", hour, minute), nil
}

func dailyTimePassed(dailyTime string, now time.Time) bool {
	return now.Format("15:04") >= dailyTime
}

func subscriptionDate(now time.Time) string {
	return now.Format("2006-01-02")
}

func (b *Bot) loadSubscriptions() error {
	return b.subscriptionsStore.load(func(raw []byte) error {
		var subscriptions map[int64]dailySubscription
		if err := json.Unmarshal(raw, &subscriptions); err != nil {
			return err
		}
		b.subMu.Lock()
		for userID, subscription := range subscriptions {
			subscription = normalizeSubscription(subscription)
			if subscription.ChatID == 0 || subscription.Time == "" {
				continue
			}
			b.subscriptions[userID] = subscription
		}
		b.subMu.Unlock()
		return nil
	})
}

func (b *Bot) saveSubscriptions() error {
	return b.subscriptionsStore.save(func() any {
		b.subMu.RLock()
		defer b.subMu.RUnlock()
		return copySubscriptions(b.subscriptions)
	})
}

func copySubscriptions(subscriptions map[int64]dailySubscription) map[int64]dailySubscription {
	result := make(map[int64]dailySubscription, len(subscriptions))
	for userID, subscription := range subscriptions {
		result[userID] = subscription
	}
	return result
}

func normalizeSubscription(subscription dailySubscription) dailySubscription {
	subscription.From = strings.ToUpper(strings.TrimSpace(subscription.From))
	subscription.To = strings.ToUpper(strings.TrimSpace(subscription.To))
	if dailyTime, err := parseDailyTime(subscription.Time); err == nil {
		subscription.Time = dailyTime
	} else {
		subscription.Time = ""
	}
	return subscription
}

// subscriptionRetryDelays is the pause after each consecutive failed delivery
// on one day; after the last one the subscription is skipped until tomorrow.
var subscriptionRetryDelays = []time.Duration{5 * time.Minute, 15 * time.Minute, 45 * time.Minute}

type subscriptionRetry struct {
	Date     string
	Failures int
	Next     time.Time
}

// handleSubscriptionSendError stops retrying every minute: an unreachable chat
// (bot blocked, removed from the group) drops the subscription, other errors
// back off and give up for the day.
func (b *Bot) handleSubscriptionSendError(userID int64, subscription dailySubscription, localNow time.Time, err error) {
	if isChatUnreachable(err) {
		b.log.Warn("subscription removed: chat unreachable", "user_id", userID, "chat_id", subscription.ChatID, "error", err)
		b.removeUserSubscription(userID)
		return
	}

	today := subscriptionDate(localNow)
	b.subMu.Lock()
	retry := b.subRetry[userID]
	if retry.Date != today {
		retry = subscriptionRetry{Date: today}
	}
	retry.Failures++
	giveUp := retry.Failures > len(subscriptionRetryDelays)
	if !giveUp {
		retry.Next = localNow.Add(subscriptionRetryDelays[retry.Failures-1])
		b.subRetry[userID] = retry
	}
	b.subMu.Unlock()

	if giveUp {
		b.log.Error("send subscription failed, skipping until tomorrow", "user_id", userID, "chat_id", subscription.ChatID, "error", err)
		b.markSubscriptionSent(userID, subscription, today)
		return
	}
	b.log.Warn("send subscription failed, will retry", "user_id", userID, "chat_id", subscription.ChatID, "retry_at", retry.Next.Format(time.RFC3339), "error", err)
}
