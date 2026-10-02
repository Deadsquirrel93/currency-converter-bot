package telegram

import (
	"bytes"
	"fmt"
	"image/png"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"currency-converter-bot/internal/chart"
	"currency-converter-bot/internal/config"
	"currency-converter-bot/internal/rates"
)

func lastPhoto(t *testing.T, fake *fakeTelegram) apiCall {
	t.Helper()
	photos := fake.methodCalls("sendPhoto")
	if len(photos) == 0 {
		t.Fatal("no photo sent")
	}
	return photos[len(photos)-1]
}

func TestChartCommand(t *testing.T) {
	_, cbrURL := newFakeCBR(t, "29.09.2026", "81,5000")
	fake, url := newFakeTelegram(t, nil)
	bot := newNewRateBot(t, cbrURL, url, "", config.Config{})
	bot.botUsername = "rates_bot"
	bot.saveLanguage(1, languageRussian)

	bot.handleUpdate(t.Context(), privateMessage(1, "/chart"))
	if actions := fake.methodCalls("sendChatAction"); len(actions) != 1 || actions[0].Payload["action"] != "upload_photo" {
		t.Fatalf("chat actions = %+v, want upload_photo", actions)
	}
	photo := lastPhoto(t, fake)
	if !strings.HasPrefix(photo.ContentType, "multipart/form-data") || photo.Payload["photo_content_type"] != "image/png" || photo.Payload["chat_id"] != "1" {
		t.Fatalf("sendPhoto request = %s %+v", photo.ContentType, photo.Payload)
	}
	caption := fmt.Sprint(photo.Payload["caption"])
	for _, want := range []string{"Курс USD -> RUB по данным ЦБ", "Последний: 1 USD = 81,50 RUB", "За период: +1,50 RUB (+1,88%)", "Минимум: 80,00"} {
		if !strings.Contains(caption, want) {
			t.Fatalf("caption = %q, want it to contain %q", caption, want)
		}
	}
	img, err := png.Decode(bytes.NewReader(photo.File))
	if err != nil {
		t.Fatalf("photo is not a PNG: %v", err)
	}
	if b := img.Bounds(); b.Dx() != chart.Width || b.Dy() != chart.Height {
		t.Fatalf("photo size = %v", b)
	}

	bot.handleUpdate(t.Context(), privateMessage(1, "/chart EUR USD"))
	if caption := fmt.Sprint(lastPhoto(t, fake).Payload["caption"]); !strings.Contains(caption, "Курс EUR -> USD") {
		t.Fatalf("EUR USD caption = %q", caption)
	}
	bot.handleUpdate(t.Context(), groupMessage(1, "/chart@rates_bot USD RUB"))
	if chatID := lastPhoto(t, fake).Payload["chat_id"]; chatID != "-100" {
		t.Fatalf("group chart sent to %v, want -100", chatID)
	}
	photos := len(fake.methodCalls("sendPhoto"))
	bot.handleUpdate(t.Context(), privateMessage(1, "/chart XYZ RUB"))
	if got := lastReply(t, fake); !strings.Contains(got, "Не знаю валюту XYZ") || len(fake.methodCalls("sendPhoto")) != photos {
		t.Fatalf("unknown currency reply = %q", got)
	}
	bot.handleUpdate(t.Context(), privateMessage(1, "/help"))
	if got := lastReply(t, fake); !strings.Contains(got, "/chart USD RUB") {
		t.Fatal("/help must mention /chart")
	}
}

func TestRateHasChartButton(t *testing.T) {
	_, cbrURL := newFakeCBR(t, "29.09.2026", "81,5000")
	fake, url := newFakeTelegram(t, nil)
	bot := newNewRateBot(t, cbrURL, url, "", config.Config{})
	bot.saveLanguage(1, languageEnglish)

	bot.handleUpdate(t.Context(), privateMessage(1, "/rate EUR RUB"))
	messages := fake.methodCalls("sendMessage")
	markup := fmt.Sprint(messages[len(messages)-1].Payload["reply_markup"])
	if !strings.Contains(markup, "chart:EUR:RUB") || !strings.Contains(markup, "30-day chart") {
		t.Fatalf("/rate markup = %s, want the chart button", markup)
	}

	press := func(data string) {
		bot.handleUpdate(t.Context(), update{CallbackQuery: &callbackQuery{ID: "q", From: &user{ID: 1}, Data: data, Message: &message{Chat: chat{ID: -100, Type: "group"}}}})
	}
	press("chart:EUR:RUB")
	if photo := lastPhoto(t, fake); photo.Payload["chat_id"] != "-100" || !strings.Contains(fmt.Sprint(photo.Payload["caption"]), "EUR -> RUB Bank of Russia rate") {
		t.Fatalf("chart from button = %+v", photo.Payload)
	}
	press("chart:XYZ:RUB")
	answers := fake.methodCalls("answerCallbackQuery")
	if got := answers[len(answers)-1].Payload["text"]; got != "Button expired" {
		t.Fatalf("forged button answer = %v", got)
	}
}

