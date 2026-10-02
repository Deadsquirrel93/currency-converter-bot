package telegram

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

// maxTimezoneLength is longer than any IANA name; it only bounds user input.
const maxTimezoneLength = 64

var errUnknownTimezone = userError{
	ru: "Не знаю такой часовой пояс. Примеры: /tz Europe/Moscow, /tz Asia/Tashkent, /tz UTC+3, /tz мск.",
	en: "Unknown time zone. Examples: /tz Europe/Moscow, /tz Asia/Tashkent, /tz UTC+3.",
}

// locationCache keeps loaded time zones: time.LoadLocation reads and parses
// the zone database on every call, and the scheduler asks every minute.
var locationCache sync.Map

// loadTimezone loads a zone name as stored in the user settings: an IANA name,
// "UTC" or a fixed offset like "UTC+03:00" produced by parseTimezone.
func loadTimezone(name string) (*time.Location, error) {
	if cached, ok := locationCache.Load(name); ok {
		return cached.(*time.Location), nil
	}
	location, err := loadTimezoneUncached(name)
	if err != nil {
		return nil, err
	}
	locationCache.Store(name, location)
	return location, nil
}

func loadTimezoneUncached(name string) (*time.Location, error) {
	// LoadLocation maps "" to UTC and "Local" to the server zone; neither is a
	// valid user choice.
	if name == "" || strings.EqualFold(name, "local") || len(name) > maxTimezoneLength {
		return nil, errUnknownTimezone
	}
	if offset, ok := parseUTCOffset(name); ok {
		if offset == 0 {
			return time.UTC, nil
		}
		return time.FixedZone(name, offset), nil
	}
	return time.LoadLocation(name)
}

// parseTimezone reads /tz input and returns the canonical name to store.
func parseTimezone(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > maxTimezoneLength {
		return "", errUnknownTimezone
	}
	switch strings.ToLower(raw) {
	case "мск", "msk", "москва", "moscow":
		return "Europe/Moscow", nil
	case "utc", "gmt", "z":
		return "UTC", nil
	}
	if offset, ok := parseUTCOffset(raw); ok {
		return formatUTCOffset(offset), nil
	}
	for _, name := range timezoneCandidates(raw) {
		if _, err := loadTimezone(name); err == nil {
			return name, nil
		}
	}
	return "", errUnknownTimezone
}

// timezoneCandidates tries the IANA capitalization first for input typed in
// one case ("europe/moscow" -> "Europe/Moscow"): the embedded zone database
// is case-sensitive even where the file system is not.
func timezoneCandidates(raw string) []string {
	if raw != strings.ToLower(raw) && raw != strings.ToUpper(raw) {
		return []string{raw}
	}
	runes := []rune(strings.ToLower(raw))
	for i, r := range runes {
		if i == 0 || runes[i-1] == '/' || runes[i-1] == '_' || runes[i-1] == '-' {
			runes[i] = unicode.ToUpper(r)
		}
	}
	if canonical := string(runes); canonical != raw {
		return []string{canonical, raw}
	}
	return []string{raw}
}

// parseUTCOffset reads "+3", "-04:30", "+0530", "UTC+3" or "GMT-2" and
// returns the offset in seconds east of UTC. A sign is required, so a bare
// number is not taken for a zone.
func parseUTCOffset(raw string) (int, bool) {
	value := strings.TrimSpace(raw)
	if len(value) >= 3 && (strings.EqualFold(value[:3], "utc") || strings.EqualFold(value[:3], "gmt")) {
		value = strings.TrimSpace(value[3:])
	}
	sign := 1
	switch {
	case strings.HasPrefix(value, "+"):
		value = value[1:]
	case strings.HasPrefix(value, "-"):
		sign = -1
		value = value[1:]
	case strings.HasPrefix(value, "−"):
		sign = -1
		value = strings.TrimPrefix(value, "−")
	default:
		return 0, false
	}

	hoursRaw, minutesRaw, hasColon := strings.Cut(value, ":")
	if !hasColon && len(value) == 4 {
		hoursRaw, minutesRaw = value[:2], value[2:]
	}
	if len(hoursRaw) == 0 || len(hoursRaw) > 2 || (minutesRaw != "" && len(minutesRaw) != 2) || (hasColon && minutesRaw == "") {
		return 0, false
	}
	hours, err := strconv.Atoi(hoursRaw)
	if err != nil || hours < 0 {
		return 0, false
	}
	minutes := 0
	if minutesRaw != "" {
		minutes, err = strconv.Atoi(minutesRaw)
		if err != nil || minutes < 0 || minutes > 59 {
			return 0, false
		}
	}
	offset := sign * (hours*3600 + minutes*60)
	// Real offsets range from UTC-12 to UTC+14.
	if offset < -12*3600 || offset > 14*3600 {
		return 0, false
	}
	return offset, true
}

func formatUTCOffset(offset int) string {
	if offset == 0 {
		return "UTC"
	}
	sign := "+"
	if offset < 0 {
		sign = "-"
		offset = -offset
	}
	return fmt.Sprintf("UTC%s%02d:%02d", sign, offset/3600, offset%3600/60)
}

