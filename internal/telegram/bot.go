package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"currency-converter-bot/internal/config"
	"currency-converter-bot/internal/convert"
	"currency-converter-bot/internal/rates"
)

type Bot struct {
	cfg      config.Config
	rates    *rates.Provider
	client   *http.Client
	log      *slog.Logger
	mu       sync.RWMutex
	sessions map[int64]session
}

type session struct {
	From              string
	To                string
	Multiplier        float64
	ModifyFromPercent float64
	ModifyToPercent   float64
}

func New(cfg config.Config, provider *rates.Provider, logger *slog.Logger) *Bot {
	return &Bot{
		cfg:      cfg,
		rates:    provider,
		client:   &http.Client{Timeout: 70 * time.Second},
		log:      logger,
		sessions: map[int64]session{},
	}
}

func (b *Bot) Run(ctx context.Context) error {
	var offset int64
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		updates, err := b.getUpdates(ctx, offset)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}
			b.log.Warn("get updates failed", "error", err)
			sleep(ctx, 3*time.Second)
			continue
		}

		for _, update := range updates {
			if update.UpdateID >= offset {
				offset = update.UpdateID + 1
			}
			b.handleUpdate(ctx, update)
		}
	}
}

func (b *Bot) handleUpdate(ctx context.Context, update update) {
	if update.Message == nil || update.Message.From == nil {
		return
	}

	userID := update.Message.From.ID
	chatID := update.Message.Chat.ID
	if !b.cfg.IsAllowed(userID) {
		b.log.Warn("blocked user", "user_id", userID, "chat_id", chatID)
		return
	}

	text := strings.TrimSpace(update.Message.Text)
	if text == "" {
		_ = b.sendMessage(ctx, chatID, "Пришлите сумму числом или используйте /from USD и /to RUB.")
		return
	}

	switch {
	case isCommand(text, "/start"), isCommand(text, "/help"):
		_ = b.sendMessage(ctx, chatID, b.helpText(userID))
	case isCommand(text, "/list"):
		_ = b.sendMessage(ctx, chatID, supportedCurrenciesText())
	case isCommand(text, "/settings"):
		b.showSettings(ctx, chatID, userID)
	case isCommand(text, "/from"):
		b.setCurrency(ctx, chatID, userID, text, true)
	case isCommand(text, "/to"):
		b.setCurrency(ctx, chatID, userID, text, false)
	case isCommand(text, "/multi"):
		b.setMultiplier(ctx, chatID, userID, text)
	case isCommand(text, "/modify_from"):
		b.setModifier(ctx, chatID, userID, text, true)
	case isCommand(text, "/modify_to"):
		b.setModifier(ctx, chatID, userID, text, false)
	default:
		b.convertMessage(ctx, chatID, userID, text)
	}
}

func (b *Bot) setCurrency(ctx context.Context, chatID, userID int64, text string, isFrom bool) {
	fields := strings.Fields(text)
	if len(fields) < 2 {
		if isFrom {
			_ = b.sendMessage(ctx, chatID, "Укажите валюту: /from USD")
		} else {
			_ = b.sendMessage(ctx, chatID, "Укажите валюту: /to RUB")
		}
		return
	}

	code := strings.ToUpper(strings.TrimSpace(fields[1]))
	code = strings.TrimPrefix(code, "/")
	if len(code) != 3 {
		_ = b.sendMessage(ctx, chatID, "Код валюты должен быть трехбуквенным, например USD, EUR или RUB.")
		return
	}
	if !isSupportedCurrency(code) {
		_ = b.sendMessage(ctx, chatID, "Такой валюты нет в списке бота. Посмотрите доступные варианты через /list.")
		return
	}

	s := b.getSession(userID)
	if isFrom {
		s.From = code
	} else {
		s.To = code
	}
	b.setSession(userID, s)
	_ = b.sendMessage(ctx, chatID, fmt.Sprintf("Готово: %s -> %s", s.From, s.To))
}

func (b *Bot) setMultiplier(ctx context.Context, chatID, userID int64, text string) {
	fields := strings.Fields(text)
	if len(fields) < 2 {
		_ = b.sendMessage(ctx, chatID, "Укажите множитель: /multi 1000. Для сброса используйте /multi 1.")
		return
	}

	multiplier, err := parseMultiplier(fields[1])
	if err != nil {
		_ = b.sendMessage(ctx, chatID, "Множитель должен быть положительным числом, например 1000, 10.5 или 1.")
		return
	}

	s := b.getSession(userID)
	s.Multiplier = multiplier
	b.setSession(userID, s)
	_ = b.sendMessage(ctx, chatID, fmt.Sprintf("Готово: входная сумма будет умножаться на %s.", formatNumber(multiplier)))
}

