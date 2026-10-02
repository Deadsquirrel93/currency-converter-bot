package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"currency-converter-bot/internal/convert"
	"currency-converter-bot/internal/rates"
)

// maxAlertsPerUser bounds the alerts file and the work done on every check.
const maxAlertsPerUser = 10

const alertOffCallbackPrefix = "alert_off:"

// rateAlert fires once when the Bank of Russia rate From -> To crosses
// Threshold in the direction of Op, and is then removed.
type rateAlert struct {
	// ID is stable for the user, so "/alert off 2" keeps meaning the same
	// alert after others are removed.
	ID        int     `json:"id"`
	ChatID    int64   `json:"chat_id"`
	From      string  `json:"from"`
	To        string  `json:"to"`
	Op        string  `json:"op"`
	Threshold float64 `json:"threshold"`
}

type userAlerts struct {
	NextID int         `json:"next_id"`
	Alerts []rateAlert `json:"alerts"`
}

type alertKey struct {
	userID int64
	id     int
}

func (a rateAlert) triggered(rate float64) bool {
	switch a.Op {
	case ">":
		return rate > a.Threshold
	case ">=":
		return rate >= a.Threshold
	case "<":
		return rate < a.Threshold
	case "<=":
		return rate <= a.Threshold
	default:
		return false
	}
}

func isAlertOp(op string) bool {
	switch op {
	case ">", ">=", "<", "<=":
		return true
	default:
		return false
	}
}

func (a rateAlert) describe() string {
	return fmt.Sprintf("%s -> %s %s %s", a.From, a.To, a.Op, formatNumber(a.Threshold))
}

var errAlertUsage = userError{
	ru: "Укажите пару, условие и порог: /alert USD RUB > 95 или /alert USD RUB < 90. Список: /alerts, удалить: /alert off 1.",
	en: "Specify a pair, a condition and a threshold: /alert USD RUB > 95 or /alert USD RUB < 90. List: /alerts, remove: /alert off 1.",
}

// parseAlert reads "USD RUB > 95", "EUR < 90,5", "> 95" (the user's pair) or
// "USD RUB выше 95". Missing currencies come from the settings.
func parseAlert(args string, s session) (rateAlert, error) {
	args = strings.NewReplacer("≥", ">=", "≤", "<=").Replace(strings.TrimSpace(args))
	fields := strings.Fields(args)
	for i, field := range fields {
		switch strings.ToLower(field) {
		case "выше", "больше", "above":
			fields[i] = ">"
		case "ниже", "меньше", "below":
			fields[i] = "<"
		}
	}
	args = strings.Join(fields, " ")

	index := strings.IndexAny(args, "<>")
	if index < 0 {
		return rateAlert{}, errAlertUsage
	}
	op, rest := args[index:index+1], args[index+1:]
	if strings.HasPrefix(rest, "=") {
		op, rest = op+"=", rest[1:]
	}

	from, to, err := parseSubscriptionPair(args[:index], s)
	if err != nil {
		return rateAlert{}, err
	}
	if from == to {
		return rateAlert{}, userError{
			ru: "Для алерта нужны две разные валюты, например /alert USD RUB > 95.",
			en: "An alert needs two different currencies, for example /alert USD RUB > 95.",
		}
	}
	threshold, err := convert.ParseAmount(rest, convert.Options{CommaThousands: normalizeLanguage(s.Language) == languageEnglish})
	// The amount parser reads "-5" as 5, so a minus is rejected here.
	if err != nil || strings.ContainsAny(rest, "-−") || threshold <= 0 || math.IsNaN(threshold) || math.IsInf(threshold, 0) {
		return rateAlert{}, userError{
			ru: "Порог должен быть положительным числом, например /alert USD RUB > 95,5.",
			en: "The threshold must be a positive number, for example /alert USD RUB > 95.5.",
		}
	}
	return rateAlert{From: from, To: to, Op: op, Threshold: threshold}, nil
}

