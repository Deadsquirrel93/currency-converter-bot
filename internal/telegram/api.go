package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"slices"
	"strconv"
	"strings"
	"time"
)

func (b *Bot) getUpdates(ctx context.Context, offset int64) ([]update, error) {
	var updates []update
	err := b.post(ctx, "getUpdates", map[string]any{
		"offset":          offset,
		"timeout":         60,
		"allowed_updates": []string{"message", "callback_query", "inline_query"},
	}, &updates)
	return updates, err
}

func (b *Bot) sendMessage(ctx context.Context, chatID int64, text string) error {
	return b.sendMessageWithMarkup(ctx, chatID, text, nil, "")
}

// sendMessageWithMarkup sends text with optional buttons; parseMode is "" for
// plain text or "HTML".
func (b *Bot) sendMessageWithMarkup(ctx context.Context, chatID int64, text string, markup *inlineKeyboardMarkup, parseMode string) error {
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
	return b.post(ctx, "sendMessage", payload, nil)
}

// sendPhoto uploads a PNG; a file can only be sent as multipart/form-data.
func (b *Bot) sendPhoto(ctx context.Context, chatID int64, photo []byte, caption string) error {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	// Writes to a bytes.Buffer cannot fail.
	_ = writer.WriteField("chat_id", strconv.FormatInt(chatID, 10))
	_ = writer.WriteField("caption", caption)
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", `form-data; name="photo"; filename="chart.png"`)
	header.Set("Content-Type", "image/png")
	part, _ := writer.CreatePart(header)
	_, _ = part.Write(photo)
	_ = writer.Close()
	return b.postRaw(ctx, "sendPhoto", body.Bytes(), writer.FormDataContentType(), nil)
}

// sendChatAction shows "sending photo..." while a slow reply is prepared.
func (b *Bot) sendChatAction(ctx context.Context, chatID int64, action string) error {
	return b.post(ctx, "sendChatAction", map[string]any{"chat_id": chatID, "action": action}, nil)
}

func (b *Bot) answerCallbackQuery(ctx context.Context, callbackQueryID, text string) error {
	payload := map[string]any{
		"callback_query_id": callbackQueryID,
	}
	if text != "" {
		payload["text"] = text
	}
	return b.post(ctx, "answerCallbackQuery", payload, nil)
}

func (b *Bot) answerInlineQuery(ctx context.Context, inlineQueryID string, results []inlineQueryResultArticle) error {
	if results == nil {
		results = []inlineQueryResultArticle{}
	}
	return b.post(ctx, "answerInlineQuery", map[string]any{
		"inline_query_id": inlineQueryID,
		"results":         results,
		"cache_time":      0,
		"is_personal":     true,
	}, nil)
}

// setBotCommands shows regular users only the commands they can use; admins
// get the full list through a per-chat scope (their private chat ID equals
// their user ID).
func (b *Bot) setBotCommands(ctx context.Context) error {
	for _, language := range []string{"", languageRussian, languageEnglish} {
		if err := b.setMyCommands(ctx, userBotCommands(language), language, nil); err != nil {
			return err
		}
	}
	for _, adminID := range slices.Sorted(maps.Keys(b.cfg.AdminUsers)) {
		scope := map[string]any{"type": "chat", "chat_id": adminID}
		for _, language := range []string{"", languageRussian, languageEnglish} {
			if err := b.setMyCommands(ctx, botCommands(language), language, scope); err != nil {
				// Fails with "chat not found" until the admin has started the bot.
				b.log.Warn("set admin commands failed", "admin_id", adminID, "error", err)
				break
			}
		}
	}
	return nil
}

func (b *Bot) setMyCommands(ctx context.Context, commands []botCommand, language string, scope map[string]any) error {
	payload := map[string]any{"commands": commands}
	if language != "" {
		payload["language_code"] = language
	}
	if scope != nil {
		payload["scope"] = scope
	}
	return b.post(ctx, "setMyCommands", payload, nil)
}