// defaultLocation is SUBSCRIPTION_TIMEZONE, used for users without /tz.
func (b *Bot) defaultLocation() *time.Location {
	if b.cfg.Location == nil {
		return time.Local
	}
	return b.cfg.Location
}

func (b *Bot) userLocation(userID int64) *time.Location {
	if name := b.getSession(userID).Timezone; name != "" {
		if location, err := loadTimezone(name); err == nil {
			return location
		}
	}
	return b.defaultLocation()
}

// timezoneLabel names the user's zone and marks the default one.
func (b *Bot) timezoneLabel(userID int64, language string) string {
	label := b.userLocation(userID).String()
	if b.getSession(userID).Timezone == "" {
		label += tr(language, " (по умолчанию)", " (default)")
	}
	return label
}

func (b *Bot) setTimezone(ctx context.Context, chatID, userID int64, text string) {
	language := b.userLanguage(userID)
	args := commandArgs(text)
	if args == "" {
		now := time.Now().In(b.userLocation(userID))
		_ = b.sendMessage(ctx, chatID, fmt.Sprintf(tr(language,
			"Часовой пояс: %s, сейчас %s.\nПо нему приходит подписка и считаются дни в истории /rate.\nИзменить: /tz Europe/Moscow, /tz UTC+3 или /tz мск. Сбросить: /tz default.",
			"Time zone: %s, now %s.\nIt is used for the subscription and for the days in the /rate history.\nChange it: /tz Europe/Moscow or /tz UTC+3. Reset: /tz default."),
			b.timezoneLabel(userID, language), now.Format("15:04")))
		return
	}

	name := ""
	if !isOffValue(args) && !isDefaultValue(args) {
		var err error
		if name, err = parseTimezone(args); err != nil {
			_ = b.sendMessage(ctx, chatID, errorText(err, language))
			return
		}
	}

	previous := b.userLocation(userID)
	s := b.getSession(userID)
	s.Timezone = name
	b.setSession(userID, s)
	location := b.userLocation(userID)
	b.rebaseSubscription(userID, previous, location)

	reply := fmt.Sprintf(tr(language, "Готово: часовой пояс %s, сейчас %s.", "Done: time zone %s, now %s."),
		b.timezoneLabel(userID, language), time.Now().In(location).Format("15:04"))
	if subscription, ok := b.getUserSubscription(userID); ok {
		reply += fmt.Sprintf(tr(language, " Подписка будет приходить в %s по этому времени.", " The subscription will arrive at %s in this time zone."), subscription.Time)
	}
	_ = b.sendMessage(ctx, chatID, reply)
}

func isDefaultValue(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "default", "reset", "сброс", "по умолчанию":
		return true
	default:
		return false
	}
}

// rebaseSubscription moves the last delivery day of the user's subscription
// to the new time zone, so changing /tz neither repeats nor skips a message.
func (b *Bot) rebaseSubscription(userID int64, from, to *time.Location) {
	b.subMu.Lock()
	subscription, ok := b.subscriptions[userID]
	if !ok {
		b.subMu.Unlock()
		return
	}
	subscription = normalizeSubscription(subscription)
	subscription.LastSentDate = rebaseLastSentDate(subscription, from, to)
	b.subscriptions[userID] = subscription
	delete(b.subRetry, userID)
	b.subMu.Unlock()

	if err := b.saveSubscriptions(); err != nil {
		b.log.Error("save subscriptions failed", "path", b.cfg.SubscriptionsFile, "error", err)
	}
}

// minDeliveryGap keeps two daily messages from arriving hours apart when the
// user moves to a zone that is behind the old one.
const minDeliveryGap = 12 * time.Hour

// rebaseLastSentDate treats the last delivery as made at its scheduled time in
// the old zone and returns the day in the new zone that it covers: the latest
// day whose scheduled time is not after it, or the next day if that one's
// scheduled time is less than minDeliveryGap later.
func rebaseLastSentDate(subscription dailySubscription, from, to *time.Location) string {
	if subscription.LastSentDate == "" || subscription.Time == "" {
		return subscription.LastSentDate
	}
	day, err := time.ParseInLocation("2006-01-02", subscription.LastSentDate, from)
	if err != nil {
		return subscription.LastSentDate
	}
	clock, err := time.Parse("15:04", subscription.Time)
	if err != nil {
		return subscription.LastSentDate
	}
	last := time.Date(day.Year(), day.Month(), day.Day(), clock.Hour(), clock.Minute(), 0, 0, from)

	local := last.In(to)
	covered := time.Date(local.Year(), local.Month(), local.Day(), clock.Hour(), clock.Minute(), 0, 0, to)
	if covered.After(last) {
		covered = covered.AddDate(0, 0, -1)
	}
	if next := covered.AddDate(0, 0, 1); next.Sub(last) < minDeliveryGap {
		covered = next
	}
	return subscriptionDate(covered)
}