func (b *Bot) handleAlertCommand(ctx context.Context, chatID, userID int64, text string) {
	language := b.userLanguage(userID)
	args := commandArgs(text)
	fields := strings.Fields(args)
	switch {
	case args == "":
		_ = b.sendMessage(ctx, chatID, errorText(errAlertUsage, language))
		return
	case isOffValue(fields[0]) || strings.EqualFold(fields[0], "del") || strings.EqualFold(fields[0], "удалить"):
		b.removeAlertCommand(ctx, chatID, userID, fields[1:])
		return
	}

	alert, err := parseAlert(args, b.getSession(userID))
	if err != nil {
		_ = b.sendMessage(ctx, chatID, errorText(err, language))
		return
	}
	alert.ChatID = chatID

	current := tr(language, "текущий курс недоступен", "the current rate is unavailable")
	if snapshot, err := b.rates.Get(ctx); err == nil {
		rate, err := rates.Convert(1, alert.From, alert.To, snapshot)
		if err != nil {
			_ = b.sendMessage(ctx, chatID, checkCurrenciesText(language, errorText(err, language)))
			return
		}
		// Otherwise the alert would fire at the very next check.
		if alert.triggered(rate) {
			_ = b.sendMessage(ctx, chatID, fmt.Sprintf(tr(language,
				"Условие уже выполнено: сейчас 1 %s = %s %s. Алерт не создан, выберите другой порог.",
				"The condition is already met: now 1 %s = %s %s. The alert was not created; choose another threshold."),
				alert.From, formatRate(rate), alert.To))
			return
		}
		current = fmt.Sprintf(tr(language, "сейчас 1 %s = %s %s", "now 1 %s = %s %s"), alert.From, formatRate(rate), alert.To)
	}

	alert, ok := b.addAlert(userID, alert)
	if !ok {
		_ = b.sendMessage(ctx, chatID, fmt.Sprintf(tr(language,
			"Можно держать не больше %d алертов. Удалите лишние: /alerts.",
			"You can keep at most %d alerts. Remove some first: /alerts."), maxAlertsPerUser))
		return
	}
	_ = b.sendMessage(ctx, chatID, fmt.Sprintf(tr(language,
		"Готово: алерт #%d %s (%s). Проверяю при каждом новом курсе ЦБ, сработает один раз.",
		"Done: alert #%d %s (%s). I check it with every new Bank of Russia rate; it fires once."),
		alert.ID, alert.describe(), current))
}

func (b *Bot) removeAlertCommand(ctx context.Context, chatID, userID int64, args []string) {
	language := b.userLanguage(userID)
	if len(args) == 1 && (strings.EqualFold(args[0], "all") || strings.EqualFold(args[0], "все")) {
		if b.removeAllAlerts(userID) {
			_ = b.sendMessage(ctx, chatID, tr(language, "Все алерты удалены.", "All alerts removed."))
			return
		}
		_ = b.sendMessage(ctx, chatID, tr(language, "Алертов нет.", "You have no alerts."))
		return
	}
	id := 0
	if len(args) == 1 {
		id, _ = strconv.Atoi(strings.TrimPrefix(args[0], "#"))
	}
	if id <= 0 {
		_ = b.sendMessage(ctx, chatID, tr(language, "Укажите номер алерта: /alert off 1 или /alert off all. Номера — в /alerts.", "Specify the alert number: /alert off 1 or /alert off all. Numbers are in /alerts."))
		return
	}
	if b.removeAlert(userID, id) {
		_ = b.sendMessage(ctx, chatID, fmt.Sprintf(tr(language, "Алерт #%d удалён.", "Alert #%d removed."), id))
		return
	}
	_ = b.sendMessage(ctx, chatID, fmt.Sprintf(tr(language, "Алерта #%d нет. Список: /alerts.", "There is no alert #%d. List: /alerts."), id))
}