// adminCommands are hidden from the command menu of regular users.
var adminCommands = map[string]bool{"allow": true, "disallow": true, "allowed": true}

func userBotCommands(language string) []botCommand {
	all := botCommands(language)
	commands := make([]botCommand, 0, len(all))
	for _, command := range all {
		if !adminCommands[command.Command] {
			commands = append(commands, command)
		}
	}
	return commands
}

func (b *Bot) getMe(ctx context.Context) (user, error) {
	var me user
	err := b.post(ctx, "getMe", map[string]any{}, &me)
	return me, err
}

// maxRetryAfter caps how long a single call waits after a 429 before its one
// retry; longer flood-control pauses are returned to the caller as errors.
const maxRetryAfter = 30 * time.Second

// apiError is a Bot API call rejected by Telegram. Description comes from
// Telegram and does not contain the token.
type apiError struct {
	Method      string
	StatusCode  int
	Description string
	RetryAfter  time.Duration
}

func (e *apiError) Error() string {
	return fmt.Sprintf("telegram %s failed: %d %s", e.Method, e.StatusCode, e.Description)
}

// isChatUnreachable reports errors that will not go away by retrying: the user
// blocked the bot, the bot was removed from the group, or the chat is gone.
func isChatUnreachable(err error) bool {
	var apiErr *apiError
	if !errors.As(err, &apiErr) {
		return false
	}
	if apiErr.StatusCode == http.StatusForbidden {
		return true
	}
	return apiErr.StatusCode == http.StatusBadRequest && strings.Contains(strings.ToLower(apiErr.Description), "chat not found")
}

// post sends one Bot API call and decodes its result into target, if any.
func (b *Bot) post(ctx context.Context, method string, payload any, target any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return b.postRaw(ctx, method, raw, "application/json", target)
}

// postRaw sends one Bot API request, retrying once after a short 429. Every
// request goes through here, so the token never leaks through an error.
func (b *Bot) postRaw(ctx context.Context, method string, raw []byte, contentType string, target any) (err error) {
	// The token is part of the URL, and net/http errors quote the URL verbatim.
	defer func() { err = redactSecret(err, b.cfg.TelegramToken) }()

	err = b.postOnce(ctx, method, raw, contentType, target)
	var apiErr *apiError
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusTooManyRequests && apiErr.RetryAfter > 0 && apiErr.RetryAfter <= maxRetryAfter {
		sleep(ctx, apiErr.RetryAfter)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		err = b.postOnce(ctx, method, raw, contentType, target)
	}
	return err
}

