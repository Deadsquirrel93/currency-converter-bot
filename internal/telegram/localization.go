package telegram

import (
	"context"
	"fmt"
	"strings"
)

const (
	languageRussian = "ru"
	languageEnglish = "en"
)

func normalizeLanguage(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case languageEnglish:
		return languageEnglish
	case languageRussian:
		return languageRussian
	default:
		return ""
	}
}

func tr(language, russian, english string) string {
	if normalizeLanguage(language) == languageEnglish {
		return english
	}
	return russian
}

func (b *Bot) userLanguage(userID int64) string {
	if language := normalizeLanguage(b.getSession(userID).Language); language != "" {
		return language
	}
	return languageRussian
}

func languageKeyboard() *inlineKeyboardMarkup {
	return &inlineKeyboardMarkup{InlineKeyboard: [][]inlineKeyboardButton{{
		{Text: "🇷🇺 Русский", CallbackData: "lang:ru"},
		{Text: "🇬🇧 English", CallbackData: "lang:en"},
	}}}
}

func (b *Bot) showLanguageSelector(ctx context.Context, chatID int64) {
	_ = b.sendMessageWithMarkup(ctx, chatID, "Выберите язык / Choose your language:", languageKeyboard())
}

func (b *Bot) setLanguage(ctx context.Context, chatID, userID int64, text string) {
	language := normalizeLanguage(commandArgs(text))
	if language == "" {
		b.showLanguageSelector(ctx, chatID)
		return
	}
	b.saveLanguage(userID, language)
	_ = b.sendMessage(ctx, chatID, tr(language, "🇷🇺 Язык изменен на русский.", "🇬🇧 Language changed to English."))
}

func (b *Bot) saveLanguage(userID int64, language string) {
	s := b.getSession(userID)
	s.Language = normalizeLanguage(language)
	b.setSession(userID, s)
}

func languageName(language string) string {
	if normalizeLanguage(language) == languageEnglish {
		return "🇬🇧 English"
	}
	return "🇷🇺 Русский"
}

func invalidCurrencyText(language string) string {
	return tr(language,
		"Такой валюты нет в списке бота. Посмотрите доступные варианты через /list.",
		"That currency is not supported. See the available currencies with /list.",
	)
}

func ratesUnavailableText(language string) string {
	return tr(language,
		"Не удалось получить курсы валют. Попробуйте чуть позже.",
		"Could not retrieve exchange rates. Please try again later.",
	)
}

func checkCurrenciesText(language, message string) string {
	return fmt.Sprintf("%s. %s", message, tr(language, "Проверьте валюты.", "Check the currencies."))
}