func TestChartWithoutHistory(t *testing.T) {
	cbr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		usd := `<Valute><CharCode>USD</CharCode><Nominal>1</Nominal><Name>USD</Name><Value>81,5</Value></Valute>`
		if r.URL.Query().Get("date_req") != "" {
			usd = "" // past documents without the currency: no history points
		}
		_, _ = fmt.Fprintf(w, `<?xml version="1.0" encoding="utf-8"?><ValCurs Date="29.09.2026">%s</ValCurs>`, usd)
	}))
	t.Cleanup(cbr.Close)
	fake, url := newFakeTelegram(t, nil)
	bot := newNewRateBot(t, cbr.URL, url, "", config.Config{})
	bot.saveLanguage(1, languageRussian)

	bot.handleUpdate(t.Context(), privateMessage(1, "/chart"))
	if len(fake.methodCalls("sendPhoto")) != 0 || !strings.Contains(lastReply(t, fake), "Не хватает данных ЦБ") {
		t.Fatalf("reply = %q, want a no-data message instead of a photo", lastReply(t, fake))
	}
}

func TestSendPhotoUsesSharedRequestPath(t *testing.T) {
	t.Run("retries once after 429", func(t *testing.T) {
		fake, url := newFakeTelegram(t, func(call apiCall, n int) (int, string) {
			if n == 1 {
				return http.StatusTooManyRequests, `{"ok":false,"error_code":429,"description":"Too Many Requests: retry after 1","parameters":{"retry_after":1}}`
			}
			return http.StatusOK, `{"ok":true}`
		})
		bot := newHardeningBot(t, config.Config{}, url)
		if err := bot.sendPhoto(t.Context(), 1, []byte("png"), "caption"); err != nil {
			t.Fatalf("sendPhoto: %v", err)
		}
		photos := fake.methodCalls("sendPhoto")
		if len(photos) != 2 || string(photos[1].File) != "png" || photos[1].Payload["caption"] != "caption" {
			t.Fatalf("sendPhoto calls = %+v", photos)
		}
	})

	t.Run("errors do not contain the token", func(t *testing.T) {
		server := httptest.NewServer(http.NotFoundHandler())
		server.Close()
		cfg := config.Config{TelegramToken: "42:" + testToken, TelegramAPI: server.URL}
		bot := New(cfg, rates.NewProvider(server.URL, t.TempDir()+"/r.json", time.Hour), slog.New(slog.DiscardHandler))
		err := bot.sendPhoto(t.Context(), 1, []byte("png"), "caption")
		if err == nil || strings.Contains(err.Error(), testToken) {
			t.Fatalf("sendPhoto error = %v, want an error without the token", err)
		}
	})

	t.Run("api errors are parsed", func(t *testing.T) {
		_, url := newFakeTelegram(t, func(call apiCall, n int) (int, string) {
			return http.StatusForbidden, `{"ok":false,"error_code":403,"description":"Forbidden: bot was blocked by the user"}`
		})
		bot := newHardeningBot(t, config.Config{}, url)
		if err := bot.sendPhoto(t.Context(), 1, []byte("png"), ""); !isChatUnreachable(err) {
			t.Fatalf("sendPhoto error = %v, want an unreachable-chat apiError", err)
		}
	})
}

func TestParseChartCallbackData(t *testing.T) {
	for data, want := range map[string]string{
		"chart:USD:RUB": "USD RUB",
		"chart:usd:eur": "USD EUR",
		"chart:XYZ:RUB": "",
		"chart:USD":     "",
		"w|USD|RUB":     "",
	} {
		from, to, ok := parseChartCallbackData(data)
		got := ""
		if ok {
			got = from + " " + to
		}
		if got != want {
			t.Fatalf("parseChartCallbackData(%q) = %q, want %q", data, got, want)
		}
	}
}