func (b *Bot) postOnce(ctx context.Context, method string, raw []byte, contentType string, target any) error {
	endpoint := fmt.Sprintf("%s/bot%s/%s", b.cfg.TelegramAPI, b.cfg.TelegramToken, method)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", contentType)

	resp, err := b.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var failure struct {
			Description string `json:"description"`
			Parameters  struct {
				RetryAfter int `json:"retry_after"`
			} `json:"parameters"`
		}
		// Reading the (small) body also lets the connection be reused.
		_ = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&failure)
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		if failure.Description == "" {
			failure.Description = http.StatusText(resp.StatusCode)
		}
		return &apiError{
			Method:      method,
			StatusCode:  resp.StatusCode,
			Description: failure.Description,
			RetryAfter:  time.Duration(failure.Parameters.RetryAfter) * time.Second,
		}
	}
	var envelope struct {
		OK          bool            `json:"ok"`
		Description string          `json:"description"`
		Result      json.RawMessage `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return err
	}
	if !envelope.OK {
		return fmt.Errorf("telegram %s failed: %s", method, envelope.Description)
	}
	if target == nil || len(envelope.Result) == 0 {
		return nil
	}
	return json.Unmarshal(envelope.Result, target)
}

func botCommands(language string) []botCommand {
	if normalizeLanguage(language) == languageEnglish {
		return []botCommand{
			{Command: "start", Description: "start the bot"},
			{Command: "help", Description: "command help"},
			{Command: "lang", Description: "change language 🇬🇧/🇷🇺"},
			{Command: "whoami", Description: "show your Telegram ID"},
			{Command: "allow", Description: "allow a user"},
			{Command: "disallow", Description: "disallow a user"},
			{Command: "allowed", Description: "access list"},
			{Command: "settings", Description: "current settings"},
			{Command: "from", Description: "select source currency"},
			{Command: "to", Description: "select target currency"},
			{Command: "swap", Description: "swap currencies"},
			{Command: "rate", Description: "current pair rate"},
			{Command: "subscribe", Description: "rate subscription"},
			{Command: "subscription", Description: "current subscriptions"},
			{Command: "unsubscribe", Description: "disable subscriptions"},
			{Command: "tz", Description: "time zone"},
			{Command: "alert", Description: "rate alert"},
			{Command: "alerts", Description: "my alerts"},
			{Command: "chart", Description: "30-day chart"},
			{Command: "with", Description: "conversion buttons"},
			{Command: "with_modify", Description: "button modifiers"},
			{Command: "inline_modify", Description: "explicit-currency modifiers"},
			{Command: "multi", Description: "input multiplier"},
			{Command: "round", Description: "result rounding"},
			{Command: "modify_from", Description: "input percentage"},
			{Command: "modify_to", Description: "result percentage"},
			{Command: "reset", Description: "reset settings"},
			{Command: "delete", Description: "delete my data"},
			{Command: "list", Description: "currency list"},
		}
	}
	return []botCommand{
		{Command: "start", Description: "запустить бота"},
		{Command: "help", Description: "справка по командам"},
		{Command: "lang", Description: "сменить язык 🇷🇺/🇬🇧"},
		{Command: "whoami", Description: "показать ваш Telegram ID"},
		{Command: "allow", Description: "разрешить пользователя"},
		{Command: "disallow", Description: "запретить пользователя"},
		{Command: "allowed", Description: "список доступа"},
		{Command: "settings", Description: "текущие настройки"},
		{Command: "from", Description: "выбрать исходную валюту"},
		{Command: "to", Description: "выбрать валюту результата"},
		{Command: "swap", Description: "поменять валюты местами"},
		{Command: "rate", Description: "текущий курс пары"},
		{Command: "subscribe", Description: "подписка на курс"},
		{Command: "subscription", Description: "текущие подписки"},
		{Command: "unsubscribe", Description: "отключить подписки"},
		{Command: "tz", Description: "часовой пояс"},
		{Command: "alert", Description: "алерт по курсу"},
		{Command: "alerts", Description: "мои алерты"},
		{Command: "chart", Description: "график за 30 дней"},
		{Command: "with", Description: "кнопки перевода в валюты"},
		{Command: "with_modify", Description: "модификаторы для кнопок"},
		{Command: "inline_modify", Description: "модификаторы для явных валют"},
		{Command: "multi", Description: "множитель входной суммы"},
		{Command: "round", Description: "округление результата"},
		{Command: "modify_from", Description: "процент к входной сумме"},
		{Command: "modify_to", Description: "процент к результату"},
		{Command: "reset", Description: "сбросить настройки"},
		{Command: "delete", Description: "удалить мои данные"},
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

type update struct {
	UpdateID      int64          `json:"update_id"`
	Message       *message       `json:"message"`
	CallbackQuery *callbackQuery `json:"callback_query"`
	InlineQuery   *inlineQuery   `json:"inline_query"`
}

type message struct {
	MessageID      int64    `json:"message_id"`
	From           *user    `json:"from"`
	Chat           chat     `json:"chat"`
	Text           string   `json:"text"`
	ReplyToMessage *message `json:"reply_to_message"`
}

type user struct {
	ID           int64  `json:"id"`
	Username     string `json:"username"`
	LanguageCode string `json:"language_code"`
}

type chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
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
