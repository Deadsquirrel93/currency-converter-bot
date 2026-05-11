package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"math"
	"net/http"
	"os"
	"path/filepath"
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
	From              string   `json:"from"`
	To                string   `json:"to"`
	With              []string `json:"with,omitempty"`
	WithModify        bool     `json:"with_modify"`
	Multiplier        float64  `json:"multiplier"`
	Round             string   `json:"round,omitempty"`
	ModifyFromPercent float64  `json:"modify_from_percent"`
	ModifyToPercent   float64  `json:"modify_to_percent"`
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

func New(cfg config.Config, provider *rates.Provider, logger *slog.Logger) *Bot {
	b := &Bot{
		cfg:      cfg,
		rates:    provider,
		client:   &http.Client{Timeout: 70 * time.Second},
		log:      logger,
		sessions: map[int64]session{},
	}
	if err := b.loadSessions(); err != nil {
		b.log.Warn("load user settings failed", "error", err)
	}
	return b
}

func (b *Bot) Run(ctx context.Context) error {
	if err := b.setBotCommands(ctx); err != nil && !errors.Is(err, context.Canceled) {
		b.log.Warn("set bot commands failed", "error", err)
	}

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
	if update.InlineQuery != nil {
		b.handleInlineQuery(ctx, *update.InlineQuery)
		return
	}
	if update.CallbackQuery != nil {
		b.handleCallbackQuery(ctx, *update.CallbackQuery)
		return
	}
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
	case isCommand(text, "/rate"):
		b.showRate(ctx, chatID, userID, text)
	case isCommand(text, "/swap"):
		b.swapCurrencies(ctx, chatID, userID)
	case isCommand(text, "/reset"):
		b.resetSettings(ctx, chatID, userID)
	case isCommand(text, "/delete"):
		b.deleteSettings(ctx, chatID, userID)
	case isCommand(text, "/from"):
		b.setCurrency(ctx, chatID, userID, text, true)
	case isCommand(text, "/to"):
		b.setCurrency(ctx, chatID, userID, text, false)
	case isCommand(text, "/with"):
		b.setWithCurrency(ctx, chatID, userID, text)
	case isCommand(text, "/with_modify"):
		b.setWithModify(ctx, chatID, userID, text)
	case isCommand(text, "/multi"):
		b.setMultiplier(ctx, chatID, userID, text)
	case isCommand(text, "/round"):
		b.setRound(ctx, chatID, userID, text)
	case isCommand(text, "/modify_from"):
		b.setModifier(ctx, chatID, userID, text, true)
	case isCommand(text, "/modify_to"):
		b.setModifier(ctx, chatID, userID, text, false)
	default:
		b.convertMessage(ctx, chatID, userID, text)
	}
}

func (b *Bot) handleCallbackQuery(ctx context.Context, query callbackQuery) {
	if query.From == nil {
		return
	}
	userID := query.From.ID
	if !b.cfg.IsAllowed(userID) {
		b.log.Warn("blocked user callback", "user_id", userID)
		_ = b.answerCallbackQuery(ctx, query.ID, "Нет доступа")
		return
	}
	if query.Message == nil {
		_ = b.answerCallbackQuery(ctx, query.ID, "Сообщение недоступно")
		return
	}

	request, err := parseWithCallbackData(query.Data)
	if err != nil {
		_ = b.answerCallbackQuery(ctx, query.ID, "Кнопка устарела")
		return
	}

	snapshot, err := b.rates.Get(ctx)
	if err != nil {
		b.log.Error("rates unavailable", "error", err)
		_ = b.answerCallbackQuery(ctx, query.ID, "Курсы недоступны")
		return
	}

	s := b.getSession(userID)
	reply, err := conversionReply(request.Amount, 1, request.From, request.To, request.Multiplier, request.ModifyFromPercent, request.ModifyToPercent, request.UseModify, s.Round, snapshot)
	if err != nil {
		_ = b.answerCallbackQuery(ctx, query.ID, "Не удалось перевести")
		_ = b.sendMessage(ctx, query.Message.Chat.ID, fmt.Sprintf("%s. Проверьте настройки.", err.Error()))
		return
	}

	_ = b.answerCallbackQuery(ctx, query.ID, "")
	_ = b.sendHTMLMessage(ctx, query.Message.Chat.ID, reply)
}

