package telegram

import (
	"context"
	"currency-converter-bot/internal/rates"
	"errors"
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

// inlineLanguage prefers the language saved by the user and otherwise uses the
// Telegram client language ("en-US" -> en), defaulting to Russian.
func inlineLanguage(saved, telegramCode string) string {
	if language := normalizeLanguage(saved); language != "" {
		return language
	}
	primary, _, _ := strings.Cut(telegramCode, "-")
	if language := normalizeLanguage(primary); language != "" {
		return language
	}
	return languageRussian
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

// userError is an input error with a message for each interface language.
type userError struct {
	ru, en string
}

func (e userError) Error() string { return e.en }

// errorText returns the message to show for err in language.
func errorText(err error, language string) string {
	var localized userError
	if errors.As(err, &localized) {
		return tr(language, localized.ru, localized.en)
	}
	var unknown *rates.UnknownCurrencyError
	if errors.As(err, &unknown) {
		return fmt.Sprintf(tr(language, "Нет курса ЦБ РФ для %s", "No Bank of Russia rate for %s"), unknown.Code)
	}
	return err.Error()
}

func unknownCurrencyCodeError(code string) error {
	return userError{
		ru: fmt.Sprintf("Не знаю валюту %s. Посмотрите доступные варианты через /list.", code),
		en: fmt.Sprintf("Unknown currency %s. See the available currencies with /list.", code),
	}
}
