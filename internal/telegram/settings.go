package telegram

import (
	"context"
	"currency-converter-bot/internal/rates"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

type session struct {
	Language          string   `json:"language,omitempty"`
	From              string   `json:"from"`
	To                string   `json:"to"`
	With              []string `json:"with,omitempty"`
	WithModify        bool     `json:"with_modify"`
	InlineModify      bool     `json:"inline_modify"`
	Multiplier        float64  `json:"multiplier"`
	Round             string   `json:"round,omitempty"`
	ModifyFromPercent float64  `json:"modify_from_percent"`
	ModifyToPercent   float64  `json:"modify_to_percent"`
	// Timezone is an IANA name or "UTC+03:00"; empty means SUBSCRIPTION_TIMEZONE.
	Timezone string `json:"timezone,omitempty"`
}

func (s *session) UnmarshalJSON(raw []byte) error {
	type sessionAlias session
	var aux struct {
		sessionAlias
		With json.RawMessage `json:"with"`
	}
	if err := json.Unmarshal(raw, &aux); err != nil {
		return err
	}

	*s = session(aux.sessionAlias)
	if len(aux.With) == 0 || string(aux.With) == "null" {
		return nil
	}

	var list []string
	if err := json.Unmarshal(aux.With, &list); err == nil {
		s.With = list
		return nil
	}

	var single string
	if err := json.Unmarshal(aux.With, &single); err != nil {
		return err
	}
	if strings.TrimSpace(single) != "" {
		s.With = []string{single}
	}
	return nil
}

func (b *Bot) setCurrency(ctx context.Context, chatID, userID int64, text string, isFrom bool) {
	language := b.userLanguage(userID)
	fields := strings.Fields(text)
	if len(fields) < 2 {
		if isFrom {
			_ = b.sendMessage(ctx, chatID, tr(language, "Укажите валюту: /from USD", "Specify a currency: /from USD"))
		} else {
			_ = b.sendMessage(ctx, chatID, tr(language, "Укажите валюту: /to RUB", "Specify a currency: /to RUB"))
		}
		return
	}

	code, ok := resolveCurrencyToken(fields[1])
	if !ok {
		_ = b.sendMessage(ctx, chatID, invalidCurrencyText(language))
		return
	}

	s := b.getSession(userID)
	if isFrom {
		s.From = code
	} else {
		s.To = code
	}
	b.setSession(userID, s)
	_ = b.sendMessage(ctx, chatID, fmt.Sprintf(tr(language, "Готово: %s -> %s", "Done: %s -> %s"), s.From, s.To))
}

func (b *Bot) setWithCurrency(ctx context.Context, chatID, userID int64, text string) {
	language := b.userLanguage(userID)
	fields := strings.Fields(text)
	if len(fields) < 2 {
		_ = b.sendMessage(ctx, chatID, tr(language, "Укажите валюты: /with USD EUR RUB. Для отключения используйте /with off.", "Specify currencies: /with USD EUR RUB. Use /with off to disable."))
		return
	}

	if len(fields) == 2 && isOffValue(fields[1]) {
		s := b.getSession(userID)
		s.With = nil
		b.setSession(userID, s)
		_ = b.sendMessage(ctx, chatID, tr(language, "Готово: кнопки дополнительного перевода отключены.", "Done: additional conversion buttons are disabled."))
		return
	}

	codes, err := parseCurrencyList(fields[1:])
	if err != nil {
		_ = b.sendMessage(ctx, chatID, errorText(err, language))
		return
	}

	s := b.getSession(userID)
	s.With = codes
	b.setSession(userID, s)
	_ = b.sendMessage(ctx, chatID, fmt.Sprintf(tr(language, "Готово: в ответах будут кнопки: %s.", "Done: replies will include buttons for: %s."), strings.Join(codes, ", ")))
}

func (b *Bot) setWithModify(ctx context.Context, chatID, userID int64, text string) {
	language := b.userLanguage(userID)
	fields := strings.Fields(text)
	if len(fields) < 2 {
		_ = b.sendMessage(ctx, chatID, tr(language, "Укажите yes или no: /with_modify yes.", "Specify yes or no: /with_modify yes."))
		return
	}

	value, err := parseYesNo(fields[1])
	if err != nil {
		_ = b.sendMessage(ctx, chatID, tr(language, "Значение должно быть yes или no.", "The value must be yes or no."))
		return
	}

	s := b.getSession(userID)
	s.WithModify = value
	b.setSession(userID, s)
	if value {
		_ = b.sendMessage(ctx, chatID, tr(language, "Готово: кнопки /with будут учитывать modify_from и modify_to.", "Done: /with buttons will apply modify_from and modify_to."))
		return
	}
	_ = b.sendMessage(ctx, chatID, tr(language, "Готово: кнопки /with не будут учитывать modify_from и modify_to.", "Done: /with buttons will not apply modify_from and modify_to."))
}

func (b *Bot) setInlineModify(ctx context.Context, chatID, userID int64, text string) {
	language := b.userLanguage(userID)
	fields := strings.Fields(text)
	if len(fields) < 2 {
		_ = b.sendMessage(ctx, chatID, tr(language, "Укажите yes или no: /inline_modify yes.", "Specify yes or no: /inline_modify yes."))
		return
	}

	value, err := parseYesNo(fields[1])
	if err != nil {
		_ = b.sendMessage(ctx, chatID, tr(language, "Значение должно быть yes или no.", "The value must be yes or no."))
		return
	}

	s := b.getSession(userID)
	s.InlineModify = value
	b.setSession(userID, s)
	if value {
		_ = b.sendMessage(ctx, chatID, tr(language, "Готово: явные валюты в тексте будут учитывать modify_from и modify_to.", "Done: explicit currencies in text will apply modify_from and modify_to."))
		return
	}
	_ = b.sendMessage(ctx, chatID, tr(language, "Готово: явные валюты в тексте не будут учитывать modify_from и modify_to.", "Done: explicit currencies in text will not apply modify_from and modify_to."))
}

func (b *Bot) setMultiplier(ctx context.Context, chatID, userID int64, text string) {
	language := b.userLanguage(userID)
	fields := strings.Fields(text)
	if len(fields) < 2 {
		_ = b.sendMessage(ctx, chatID, tr(language, "Укажите множитель: /multi 1000. Для сброса используйте /multi 1.", "Specify a multiplier: /multi 1000. Use /multi 1 to reset."))
		return
	}

	multiplier, err := parseMultiplier(fields[1])
	if err != nil {
		_ = b.sendMessage(ctx, chatID, tr(language, "Множитель должен быть положительным числом не больше миллиарда, например 1000, 10.5 или 1.", "The multiplier must be a positive number up to one billion, such as 1000, 10.5, or 1."))
		return
	}

	s := b.getSession(userID)
	s.Multiplier = multiplier
	b.setSession(userID, s)
	_ = b.sendMessage(ctx, chatID, fmt.Sprintf(tr(language, "Готово: входная сумма будет умножаться на %s.", "Done: the input amount will be multiplied by %s."), formatNumber(multiplier)))
}

func (b *Bot) setRound(ctx context.Context, chatID, userID int64, text string) {
	language := b.userLanguage(userID)
	fields := strings.Fields(text)
	if len(fields) < 2 {
		_ = b.sendMessage(ctx, chatID, tr(language, "Укажите округление: /round auto, /round 0, /round 2, /round 4 или /round 6.", "Specify rounding: /round auto, /round 0, /round 2, /round 4, or /round 6."))
		return
	}

	round, err := parseRoundMode(fields[1])
	if err != nil {
		_ = b.sendMessage(ctx, chatID, tr(language, "Округление должно быть auto, 0, 2, 4 или 6.", "Rounding must be auto, 0, 2, 4, or 6."))
		return
	}

	s := b.getSession(userID)
	s.Round = round
	b.setSession(userID, s)
	_ = b.sendMessage(ctx, chatID, fmt.Sprintf(tr(language, "Готово: округление результата — %s.", "Done: result rounding is %s."), formatRoundMode(round)))
}

func (b *Bot) setModifier(ctx context.Context, chatID, userID int64, text string, isFrom bool) {
	language := b.userLanguage(userID)
	fields := strings.Fields(text)
	command := "/modify_to"
	if isFrom {
		command = "/modify_from"
	}
	if len(fields) < 2 {
		_ = b.sendMessage(ctx, chatID, fmt.Sprintf(tr(language, "Укажите процент: %s 1.5. Для сброса используйте %s 0.", "Specify a percentage: %s 1.5. Use %s 0 to reset."), command, command))
		return
	}

	percent, err := parseModifierPercent(fields[1])
	if err != nil {
		_ = b.sendMessage(ctx, chatID, tr(language, "Процент должен быть числом больше -100 и не больше 1000, например 1.5, +1,5 или -2.", "The percentage must be a number above -100 and up to 1000, such as 1.5, +1.5, or -2."))
		return
	}

	s := b.getSession(userID)
	if isFrom {
		s.ModifyFromPercent = percent
	} else {
		s.ModifyToPercent = percent
	}
	b.setSession(userID, s)

	if isFrom {
		_ = b.sendMessage(ctx, chatID, fmt.Sprintf(tr(language, "Готово: входная сумма будет изменяться на %s.", "Done: the input amount will be adjusted by %s."), formatPercent(percent)))
		return
	}
	_ = b.sendMessage(ctx, chatID, fmt.Sprintf(tr(language, "Готово: результат будет изменяться на %s.", "Done: the result will be adjusted by %s."), formatPercent(percent)))
}

func (b *Bot) showSettings(ctx context.Context, chatID, userID int64) {
	s := b.getSession(userID)
	snapshot, err := b.rates.Get(ctx)
	if err != nil {
		b.log.Error("rates unavailable", "error", err)
		snapshot = rates.Snapshot{}
	}
	language := b.userLanguage(userID)
	_ = b.sendMessage(ctx, chatID, settingsTextForLanguage(s, snapshot, language, b.timezoneLabel(userID, language)))
}

func (b *Bot) resetSettings(ctx context.Context, chatID, userID int64) {
	language := b.userLanguage(userID)
	timezone := b.getSession(userID).Timezone
	s := defaultSession(b.cfg.DefaultFrom, b.cfg.DefaultTo)
	s.Language = language
	s.Timezone = timezone
	b.setSession(userID, s)
	_ = b.sendMessage(ctx, chatID, fmt.Sprintf(tr(language, "Настройки сброшены: %s -> %s.", "Settings reset: %s -> %s."), s.From, s.To))
}

func (b *Bot) swapCurrencies(ctx context.Context, chatID, userID int64) {
	s := b.getSession(userID)
	s.From, s.To = s.To, s.From
	b.setSession(userID, s)
	_ = b.sendMessage(ctx, chatID, fmt.Sprintf(tr(b.userLanguage(userID), "Готово: %s -> %s", "Done: %s -> %s"), s.From, s.To))
}

func (b *Bot) deleteSettings(ctx context.Context, chatID, userID int64) {
	language := b.userLanguage(userID)
	b.deleteSession(userID)
	b.removeUserSubscription(userID)
	_ = b.sendMessage(ctx, chatID, tr(language, "Ваши данные удалены: настройки, выбор языка и подписка. При следующем сообщении нужно будет снова выбрать язык.", "Your data has been deleted: settings, language choice, and subscription. You will need to choose a language again with your next message."))
}

func (b *Bot) getSession(userID int64) session {
	b.mu.RLock()
	s, ok := b.sessions[userID]
	b.mu.RUnlock()
	if ok {
		return normalizeSession(s, b.cfg.DefaultFrom, b.cfg.DefaultTo)
	}
	return defaultSession(b.cfg.DefaultFrom, b.cfg.DefaultTo)
}

func (b *Bot) setSession(userID int64, s session) {
	s = normalizeSession(s, b.cfg.DefaultFrom, b.cfg.DefaultTo)
	b.mu.Lock()
	b.sessions[userID] = s
	b.mu.Unlock()

	if err := b.saveSessions(); err != nil {
		b.log.Error("save user settings failed", "path", b.cfg.UserSettingsFile, "error", err)
	}
}

func (b *Bot) deleteSession(userID int64) {
	b.mu.Lock()
	delete(b.sessions, userID)
	b.mu.Unlock()

	if err := b.saveSessions(); err != nil {
		b.log.Error("delete user settings failed", "path", b.cfg.UserSettingsFile, "error", err)
	}
}

// Limits for user settings: beyond them results stop being meaningful and
// numbers overflow formatting. -100% would turn every amount into zero.
const (
	maxMultiplier      = 1e9
	maxModifierPercent = 1000
)

func parseModifierPercent(raw string) (float64, error) {
	raw = strings.TrimSpace(strings.ReplaceAll(raw, ",", "."))
	if raw == "" {
		return 0, errors.New("empty percent")
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value <= -100 || value > maxModifierPercent {
		return 0, errors.New("invalid percent")
	}
	return value, nil
}

func parseMultiplier(raw string) (float64, error) {
	raw = strings.TrimSpace(strings.ReplaceAll(raw, ",", "."))
	if raw == "" {
		return 0, errors.New("empty multiplier")
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value <= 0 || value > maxMultiplier {
		return 0, errors.New("invalid multiplier")
	}
	return value, nil
}

func parseRoundMode(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "auto", "default", "по", "авто":
		return "", nil
	case "0", "2", "4", "6":
		return strings.TrimSpace(raw), nil
	default:
		return "", errors.New("invalid round mode")
	}
}

func roundPrecision(roundMode string) (int, bool) {
	switch roundMode {
	case "0":
		return 0, true
	case "2":
		return 2, true
	case "4":
		return 4, true
	case "6":
		return 6, true
	default:
		return 0, false
	}
}

func parseYesNo(raw string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "yes", "y", "true", "1", "да":
		return true, nil
	case "no", "n", "false", "0", "нет":
		return false, nil
	default:
		return false, errors.New("invalid yes/no")
	}
}

func parseCurrencyList(raw []string) ([]string, error) {
	codes := make([]string, 0, len(raw))
	seen := map[string]struct{}{}
	for _, part := range raw {
		if strings.TrimSpace(part) == "" {
			continue
		}
		code, ok := resolveCurrencyToken(part)
		if !ok {
			return nil, userError{
				ru: "Такой валюты нет в списке бота. Посмотрите доступные варианты через /list.",
				en: "That currency is not supported. See the available currencies with /list.",
			}
		}
		if _, ok := seen[code]; ok {
			continue
		}
		seen[code] = struct{}{}
		codes = append(codes, code)
	}
	if len(codes) == 0 {
		return nil, userError{
			ru: "Укажите хотя бы одну валюту: /with USD EUR RUB.",
			en: "Specify at least one currency: /with USD EUR RUB.",
		}
	}
	return codes, nil
}

func normalizeSession(s session, defaultFrom, defaultTo string) session {
	s.Language = normalizeLanguage(s.Language)
	if strings.TrimSpace(s.From) == "" {
		s.From = defaultFrom
	}
	if strings.TrimSpace(s.To) == "" {
		s.To = defaultTo
	}
	s.From = strings.ToUpper(strings.TrimSpace(s.From))
	s.To = strings.ToUpper(strings.TrimSpace(s.To))
	s.With = normalizeCurrencyList(s.With)
	s.Round, _ = parseRoundMode(s.Round)
	s.Timezone = strings.TrimSpace(s.Timezone)
	if s.Multiplier == 0 {
		s.Multiplier = 1
	}
	return s
}

func defaultSession(defaultFrom, defaultTo string) session {
	return normalizeSession(session{
		From:       defaultFrom,
		To:         defaultTo,
		Multiplier: 1,
	}, defaultFrom, defaultTo)
}

func formatYesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func formatRoundMode(roundMode string) string {
	if strings.TrimSpace(roundMode) == "" {
		return "auto"
	}
	return roundMode
}

func settingsText(s session, snapshot rates.Snapshot) string {
	return settingsTextForLanguage(s, snapshot, languageRussian, s.Timezone)
}

func settingsTextForLanguage(s session, snapshot rates.Snapshot, language, timezone string) string {
	s = normalizeSession(s, "", "")
	updatedAt := tr(language, "нет данных", "no data")
	if !snapshot.FetchedAt.IsZero() {
		updatedAt = snapshot.FetchedAt.UTC().Format("2006-01-02 15:04:05 UTC")
	}
	with := tr(language, "выключены", "disabled")
	if len(s.With) > 0 {
		with = strings.Join(s.With, ", ")
	}

	return fmt.Sprintf(
		tr(language, "Настройки:\nЯзык: %s\nПара: %s -> %s\nКурсы обновлены: %s\nКнопки перевода: %s\nМодификаторы для кнопок: %s\nМодификаторы для явных валют: %s\nМножитель входной суммы: %s\nОкругление результата: %s\nМодификатор входной суммы: %s\nМодификатор результата: %s\nЧасовой пояс: %s", "Settings:\nLanguage: %s\nPair: %s -> %s\nRates updated: %s\nConversion buttons: %s\nButton modifiers: %s\nExplicit-currency modifiers: %s\nInput multiplier: %s\nResult rounding: %s\nInput modifier: %s\nResult modifier: %s\nTime zone: %s"),
		languageName(language),
		s.From,
		s.To,
		updatedAt,
		with,
		formatYesNo(s.WithModify),
		formatYesNo(s.InlineModify),
		formatNumber(s.Multiplier),
		formatRoundMode(s.Round),
		formatPercent(s.ModifyFromPercent),
		formatPercent(s.ModifyToPercent),
		timezone,
	)
}

func isOffValue(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "off", "no", "none", "0", "-", "нет":
		return true
	default:
		return false
	}
}