func (b *Bot) handleInlineQuery(ctx context.Context, query inlineQuery) {
	if query.From == nil {
		return
	}
	userID := query.From.ID
	if !b.cfg.IsAllowed(userID) {
		b.log.Warn("blocked user inline query", "user_id", userID)
		_ = b.answerInlineQuery(ctx, query.ID, nil)
		return
	}

	text := strings.TrimSpace(query.Query)
	if text == "" {
		_ = b.answerInlineQuery(ctx, query.ID, nil)
		return
	}

	s := b.getSession(userID)
	request, err := parseConversionInput(text, s)
	if err != nil {
		_ = b.answerInlineQuery(ctx, query.ID, nil)
		return
	}

	snapshot, err := b.rates.Get(ctx)
	if err != nil {
		b.log.Error("rates unavailable", "error", err)
		_ = b.answerInlineQuery(ctx, query.ID, nil)
		return
	}

	reply, err := conversionReply(request.Amount, request.AmountCount, request.From, request.To, s.Multiplier, s.ModifyFromPercent, s.ModifyToPercent, true, s.Round, snapshot)
	if err != nil {
		_ = b.answerInlineQuery(ctx, query.ID, nil)
		return
	}

	_ = b.answerInlineQuery(ctx, query.ID, []inlineQueryResultArticle{
		inlineConversionResult(reply),
	})
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

	code, ok := resolveCurrencyToken(fields[1])
	if !ok {
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

func (b *Bot) setWithCurrency(ctx context.Context, chatID, userID int64, text string) {
	fields := strings.Fields(text)
	if len(fields) < 2 {
		_ = b.sendMessage(ctx, chatID, "Укажите валюты: /with USD EUR RUB. Для отключения используйте /with off.")
		return
	}

	if len(fields) == 2 && isOffValue(fields[1]) {
		s := b.getSession(userID)
		s.With = nil
		b.setSession(userID, s)
		_ = b.sendMessage(ctx, chatID, "Готово: кнопки дополнительного перевода отключены.")
		return
	}

	codes, err := parseCurrencyList(fields[1:])
	if err != nil {
		_ = b.sendMessage(ctx, chatID, err.Error())
		return
	}

	s := b.getSession(userID)
	s.With = codes
	b.setSession(userID, s)
	_ = b.sendMessage(ctx, chatID, fmt.Sprintf("Готово: в ответах будут кнопки: %s.", strings.Join(codes, ", ")))
}

func (b *Bot) setWithModify(ctx context.Context, chatID, userID int64, text string) {
	fields := strings.Fields(text)
	if len(fields) < 2 {
		_ = b.sendMessage(ctx, chatID, "Укажите yes или no: /with_modify yes.")
		return
	}

	value, err := parseYesNo(fields[1])
	if err != nil {
		_ = b.sendMessage(ctx, chatID, "Значение должно быть yes или no.")
		return
	}

	s := b.getSession(userID)
	s.WithModify = value
	b.setSession(userID, s)
	if value {
		_ = b.sendMessage(ctx, chatID, "Готово: кнопки /with будут учитывать modify_from и modify_to.")
		return
	}
	_ = b.sendMessage(ctx, chatID, "Готово: кнопки /with не будут учитывать modify_from и modify_to.")
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

func (b *Bot) setRound(ctx context.Context, chatID, userID int64, text string) {
	fields := strings.Fields(text)
	if len(fields) < 2 {
		_ = b.sendMessage(ctx, chatID, "Укажите округление: /round auto, /round 0, /round 2, /round 4 или /round 6.")
		return
	}

	round, err := parseRoundMode(fields[1])
	if err != nil {
		_ = b.sendMessage(ctx, chatID, "Округление должно быть auto, 0, 2, 4 или 6.")
		return
	}

	s := b.getSession(userID)
	s.Round = round
	b.setSession(userID, s)
	_ = b.sendMessage(ctx, chatID, fmt.Sprintf("Готово: округление результата - %s.", formatRoundMode(round)))
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

func (b *Bot) resetSettings(ctx context.Context, chatID, userID int64) {
	s := defaultSession(b.cfg.DefaultFrom, b.cfg.DefaultTo)
	b.setSession(userID, s)
	_ = b.sendMessage(ctx, chatID, fmt.Sprintf("Настройки сброшены: %s -> %s.", s.From, s.To))
}

func (b *Bot) swapCurrencies(ctx context.Context, chatID, userID int64) {
	s := b.getSession(userID)
	s.From, s.To = s.To, s.From
	b.setSession(userID, s)
	_ = b.sendMessage(ctx, chatID, fmt.Sprintf("Готово: %s -> %s", s.From, s.To))
}

func (b *Bot) deleteSettings(ctx context.Context, chatID, userID int64) {
	b.deleteSession(userID)
	_ = b.sendMessage(ctx, chatID, "Ваши сохраненные настройки удалены. При следующем сообщении будут использоваться настройки по умолчанию.")
}

func (b *Bot) showRate(ctx context.Context, chatID, userID int64, text string) {
	s := b.getSession(userID)
	request, err := parseRateRequest(text, s)
	if err != nil {
		_ = b.sendMessage(ctx, chatID, err.Error())
		return
	}

	snapshot, err := b.rates.Get(ctx)
	if err != nil {
		b.log.Error("rates unavailable", "error", err)
		_ = b.sendMessage(ctx, chatID, "Не удалось получить курсы валют. Попробуйте чуть позже.")
		return
	}

	reply, err := rateReply(request.From, request.To, snapshot)
	if err != nil {
		_ = b.sendMessage(ctx, chatID, fmt.Sprintf("%s. Проверьте валюты.", err.Error()))
		return
	}
	_ = b.sendMessage(ctx, chatID, reply)
}

func (b *Bot) convertMessage(ctx context.Context, chatID, userID int64, text string) {
	s := b.getSession(userID)
	request, err := parseConversionInput(text, s)
	if err != nil {
		_ = b.sendMessage(ctx, chatID, "Не вижу сумму. Например: 12 345,67 или несколько сумм, каждая с новой строки.")
		return
	}

	snapshot, err := b.rates.Get(ctx)
	if err != nil {
		b.log.Error("rates unavailable", "error", err)
		_ = b.sendMessage(ctx, chatID, "Не удалось получить курсы валют. Попробуйте чуть позже.")
		return
	}

	reply, err := conversionReply(request.Amount, request.AmountCount, request.From, request.To, s.Multiplier, s.ModifyFromPercent, s.ModifyToPercent, true, s.Round, snapshot)
	if err != nil {
		_ = b.sendMessage(ctx, chatID, fmt.Sprintf("%s. Проверьте валюты.", err.Error()))
		return
	}

	var markup *inlineKeyboardMarkup
	if len(s.With) > 0 {
		buttonSession := s
		buttonSession.From = request.From
		markup = withReplyMarkup(request.Amount, buttonSession)
	}
	_ = b.sendHTMLMessageWithMarkup(ctx, chatID, reply, markup)
}

func (b *Bot) helpText(userID int64) string {
	s := b.getSession(userID)
	return fmt.Sprintf("Я конвертирую валюты по официальным курсам ЦБ РФ. Если whitelist пустой, я доступен всем; если задан, отвечаю только разрешенным Telegram ID.\n\nТекущая пара: %s -> %s\n\nКоманды:\n/from USD - выбрать исходную валюту\n/to RUB - выбрать валюту результата\n/swap - поменять исходную и итоговую валюты местами\n/rate USD RUB - показать текущий курс пары\n/with USD EUR RUB - добавить кнопки перевода в валюты\n/with off - отключить кнопки перевода\n/with_modify yes - учитывать modify_from и modify_to для кнопок\n/multi 1000 - умножать входную сумму перед расчетом\n/round auto - округление результата: auto, 0, 2, 4 или 6\n/modify_from 1.5 - изменить входную сумму на процент перед расчетом\n/modify_to 1.5 - изменить результат на процент после расчета\n/reset - сбросить настройки к значениям по умолчанию\n/delete - удалить сохраненные настройки пользователя\n/settings - текущие настройки\n/list - список поддерживаемых валют\n/help - эта справка\n\nМожно писать сразу: 100 usd to rub, 100$ в руб или просто 12 345,67. Несколько сумм с новой строки я сложу и переведу итог.\n\nInline mode: в любом чате пишите @имя_бота 100 usd rub.", s.From, s.To)
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
	snapshot := copySessions(b.sessions)
	b.mu.Unlock()

	if err := b.writeSessions(snapshot); err != nil {
		b.log.Error("save user settings failed", "path", b.cfg.UserSettingsFile, "error", err)
	}
}

func (b *Bot) deleteSession(userID int64) {
	b.mu.Lock()
	delete(b.sessions, userID)
	snapshot := copySessions(b.sessions)
	b.mu.Unlock()

	if err := b.writeSessions(snapshot); err != nil {
		b.log.Error("delete user settings failed", "path", b.cfg.UserSettingsFile, "error", err)
	}
}

func (b *Bot) getUpdates(ctx context.Context, offset int64) ([]update, error) {
	var result getUpdatesResponse
	err := b.post(ctx, "getUpdates", map[string]any{
		"offset":          offset,
		"timeout":         60,
		"allowed_updates": []string{"message", "callback_query", "inline_query"},
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
	return b.sendMessageWithMarkup(ctx, chatID, text, nil)
}

func (b *Bot) sendHTMLMessage(ctx context.Context, chatID int64, text string) error {
	return b.sendHTMLMessageWithMarkup(ctx, chatID, text, nil)
}

func (b *Bot) sendMessageWithMarkup(ctx context.Context, chatID int64, text string, markup *inlineKeyboardMarkup) error {
	return b.sendMessageWithMarkupAndParseMode(ctx, chatID, text, markup, "")
}

func (b *Bot) sendHTMLMessageWithMarkup(ctx context.Context, chatID int64, text string, markup *inlineKeyboardMarkup) error {
	return b.sendMessageWithMarkupAndParseMode(ctx, chatID, text, markup, "HTML")
}

func (b *Bot) sendMessageWithMarkupAndParseMode(ctx context.Context, chatID int64, text string, markup *inlineKeyboardMarkup, parseMode string) error {
	var result apiResponse
	payload := map[string]any{
		"chat_id": chatID,
		"text":    text,
	}
	if markup != nil {
		payload["reply_markup"] = markup
	}
	if parseMode != "" {
		payload["parse_mode"] = parseMode
	}
	err := b.post(ctx, "sendMessage", payload, &result)
	if err != nil {
		return err
	}
	if !result.OK {
		return fmt.Errorf("telegram sendMessage failed: %s", result.Description)
	}
	return nil
}

func (b *Bot) answerCallbackQuery(ctx context.Context, callbackQueryID, text string) error {
	var result apiResponse
	payload := map[string]any{
		"callback_query_id": callbackQueryID,
	}
	if text != "" {
		payload["text"] = text
	}
	err := b.post(ctx, "answerCallbackQuery", payload, &result)
	if err != nil {
		return err
	}
	if !result.OK {
		return fmt.Errorf("telegram answerCallbackQuery failed: %s", result.Description)
	}
	return nil
}

func (b *Bot) answerInlineQuery(ctx context.Context, inlineQueryID string, results []inlineQueryResultArticle) error {
	if results == nil {
		results = []inlineQueryResultArticle{}
	}
	var result apiResponse
	err := b.post(ctx, "answerInlineQuery", map[string]any{
		"inline_query_id": inlineQueryID,
		"results":         results,
		"cache_time":      0,
		"is_personal":     true,
	}, &result)
	if err != nil {
		return err
	}
	if !result.OK {
		return fmt.Errorf("telegram answerInlineQuery failed: %s", result.Description)
	}
	return nil
}

func (b *Bot) setBotCommands(ctx context.Context) error {
	var result apiResponse
	err := b.post(ctx, "setMyCommands", map[string]any{
		"commands": botCommands(),
	}, &result)
	if err != nil {
		return err
	}
	if !result.OK {
		return fmt.Errorf("telegram setMyCommands failed: %s", result.Description)
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

func commandArgs(text string) string {
	fields := strings.Fields(text)
	if len(fields) < 2 {
		return ""
	}
	return strings.Join(fields[1:], " ")
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
			return nil, errors.New("Такой валюты нет в списке бота. Посмотрите доступные варианты через /list.")
		}
		if _, ok := seen[code]; ok {
			continue
		}
		seen[code] = struct{}{}
		codes = append(codes, code)
	}
	if len(codes) == 0 {
		return nil, errors.New("Укажите хотя бы одну валюту: /with USD EUR RUB.")
	}
	return codes, nil
}

type conversionInput struct {
	Amount      float64
	AmountCount int
	From        string
	To          string
}

func parseConversionInput(text string, s session) (conversionInput, error) {
	amount, amountCount, err := convert.ParseAmounts(text)
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
	}, nil
}

type rateRequest struct {
	From string
	To   string
}

func parseRateRequest(text string, s session) (rateRequest, error) {
	s = normalizeSession(s, "", "")
	args := commandArgs(text)
	if unknown := firstUnknownCurrencyCodeToken(args); unknown != "" {
		return rateRequest{}, fmt.Errorf("Не знаю валюту %s. Посмотрите доступные варианты через /list.", unknown)
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

func applyPercent(value, percent float64) float64 {
	return value * (1 + percent/100)
}

func rateReply(from, to string, snapshot rates.Snapshot) (string, error) {
	direct, err := rates.Convert(1, from, to, snapshot)
	if err != nil {
		return "", err
	}
	reverse, err := rates.Convert(1, to, from, snapshot)
	if err != nil {
		return "", err
	}

	updatedAt := "нет данных"
	if !snapshot.FetchedAt.IsZero() {
		updatedAt = snapshot.FetchedAt.UTC().Format("2006-01-02 15:04:05 UTC")
	}

	return fmt.Sprintf(
		"Курс:\n1 %s = %s %s%s\n1 %s = %s %s%s\nОбновлено: %s",
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

func formatRate(value float64) string {
	if math.Abs(value) >= 1 {
		return convert.FormatMoney(value)
	}
	return formatSmallDecimal(value, 8)
}

func formatConvertedAmount(value float64) string {
	if value == 0 || math.Abs(value) >= 0.01 {
		return convert.FormatMoney(value)
	}
	return formatSmallDecimal(value, 8)
}

func formatConvertedAmountForMode(value float64, roundMode string) string {
	precision, ok := roundPrecision(roundMode)
	if !ok {
		return formatConvertedAmount(value)
	}
	return formatFixedDecimal(value, precision)
}

func formatFixedDecimal(value float64, precision int) string {
	if precision <= 0 {
		return groupWholeNumber(value)
	}
	formatted := strconv.FormatFloat(value, 'f', precision, 64)
	whole, fraction, ok := strings.Cut(formatted, ".")
	if !ok {
		return groupDigits(whole)
	}
	return groupDigits(whole) + "," + fraction
}

func groupWholeNumber(value float64) string {
	rounded := math.Round(value)
	return groupDigits(strconv.FormatInt(int64(rounded), 10))
}

func groupDigits(raw string) string {
	sign := ""
	if strings.HasPrefix(raw, "-") {
		sign = "-"
		raw = strings.TrimPrefix(raw, "-")
	}
	if len(raw) <= 3 {
		return sign + raw
	}
	var b strings.Builder
	firstGroup := len(raw) % 3
	if firstGroup == 0 {
		firstGroup = 3
	}
	b.WriteString(sign)
	b.WriteString(raw[:firstGroup])
	for i := firstGroup; i < len(raw); i += 3 {
		b.WriteByte(' ')
		b.WriteString(raw[i : i+3])
	}
	return b.String()
}

func formatSmallDecimal(value float64, precision int) string {
	formatted := strconv.FormatFloat(value, 'f', precision, 64)
	formatted = strings.TrimRight(formatted, "0")
	formatted = strings.TrimRight(formatted, ".")
	if formatted == "" || formatted == "-0" {
		formatted = "0"
	}
	return strings.ReplaceAll(formatted, ".", ",")
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

func conversionReply(amount float64, amountCount int, from, to string, multiplier, modifyFromPercent, modifyToPercent float64, useModify bool, roundMode string, snapshot rates.Snapshot) (string, error) {
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
		replyPrefix = fmt.Sprintf("Итого: %s = <b>%s</b>\nСтрок учтено: %d", html.EscapeString(amountText), html.EscapeString(resultText), amountCount)
	}

	lines := []string{
		replyPrefix,
		fmt.Sprintf("Курс: 1 %s = %s %s", html.EscapeString(from), html.EscapeString(formatRate(rawUnitRate)), html.EscapeString(to)),
	}
	if suffix := convenientRateSuffix(from, to, rawUnitRate); suffix != "" {
		lines = append(lines, html.EscapeString(strings.TrimPrefix(suffix, "\n")))
	}
	if multiplier != 1 || (useModify && (modifyFromPercent != 0 || modifyToPercent != 0)) {
		lines = append(lines, fmt.Sprintf("Расчет для 1 введенной единицы = %s %s", html.EscapeString(formatConvertedAmountForMode(unitRate, roundMode)), html.EscapeString(to)))
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

func normalizeSession(s session, defaultFrom, defaultTo string) session {
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

func hasInputSettings(s session) bool {
	s = normalizeSession(s, "", "")
	return s.Multiplier != 1 || s.ModifyFromPercent != 0 || s.ModifyToPercent != 0
}

func formatNumber(value float64) string {
	formatted := convert.FormatMoney(value)
	formatted = strings.TrimRight(formatted, "0")
	return strings.TrimRight(formatted, ",")
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
	s = normalizeSession(s, "", "")
	updatedAt := "нет данных"
	if !snapshot.FetchedAt.IsZero() {
		updatedAt = snapshot.FetchedAt.UTC().Format("2006-01-02 15:04:05 UTC")
	}
	with := "выключена"
	if len(s.With) > 0 {
		with = strings.Join(s.With, ", ")
	}

	return fmt.Sprintf(
		"Настройки:\nПара: %s -> %s\nКурсы обновлены: %s\nКнопки перевода: %s\nМодификаторы для кнопок: %s\nМножитель входной суммы: %s\nОкругление результата: %s\nМодификатор входной суммы: %s\nМодификатор результата: %s\n\nДругие настройки будут добавлены сюда позже.",
		s.From,
		s.To,
		updatedAt,
		with,
		formatYesNo(s.WithModify),
		formatNumber(s.Multiplier),
		formatRoundMode(s.Round),
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

func withReplyMarkup(amount float64, s session) *inlineKeyboardMarkup {
	buttons := make([]inlineKeyboardButton, 0, len(s.With))
	for _, to := range s.With {
		data, ok := withCallbackData(amount, s, to)
		if !ok {
			continue
		}
		buttons = append(buttons, inlineKeyboardButton{Text: "в " + to, CallbackData: data})
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

func inlineConversionResult(reply string) inlineQueryResultArticle {
	plain := stripTelegramHTML(reply)
	title := firstLine(plain)
	if title == "" {
		title = "Конвертация"
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
		request.ModifyFromPercent, err = parseCallbackFloat(parts[6])
		if err != nil {
			return withCallbackRequest{}, err
		}
		request.ModifyToPercent, err = parseCallbackFloat(parts[7])
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
	if strings.TrimSpace(b.cfg.UserSettingsFile) == "" {
		return nil
	}
	raw, err := os.ReadFile(b.cfg.UserSettingsFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}

	var sessions map[int64]session
	if err := json.Unmarshal(raw, &sessions); err != nil {
		return err
	}

	b.mu.Lock()
	for userID, s := range sessions {
		b.sessions[userID] = normalizeSession(s, b.cfg.DefaultFrom, b.cfg.DefaultTo)
	}
	b.mu.Unlock()
	return nil
}

func (b *Bot) writeSessions(sessions map[int64]session) error {
	if strings.TrimSpace(b.cfg.UserSettingsFile) == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(b.cfg.UserSettingsFile), 0o755); err != nil {
		return err
	}

	raw, err := json.MarshalIndent(sessions, "", "  ")
	if err != nil {
		return err
	}

	tmpFile := b.cfg.UserSettingsFile + ".tmp"
	if err := os.WriteFile(tmpFile, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpFile, b.cfg.UserSettingsFile)
}

func copySessions(sessions map[int64]session) map[int64]session {
	result := make(map[int64]session, len(sessions))
	for userID, s := range sessions {
		result[userID] = s
	}
	return result
}

func botCommands() []botCommand {
	return []botCommand{
		{Command: "start", Description: "запустить бота"},
		{Command: "help", Description: "справка по командам"},
		{Command: "settings", Description: "текущие настройки"},
		{Command: "from", Description: "выбрать исходную валюту"},
		{Command: "to", Description: "выбрать валюту результата"},
		{Command: "swap", Description: "поменять валюты местами"},
		{Command: "rate", Description: "текущий курс пары"},
		{Command: "with", Description: "кнопки перевода в валюты"},
		{Command: "with_modify", Description: "модификаторы для кнопок"},
		{Command: "multi", Description: "множитель входной суммы"},
		{Command: "round", Description: "округление результата"},
		{Command: "modify_from", Description: "процент к входной сумме"},
		{Command: "modify_to", Description: "процент к результату"},
		{Command: "reset", Description: "сбросить настройки"},
		{Command: "delete", Description: "удалить настройки"},
		{Command: "list", Description: "список валют"},
	}
}

type botCommand struct {
	Command     string `json:"command"`
	Description string `json:"description"`
}

type inlineKeyboardMarkup struct {
	InlineKeyboard [][]inlineKeyboardButton `json:"inline_keyboard"`
}

type inlineKeyboardButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data"`
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
	UpdateID      int64          `json:"update_id"`
	Message       *message       `json:"message"`
	CallbackQuery *callbackQuery `json:"callback_query"`
	InlineQuery   *inlineQuery   `json:"inline_query"`
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

type callbackQuery struct {
	ID      string   `json:"id"`
	From    *user    `json:"from"`
	Message *message `json:"message"`
	Data    string   `json:"data"`
}

type inlineQuery struct {
	ID    string `json:"id"`
	From  *user  `json:"from"`
	Query string `json:"query"`
}

type inlineQueryResultArticle struct {
	Type                string                  `json:"type"`
	ID                  string                  `json:"id"`
	Title               string                  `json:"title"`
	Description         string                  `json:"description,omitempty"`
	InputMessageContent inputTextMessageContent `json:"input_message_content"`
}

type inputTextMessageContent struct {
	MessageText string `json:"message_text"`
	ParseMode   string `json:"parse_mode,omitempty"`
}