func (b *Bot) showAlerts(ctx context.Context, chatID, userID int64) {
	language := b.userLanguage(userID)
	alerts := b.userAlertList(userID)
	if len(alerts) == 0 {
		_ = b.sendMessage(ctx, chatID, tr(language, "Алертов нет. Создать: /alert USD RUB > 95.", "You have no alerts. Create one: /alert USD RUB > 95."))
		return
	}
	snapshot, err := b.rates.Get(ctx)
	lines := []string{tr(language, "Ваши алерты:", "Your alerts:")}
	buttons := make([][]inlineKeyboardButton, 0, len(alerts))
	for _, alert := range alerts {
		line := fmt.Sprintf("#%d %s", alert.ID, alert.describe())
		if err == nil {
			if rate, err := rates.Convert(1, alert.From, alert.To, snapshot); err == nil {
				line += fmt.Sprintf(tr(language, " (сейчас %s)", " (now %s)"), formatRate(rate))
			}
		}
		lines = append(lines, line)
		buttons = append(buttons, []inlineKeyboardButton{{
			Text:         fmt.Sprintf(tr(language, "Удалить #%d", "Remove #%d"), alert.ID),
			CallbackData: alertOffCallbackPrefix + strconv.Itoa(alert.ID),
		}})
	}
	lines = append(lines, tr(language, "Удалить: /alert off N или кнопкой ниже.", "Remove: /alert off N or with a button below."))
	_ = b.sendMessageWithMarkup(ctx, chatID, strings.Join(lines, "\n"), &inlineKeyboardMarkup{InlineKeyboard: buttons}, "")
}

func (b *Bot) handleAlertOffCallback(ctx context.Context, query callbackQuery, rawID string) {
	userID := query.From.ID
	language := b.userLanguage(userID)
	id, err := strconv.Atoi(rawID)
	if err != nil || id <= 0 {
		_ = b.answerCallbackQuery(ctx, query.ID, tr(language, "Кнопка устарела", "Button expired"))
		return
	}
	// Alerts belong to the user who pressed the button, so in a group one
	// member cannot remove another member's alert.
	if !b.removeAlert(userID, id) {
		_ = b.answerCallbackQuery(ctx, query.ID, fmt.Sprintf(tr(language, "Алерта #%d уже нет", "Alert #%d is already gone"), id))
		return
	}
	_ = b.answerCallbackQuery(ctx, query.ID, fmt.Sprintf(tr(language, "Алерт #%d удалён", "Alert #%d removed"), id))
}

// sendAlerts fires the alerts whose condition holds for latest. A fired
// alert is removed; an undeliverable one is retried like the subscriptions.
func (b *Bot) sendAlerts(ctx context.Context, now time.Time, latest rates.Snapshot) {
	type firing struct {
		userID int64
		alert  rateAlert
		rate   float64
	}
	var firings []firing
	b.alertsMu.Lock()
	for userID, list := range b.alerts {
		for _, alert := range list.Alerts {
			rate, err := rates.Convert(1, alert.From, alert.To, latest)
			if err != nil || !alert.triggered(rate) {
				continue
			}
			if retry, ok := b.alertRetry[alertKey{userID, alert.ID}]; ok && now.Before(retry.Next) {
				continue
			}
			firings = append(firings, firing{userID: userID, alert: alert, rate: rate})
		}
	}
	b.alertsMu.Unlock()

	for _, f := range firings {
		// Access revoked with /disallow: keep the alert, stop sending.
		if !b.isAllowed(f.userID) {
			continue
		}
		language := b.userLanguage(f.userID)
		text := fmt.Sprintf(tr(language, "Сработал алерт #%d: %s\nКурс ЦБ на %s: 1 %s = %s %s", "Alert #%d triggered: %s\nBank of Russia rate for %s: 1 %s = %s %s"),
			f.alert.ID, f.alert.describe(), formatRateDate(latest.Date), f.alert.From, formatRate(f.rate), f.alert.To)
		if err := b.sendMessage(ctx, f.alert.ChatID, text); err != nil {
			b.handleAlertSendError(f.userID, f.alert, now, err)
			continue
		}
		b.removeAlert(f.userID, f.alert.ID)
	}
}

func (b *Bot) handleAlertSendError(userID int64, alert rateAlert, now time.Time, err error) {
	if isChatUnreachable(err) {
		b.log.Warn("alerts removed: chat unreachable", "user_id", userID, "chat_id", alert.ChatID, "error", err)
		b.removeAlertsForChat(userID, alert.ChatID)
		return
	}

	key := alertKey{userID, alert.ID}
	b.alertsMu.Lock()
	retry := b.alertRetry[key]
	giveUp := retry.fail(now)
	if !giveUp {
		b.alertRetry[key] = retry
	}
	b.alertsMu.Unlock()

	if giveUp {
		b.log.Error("send alert failed, removing it", "user_id", userID, "chat_id", alert.ChatID, "alert_id", alert.ID, "error", err)
		b.removeAlert(userID, alert.ID)
		return
	}
	b.log.Warn("send alert failed, will retry", "user_id", userID, "chat_id", alert.ChatID, "alert_id", alert.ID, "retry_at", retry.Next.Format(time.RFC3339), "error", err)
}