func normalizeCurrencyList(codes []string) []string {
	if len(codes) == 0 {
		return nil
	}
	result := make([]string, 0, len(codes))
	seen := map[string]struct{}{}
	for _, code := range codes {
		code = strings.ToUpper(strings.TrimSpace(code))
		if code == "" {
			continue
		}
		if _, ok := seen[code]; ok {
			continue
		}
		seen[code] = struct{}{}
		result = append(result, code)
	}
	return result
}

func (b *Bot) loadSessions() error {
	return b.sessionsStore.load(func(raw []byte) error {
		var sessions map[int64]session
		if err := json.Unmarshal(raw, &sessions); err != nil {
			return err
		}
		b.mu.Lock()
		for userID, s := range sessions {
			s = normalizeSession(s, b.cfg.DefaultFrom, b.cfg.DefaultTo)
			if s.Timezone != "" {
				if _, err := loadTimezone(s.Timezone); err != nil {
					b.log.Warn("unknown user time zone, using the default", "user_id", userID, "timezone", s.Timezone, "error", err)
					s.Timezone = ""
				}
			}
			b.sessions[userID] = s
		}
		b.mu.Unlock()
		return nil
	})
}

func (b *Bot) saveSessions() error {
	return b.sessionsStore.save(func() any {
		b.mu.RLock()
		defer b.mu.RUnlock()
		return copySessions(b.sessions)
	})
}

func copySessions(sessions map[int64]session) map[int64]session {
	result := make(map[int64]session, len(sessions))
	for userID, s := range sessions {
		result[userID] = s
	}
	return result
}