func (b *Bot) setModifier(ctx context.Context, chatID, userID int64, text string, isFrom bool) {
	fields := strings.Fields(text)
	command := "/modify_to"
	if isFrom {
		command = "/modify_from"
	}
	if len(fields) < 2 {
		_ = b.sendMessage(ctx, chatID, fmt.Sprintf("Укажите процент: %s 1.5. Для сброса используйте %s 0.", command, command))
		return
	}

	percent, err := parseModifierPercent(fields[1])
	if err != nil {
		_ = b.sendMessage(ctx, chatID, "Процент должен быть числом, например 1.5, +1,5 или -2.")
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
		_ = b.sendMessage(ctx, chatID, fmt.Sprintf("Готово: входная сумма будет изменяться на %s.", formatPercent(percent)))
		return
	}
	_ = b.sendMessage(ctx, chatID, fmt.Sprintf("Готово: результат будет изменяться на %s.", formatPercent(percent)))
}

func (b *Bot) showSettings(ctx context.Context, chatID, userID int64) {
	s := b.getSession(userID)
	snapshot, err := b.rates.Get(ctx)
	if err != nil {
		b.log.Error("rates unavailable", "error", err)
		_ = b.sendMessage(ctx, chatID, settingsText(s, rates.Snapshot{}))
		return
	}
	_ = b.sendMessage(ctx, chatID, settingsText(s, snapshot))
}

func (b *Bot) convertMessage(ctx context.Context, chatID, userID int64, text string) {
	amount, amountCount, err := convert.ParseAmounts(text)
	if err != nil {
		_ = b.sendMessage(ctx, chatID, "Не вижу сумму. Например: 12 345,67 или несколько сумм, каждая с новой строки.")
		return
	}

	s := b.getSession(userID)
	snapshot, err := b.rates.Get(ctx)
	if err != nil {
		b.log.Error("rates unavailable", "error", err)
		_ = b.sendMessage(ctx, chatID, "Не удалось получить курсы валют. Попробуйте чуть позже.")
		return
	}

	multipliedAmount := amount * s.Multiplier
	effectiveAmount := applyPercent(multipliedAmount, s.ModifyFromPercent)
	baseResult, err := rates.Convert(effectiveAmount, s.From, s.To, snapshot)
	if err != nil {
		_ = b.sendMessage(ctx, chatID, fmt.Sprintf("%s. Проверьте /from и /to.", err.Error()))
		return
	}
	result := applyPercent(baseResult, s.ModifyToPercent)

	unitRate, err := rates.Convert(1, s.From, s.To, snapshot)
	if err != nil {
		_ = b.sendMessage(ctx, chatID, fmt.Sprintf("%s. Проверьте /from и /to.", err.Error()))
		return
	}
	unitRate = applyPercent(applyPercent(unitRate*s.Multiplier, s.ModifyFromPercent), s.ModifyToPercent)

	amountText := formatAmountWithSettings(amount, multipliedAmount, effectiveAmount, s.From)
	replyPrefix := fmt.Sprintf("%s = %s %s", amountText, convert.FormatMoney(result), s.To)
	if amountCount > 1 {
		replyPrefix = fmt.Sprintf("Итого: %s = %s %s\nСтрок учтено: %d", amountText, convert.FormatMoney(result), s.To, amountCount)
	}

	unitLabel := fmt.Sprintf("Курс: 1 %s", s.From)
	if hasInputSettings(s) {
		unitLabel = "Расчет для 1 введенной единицы"
	}

	reply := fmt.Sprintf(
		"%s\n%s = %s %s",
		replyPrefix,
		unitLabel,
		convert.FormatMoney(unitRate),
		s.To,
	)
	_ = b.sendMessage(ctx, chatID, reply)
}

func (b *Bot) helpText(userID int64) string {
	s := b.getSession(userID)
	return fmt.Sprintf("Я конвертирую валюты по официальным курсам ЦБ РФ и отвечаю только пользователям из whitelist.\n\nТекущая пара: %s -> %s\n\nКоманды:\n/from USD - выбрать исходную валюту\n/to RUB - выбрать валюту результата\n/multi 1000 - умножать входную сумму перед расчетом\n/modify_from 1.5 - изменить входную сумму на процент перед расчетом\n/modify_to 1.5 - изменить результат на процент после расчета\n/settings - текущие настройки\n/list - список поддерживаемых валют\n/help - эта справка\n\nПосле выбора пары пришлите сумму, например: 12 345,67. Можно прислать несколько сумм, каждую с новой строки, я сложу их и переведу итог.", s.From, s.To)
}