func (b *Bot) hasAlerts() bool {
	b.alertsMu.Lock()
	defer b.alertsMu.Unlock()
	for _, list := range b.alerts {
		if len(list.Alerts) > 0 {
			return true
		}
	}
	return false
}

func (b *Bot) userAlertList(userID int64) []rateAlert {
	b.alertsMu.Lock()
	defer b.alertsMu.Unlock()
	return append([]rateAlert(nil), b.alerts[userID].Alerts...)
}

// addAlert assigns the next ID and stores the alert unless the user has
// reached maxAlertsPerUser.
func (b *Bot) addAlert(userID int64, alert rateAlert) (rateAlert, bool) {
	b.alertsMu.Lock()
	list := b.alerts[userID]
	if len(list.Alerts) >= maxAlertsPerUser {
		b.alertsMu.Unlock()
		return rateAlert{}, false
	}
	list.NextID++
	alert.ID = list.NextID
	list.Alerts = append(append([]rateAlert(nil), list.Alerts...), alert)
	b.alerts[userID] = list
	b.alertsMu.Unlock()
	b.saveAlertsLogged()
	return alert, true
}

func (b *Bot) removeAlert(userID int64, id int) bool {
	return b.removeAlertsWhere(userID, func(alert rateAlert) bool { return alert.ID == id })
}

func (b *Bot) removeAlertsForChat(userID, chatID int64) bool {
	return b.removeAlertsWhere(userID, func(alert rateAlert) bool { return alert.ChatID == chatID })
}

func (b *Bot) removeAllAlerts(userID int64) bool {
	return b.removeAlertsWhere(userID, func(rateAlert) bool { return true })
}

func (b *Bot) removeAlertsWhere(userID int64, match func(rateAlert) bool) bool {
	b.alertsMu.Lock()
	list, ok := b.alerts[userID]
	if !ok {
		b.alertsMu.Unlock()
		return false
	}
	kept := make([]rateAlert, 0, len(list.Alerts))
	removed := false
	for _, alert := range list.Alerts {
		if match(alert) {
			removed = true
			delete(b.alertRetry, alertKey{userID, alert.ID})
			continue
		}
		kept = append(kept, alert)
	}
	if len(kept) == 0 {
		// /delete must leave nothing behind; a later alert starts from #1.
		delete(b.alerts, userID)
	} else {
		list.Alerts = kept
		b.alerts[userID] = list
	}
	b.alertsMu.Unlock()
	if removed {
		b.saveAlertsLogged()
	}
	return removed
}

func (b *Bot) loadAlerts() error {
	return b.alertsStore.load(func(raw []byte) error {
		var stored map[int64]userAlerts
		if err := json.Unmarshal(raw, &stored); err != nil {
			return err
		}
		b.alertsMu.Lock()
		defer b.alertsMu.Unlock()
		for userID, list := range stored {
			var alerts []rateAlert
			for _, alert := range list.Alerts {
				alert.From = strings.ToUpper(strings.TrimSpace(alert.From))
				alert.To = strings.ToUpper(strings.TrimSpace(alert.To))
				if alert.ID <= 0 || alert.ChatID == 0 || alert.From == "" || alert.To == "" || !isAlertOp(alert.Op) || alert.Threshold <= 0 {
					continue
				}
				alerts = append(alerts, alert)
				list.NextID = max(list.NextID, alert.ID)
			}
			if len(alerts) > 0 {
				b.alerts[userID] = userAlerts{NextID: list.NextID, Alerts: alerts}
			}
		}
		return nil
	})
}

func (b *Bot) saveAlertsLogged() {
	err := b.alertsStore.save(func() any {
		b.alertsMu.Lock()
		defer b.alertsMu.Unlock()
		result := make(map[int64]userAlerts, len(b.alerts))
		for userID, list := range b.alerts {
			result[userID] = userAlerts{NextID: list.NextID, Alerts: append([]rateAlert(nil), list.Alerts...)}
		}
		return result
	})
	if err != nil {
		b.log.Error("save alerts failed", "path", b.cfg.AlertsFile, "error", err)
	}
}
