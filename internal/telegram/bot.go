package telegram

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"currency-converter-bot/internal/config"
	"currency-converter-bot/internal/rates"
)

type Bot struct {
	cfg                  config.Config
	rates                *rates.Provider
	client               *http.Client
	log                  *slog.Logger
	mu                   sync.RWMutex
	sessions             map[int64]session
	accessMu             sync.RWMutex
	allowedUsers         map[int64]struct{}
	subMu                sync.RWMutex
	subscriptions        map[int64]dailySubscription
	subscriptionLocation *time.Location
}

func New(cfg config.Config, provider *rates.Provider, logger *slog.Logger) *Bot {
	subscriptionLocation, err := loadSubscriptionLocation(cfg.SubscriptionTimezone)
	if err != nil {
		logger.Warn("load subscription timezone failed", "timezone", cfg.SubscriptionTimezone, "error", err)
	}
	b := &Bot{
		cfg:                  cfg,
		rates:                provider,
		client:               &http.Client{Timeout: 70 * time.Second},
		log:                  logger,
		sessions:             map[int64]session{},
		allowedUsers:         map[int64]struct{}{},
		subscriptions:        map[int64]dailySubscription{},
		subscriptionLocation: subscriptionLocation,
	}
	if err := b.loadSessions(); err != nil {
		b.log.Warn("load user settings failed", "error", err)
	}
	if err := b.loadAllowedUsers(); err != nil {
		b.log.Warn("load allowed users failed", "error", err)
	}
	if err := b.loadSubscriptions(); err != nil {
		b.log.Warn("load subscriptions failed", "error", err)
	}
	return b
}

