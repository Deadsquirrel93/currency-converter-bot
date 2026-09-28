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
		b.deleteSubscription(ctx, chatID, userID)
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
	now := time.Now().In(b.subscriptionLocation)
	passedToday := dailyTimePassed(subscription.Time, now)
	if passedToday {
		subscription.LastSentDate = subscriptionDate(now)
	}

	b.setUserSubscription(userID, subscription)
	confirmation := fmt.Sprintf(tr(language, "Готово: буду присылать курс %s -> %s каждый день в %s (%s).", "Done: I will send the %s -> %s rate every day at %s (%s)."), subscription.From, subscription.To, subscription.Time, b.subscriptionLocation.String())
	if !passedToday {
		_ = b.sendMessage(ctx, chatID, confirmation)
		return
	}
	confirmation += tr(language, " Сегодня это время уже прошло, поэтому текущий курс присылаю сейчас, следующий — завтра.", " That time has already passed today, so here is the current rate now; the next one comes tomorrow.")
	_ = b.sendMessage(ctx, chatID, confirmation)
	b.sendRate(ctx, chatID, subscription.From, subscription.To, language)
}

func (b *Bot) showSubscription(ctx context.Context, chatID, userID int64) {
	language := b.userLanguage(userID)
	subscription, ok := b.getUserSubscription(userID)
	if !ok {
		_ = b.sendMessage(ctx, chatID, tr(language, "Подписка выключена. Включить: /subscribe 09:00 или /subscribe 09:00 USD RUB.", "Subscription is off. Enable it with /subscribe 09:00 or /subscribe 09:00 USD RUB."))
		return
	}
	_ = b.sendMessage(ctx, chatID, fmt.Sprintf(tr(language, "Подписка:\nПара: %s -> %s\nВремя: %s (%s)\nОтключить: /unsubscribe", "Subscription:\nPair: %s -> %s\nTime: %s (%s)\nDisable: /unsubscribe"), subscription.From, subscription.To, subscription.Time, b.subscriptionLocation.String()))
}

func (b *Bot) deleteSubscription(ctx context.Context, chatID, userID int64) {
	language := b.userLanguage(userID)
	if b.removeUserSubscription(userID) {
		_ = b.sendMessage(ctx, chatID, tr(language, "Подписка отключена.", "Subscription disabled."))
		return
	}
	_ = b.sendMessage(ctx, chatID, tr(language, "Подписка уже выключена.", "Subscription is already disabled."))
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

	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			b.sendDueSubscriptions(ctx, now)
		}
	}
}

func (b *Bot) sendDueSubscriptions(ctx context.Context, now time.Time) {
	if b.rates == nil {
		return
	}

	location := b.subscriptionLocation
	if location == nil {
		location = time.Local
	}
	localNow := now.In(location)
	due := b.dueSubscriptions(localNow)
	if len(due) == 0 {
		return
	}

	snapshot, err := b.rates.Get(ctx)
	if err != nil {
		b.log.Error("rates unavailable for subscriptions", "error", err)
		return
	}
	historicalSnapshots := b.subscriptionHistoricalSnapshots(ctx, snapshot, localNow)

	for userID, subscription := range due {
		language := b.userLanguage(userID)
		reply, err := rateReplyWithHistoryForLanguage(subscription.From, subscription.To, snapshot, historicalSnapshots, language)
		if err != nil {
			b.log.Error("subscription rate reply failed", "user_id", userID, "from", subscription.From, "to", subscription.To, "error", err)
			continue
		}
		text := fmt.Sprintf(tr(language, "Ежедневный курс %s -> %s\n\n%s", "Daily rate %s -> %s\n\n%s"), subscription.From, subscription.To, reply)
		if err := b.sendMessage(ctx, subscription.ChatID, text); err != nil {
			b.handleSubscriptionSendError(userID, subscription, localNow, err)
			continue
		}
		b.markSubscriptionSent(userID, subscription, subscriptionDate(localNow))
	}
}

func (b *Bot) dueSubscriptions(now time.Time) map[int64]dailySubscription {
	today := subscriptionDate(now)
	b.subMu.RLock()
	defer b.subMu.RUnlock()

	due := map[int64]dailySubscription{}
	for userID, subscription := range b.subscriptions {
		subscription = normalizeSubscription(subscription)
		if subscription.ChatID == 0 || subscription.Time == "" || subscription.LastSentDate == today {
			continue
		}
		// Access revoked with /disallow: keep the subscription (it resumes if
		// access is granted again) but stop sending.
		if !b.isAllowed(userID) {
			continue
		}
		if retry, ok := b.subRetry[userID]; ok && retry.Date == today && now.Before(retry.Next) {
			continue
		}
		if dailyTimePassed(subscription.Time, now) {
			due[userID] = subscription
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

	currencyText := strings.Join(fields[1:], " ")
	if unknown := firstUnknownCurrencyCodeToken(currencyText); unknown != "" {
		return dailySubscription{}, unknownCurrencyCodeError(unknown)
	}
	codes := currencyCodesFromText(currencyText)

	from := s.From
	to := s.To
	switch len(codes) {
	case 0:
	case 1:
		from = codes[0]
	default:
		from = codes[0]
		to = codes[1]
	}

	return dailySubscription{From: from, To: to, Time: dailyTime}, nil
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
