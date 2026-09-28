package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

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
	for _, language := range []string{"", languageRussian, languageEnglish} {
		var result apiResponse
		payload := map[string]any{"commands": botCommandsForLanguage(language)}
		if language != "" {
			payload["language_code"] = language
		}
		if err := b.post(ctx, "setMyCommands", payload, &result); err != nil {
			return err
		}
		if !result.OK {
			return fmt.Errorf("telegram setMyCommands failed: %s", result.Description)
		}
	}
	return nil
}

func (b *Bot) post(ctx context.Context, method string, payload any, target any) (err error) {
	// The token is part of the URL, and net/http errors quote the URL verbatim.
	defer func() { err = redactSecret(err, b.cfg.TelegramToken) }()

	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	endpoint := fmt.Sprintf("%s/bot%s/%s", b.cfg.TelegramAPI, b.cfg.TelegramToken, method)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
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

func botCommands() []botCommand {
	return botCommandsForLanguage(languageRussian)
}

func botCommandsForLanguage(language string) []botCommand {
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
			{Command: "subscribe", Description: "daily rate"},
			{Command: "subscription", Description: "current subscription"},
			{Command: "unsubscribe", Description: "disable subscription"},
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
		{Command: "subscribe", Description: "ежедневный курс"},
		{Command: "subscription", Description: "текущая подписка"},
		{Command: "unsubscribe", Description: "отключить подписку"},
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
	ID           int64  `json:"id"`
	LanguageCode string `json:"language_code"`
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