func (b *Bot) Run(ctx context.Context) error {
	if err := b.setBotCommands(ctx); err != nil && !errors.Is(err, context.Canceled) {
		b.log.Warn("set bot commands failed", "error", err)
	}

	go b.runSubscriptionScheduler(ctx)

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
	text := strings.TrimSpace(update.Message.Text)
	if !b.isAllowed(userID) {
		b.log.Warn("blocked user", "user_id", userID, "chat_id", chatID)
		b.showBlockedUserMessage(ctx, chatID, userID, text)
		return
	}

	if isCommand(text, "/lang") {
		b.setLanguage(ctx, chatID, userID, text)
		return
	}
	if b.getSession(userID).Language == "" {
		b.showLanguageSelector(ctx, chatID)
		return
	}
	lang := b.userLanguage(userID)

	if text == "" {
		_ = b.sendMessage(ctx, chatID, tr(lang, "Пришлите сумму числом или используйте /from USD и /to RUB.", "Send an amount as a number or use /from USD and /to RUB."))
		return
	}

	switch {
	case isCommand(text, "/start"), isCommand(text, "/help"):
		_ = b.sendMessage(ctx, chatID, b.helpText(userID))
	case isCommand(text, "/whoami"):
		_ = b.sendMessage(ctx, chatID, b.whoamiText(userID))
	case isCommand(text, "/allow"):
		b.allowUsers(ctx, chatID, userID, text)
	case isCommand(text, "/disallow"):
		b.disallowUsers(ctx, chatID, userID, text)
	case isCommand(text, "/allowed"):
		b.showAllowedUsers(ctx, chatID, userID)
	case isCommand(text, "/list"):
		_ = b.sendMessage(ctx, chatID, supportedCurrenciesTextForLanguage(lang))
	case isCommand(text, "/settings"):
		b.showSettings(ctx, chatID, userID)
	case isCommand(text, "/rate"):
		b.showRate(ctx, chatID, userID, text)
	case isCommand(text, "/subscribe"):
		b.setSubscription(ctx, chatID, userID, text)
	case isCommand(text, "/subscription"):
		b.showSubscription(ctx, chatID, userID)
	case isCommand(text, "/unsubscribe"):
		b.deleteSubscription(ctx, chatID, userID)
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
	case isCommand(text, "/inline_modify"):
		b.setInlineModify(ctx, chatID, userID, text)
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
	if !b.isAllowed(userID) {
		b.log.Warn("blocked user callback", "user_id", userID)
		_ = b.answerCallbackQuery(ctx, query.ID, "Нет доступа")
		return
	}
	if strings.HasPrefix(query.Data, "lang:") {
		language := normalizeLanguage(strings.TrimPrefix(query.Data, "lang:"))
		if language == "" {
			_ = b.answerCallbackQuery(ctx, query.ID, "Invalid language")
			return
		}
		b.saveLanguage(userID, language)
		_ = b.answerCallbackQuery(ctx, query.ID, tr(language, "Язык выбран", "Language selected"))
		if query.Message != nil {
			_ = b.sendMessage(ctx, query.Message.Chat.ID, tr(language, "🇷🇺 Язык изменен на русский. Отправьте /help, чтобы увидеть команды.", "🇬🇧 Language changed to English. Send /help to see the commands."))
		}
		return
	}
	language := b.userLanguage(userID)
	if query.Message == nil {
		_ = b.answerCallbackQuery(ctx, query.ID, tr(language, "Сообщение недоступно", "Message unavailable"))
		return
	}

	request, err := parseWithCallbackData(query.Data)
	if err != nil {
		_ = b.answerCallbackQuery(ctx, query.ID, tr(language, "Кнопка устарела", "Button expired"))
		return
	}

	snapshot, err := b.rates.Get(ctx)
	if err != nil {
		b.log.Error("rates unavailable", "error", err)
		_ = b.answerCallbackQuery(ctx, query.ID, tr(language, "Курсы недоступны", "Rates unavailable"))
		return
	}

	s := b.getSession(userID)
	reply, err := conversionReplyForLanguage(request.Amount, 1, request.From, request.To, request.Multiplier, request.ModifyFromPercent, request.ModifyToPercent, request.UseModify, s.Round, snapshot, language)
	if err != nil {
		_ = b.answerCallbackQuery(ctx, query.ID, tr(language, "Не удалось перевести", "Conversion failed"))
		_ = b.sendMessage(ctx, query.Message.Chat.ID, fmt.Sprintf("%s. %s", err.Error(), tr(language, "Проверьте настройки.", "Check your settings.")))
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
	if !b.isAllowed(userID) {
		b.log.Warn("blocked user inline query", "user_id", userID)
		_ = b.answerInlineQuery(ctx, query.ID, nil)
		return
	}
	s := b.getSession(userID)
	// Inline users may never have opened a private chat with the bot, so fall
	// back to the Telegram client language instead of requiring /lang first.
	language := inlineLanguage(s.Language, query.From.LanguageCode)
	s.Language = language

	text := strings.TrimSpace(query.Query)
	if text == "" {
		_ = b.answerInlineQuery(ctx, query.ID, nil)
		return
	}

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

	settings := conversionSettingsForInput(s, request)
	reply, err := conversionReplyForLanguage(request.Amount, request.AmountCount, request.From, request.To, settings.Multiplier, settings.ModifyFromPercent, settings.ModifyToPercent, settings.UseModify, s.Round, snapshot, language)
	if err != nil {
		_ = b.answerInlineQuery(ctx, query.ID, nil)
		return
	}

	_ = b.answerInlineQuery(ctx, query.ID, []inlineQueryResultArticle{
		inlineConversionResultForLanguage(reply, language),
	})
}

func (b *Bot) helpText(userID int64) string {
	s := b.getSession(userID)
	language := b.userLanguage(userID)
	russian := "Я конвертирую валюты по официальным курсам ЦБ РФ.\n\nЯзык: %s\nТекущая пара: %s -> %s\n\nКоманды:\n/lang en — сменить язык (en/ru)\n/from USD — выбрать исходную валюту\n/to RUB — выбрать валюту результата\n/swap — поменять валюты местами\n/rate USD RUB — показать текущий курс пары\n/subscribe 09:00 [USD RUB] — ежедневный курс\n/subscription — показать подписку\n/unsubscribe — отключить подписку\n/with USD EUR RUB — добавить кнопки перевода\n/with off — отключить кнопки\n/with_modify yes — применять модификаторы для кнопок\n/inline_modify yes — применять модификаторы для явных валют\n/multi 1000 — множитель входной суммы\n/round auto — округление: auto, 0, 2, 4 или 6\n/modify_from 1.5 — процент к входной сумме\n/modify_to 1.5 — процент к результату\n/reset — сбросить настройки (язык сохранится)\n/delete — удалить мои данные (настройки, язык, подписку)\n/settings — текущие настройки\n/whoami — показать Telegram ID\n/list — список валют\n/help — эта справка"
	english := "I convert currencies using the official exchange rates of the Bank of Russia.\n\nLanguage: %s\nCurrent pair: %s -> %s\n\nCommands:\n/lang ru — change language (en/ru)\n/from USD — select the source currency\n/to RUB — select the target currency\n/swap — swap the currencies\n/rate USD RUB — show the current pair rate\n/subscribe 09:00 [USD RUB] — daily rate subscription\n/subscription — show the subscription\n/unsubscribe — disable the subscription\n/with USD EUR RUB — add conversion buttons\n/with off — disable the buttons\n/with_modify yes — apply modifiers to buttons\n/inline_modify yes — apply modifiers to explicit currencies\n/multi 1000 — multiply the input amount\n/round auto — rounding: auto, 0, 2, 4, or 6\n/modify_from 1.5 — adjust the input amount by a percentage\n/modify_to 1.5 — adjust the result by a percentage\n/reset — reset settings (language is preserved)\n/delete — delete my data (settings, language, subscription)\n/settings — show current settings\n/whoami — show your Telegram ID\n/list — list supported currencies\n/help — show this help"
	text := fmt.Sprintf(tr(language, russian, english), languageName(language), s.From, s.To)
	if b.cfg.IsAdmin(userID) {
		text += tr(language, "\n\nАдмин-команды:\n/allow 123456789 — разрешить пользователя по Telegram ID\n/disallow 123456789 — убрать пользователя из runtime whitelist\n/allowed — показать список доступа", "\n\nAdmin commands:\n/allow 123456789 — allow a Telegram user ID\n/disallow 123456789 — remove a user from the runtime whitelist\n/allowed — show the access list")
	}
	text += tr(language, "\n\nМожно писать сразу: 100 usd to rub, 100$ в руб или просто 12 345,67. Для покупок поддерживаются 100х9, 100 x 9 и 100 * 9. Суммы на разных строках будут сложены.\n\nInline mode: @имя_бота 100 usd rub.", "\n\nYou can enter 100 usd to rub, 100$ in rub, or simply 12,345.67. Purchases support 100x9 and 100 * 9. Amounts on separate lines are added together.\n\nInline mode: @bot_name 100 usd rub.")
	return text
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