func (b *Bot) getSession(userID int64) session {
	b.mu.RLock()
	s, ok := b.sessions[userID]
	b.mu.RUnlock()
	if ok {
		return normalizeSession(s)
	}
	return session{From: b.cfg.DefaultFrom, To: b.cfg.DefaultTo, Multiplier: 1}
}

func (b *Bot) setSession(userID int64, s session) {
	b.mu.Lock()
	b.sessions[userID] = normalizeSession(s)
	b.mu.Unlock()
}

func (b *Bot) getUpdates(ctx context.Context, offset int64) ([]update, error) {
	var result getUpdatesResponse
	err := b.post(ctx, "getUpdates", map[string]any{
		"offset":          offset,
		"timeout":         60,
		"allowed_updates": []string{"message"},
	}, &result)
	if err != nil {
		return nil, err
	}
	if !result.OK {
		return nil, fmt.Errorf("telegram getUpdates failed: %s", result.Description)
	}
	return result.Result, nil
}

func (b *Bot) sendMessage(ctx context.Context, chatID int64, text string) error {
	var result apiResponse
	err := b.post(ctx, "sendMessage", map[string]any{
		"chat_id": chatID,
		"text":    text,
	}, &result)
	if err != nil {
		return err
	}
	if !result.OK {
		return fmt.Errorf("telegram sendMessage failed: %s", result.Description)
	}
	return nil
}

func (b *Bot) post(ctx context.Context, method string, payload any, target any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	url := fmt.Sprintf("%s/bot%s/%s", b.cfg.TelegramAPI, b.cfg.TelegramToken, method)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := b.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("telegram %s failed: %s", method, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(target)
}

func isCommand(text, command string) bool {
	text = strings.TrimSpace(strings.ToLower(text))
	return text == command || strings.HasPrefix(text, command+" ") || strings.HasPrefix(text, command+"@")
}

func sleep(ctx context.Context, d time.Duration) {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

func parseModifierPercent(raw string) (float64, error) {
	raw = strings.TrimSpace(strings.ReplaceAll(raw, ",", "."))
	if raw == "" {
		return 0, errors.New("empty percent")
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
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
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value <= 0 {
		return 0, errors.New("invalid multiplier")
	}
	return value, nil
}

func applyPercent(value, percent float64) float64 {
	return value * (1 + percent/100)
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

func normalizeSession(s session) session {
	if s.Multiplier == 0 {
		s.Multiplier = 1
	}
	return s
}

func hasInputSettings(s session) bool {
	s = normalizeSession(s)
	return s.Multiplier != 1 || s.ModifyFromPercent != 0 || s.ModifyToPercent != 0
}

func formatNumber(value float64) string {
	formatted := convert.FormatMoney(value)
	formatted = strings.TrimRight(formatted, "0")
	return strings.TrimRight(formatted, ",")
}

func settingsText(s session, snapshot rates.Snapshot) string {
	s = normalizeSession(s)
	updatedAt := "нет данных"
	if !snapshot.FetchedAt.IsZero() {
		updatedAt = snapshot.FetchedAt.UTC().Format("2006-01-02 15:04:05 UTC")
	}

	return fmt.Sprintf(
		"Настройки:\nПара: %s -> %s\nКурсы обновлены: %s\nМножитель входной суммы: %s\nМодификатор входной суммы: %s\nМодификатор результата: %s\n\nДругие настройки будут добавлены сюда позже.",
		s.From,
		s.To,
		updatedAt,
		formatNumber(s.Multiplier),
		formatPercent(s.ModifyFromPercent),
		formatPercent(s.ModifyToPercent),
	)
}

func formatPercent(value float64) string {
	formatted := formatNumber(value)
	if value > 0 {
		return "+" + formatted + "%"
	}
	return formatted + "%"
}

type apiResponse struct {
	OK          bool   `json:"ok"`
	Description string `json:"description"`
}

type getUpdatesResponse struct {
	OK          bool     `json:"ok"`
	Description string   `json:"description"`
	Result      []update `json:"result"`
}

type update struct {
	UpdateID int64    `json:"update_id"`
	Message  *message `json:"message"`
}

type message struct {
	MessageID int64  `json:"message_id"`
	From      *user  `json:"from"`
	Chat      chat   `json:"chat"`
	Text      string `json:"text"`
}

type user struct {
	ID int64 `json:"id"`
}

type chat struct {
	ID int64 `json:"id"`
}
