package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

func (b *Bot) isAllowed(userID int64) bool {
	if b.cfg.IsAdmin(userID) {
		return true
	}

	b.accessMu.RLock()
	_, dynamicAllowed := b.allowedUsers[userID]
	dynamicCount := len(b.allowedUsers)
	b.accessMu.RUnlock()
	if dynamicAllowed {
		return true
	}

	if _, ok := b.cfg.AllowedUsers[userID]; ok {
		return true
	}
	return len(b.cfg.AdminUsers) == 0 && len(b.cfg.AllowedUsers) == 0 && dynamicCount == 0
}

func (b *Bot) whoamiText(userID int64) string {
	language := b.userLanguage(userID)
	if b.cfg.IsAdmin(userID) {
		return fmt.Sprintf(tr(language, "Ваш Telegram ID: %d\nРоль: админ", "Your Telegram ID: %d\nRole: admin"), userID)
	}
	if b.isAllowed(userID) {
		return fmt.Sprintf(tr(language, "Ваш Telegram ID: %d\nДоступ: разрешен", "Your Telegram ID: %d\nAccess: allowed"), userID)
	}
	return fmt.Sprintf(tr(language, "Ваш Telegram ID: %d\nДоступ: не разрешен", "Your Telegram ID: %d\nAccess: not allowed"), userID)
}

func (b *Bot) showBlockedUserMessage(ctx context.Context, chatID, userID int64, text string) {
	_ = b.sendMessage(ctx, chatID, fmt.Sprintf("Доступ к боту ограничен / Bot access is restricted.\nTelegram ID: %d\nПередайте ID администратору / Send this ID to the administrator.", userID))
}

func (b *Bot) allowUsers(ctx context.Context, chatID, adminID int64, text string) {
	if !b.requireAdmin(ctx, chatID, adminID) {
		return
	}

	ids, err := parseTelegramUserIDArgs(commandArgs(text))
	if err != nil {
		_ = b.sendMessage(ctx, chatID, tr(b.userLanguage(adminID), err.Error(), "Specify a numeric Telegram ID, for example /allow 123456789."))
		return
	}

	added, already := b.addAllowedUserIDs(ids)
	language := b.userLanguage(adminID)
	parts := []string{}
	if len(added) > 0 {
		parts = append(parts, tr(language, "Добавлены: ", "Added: ")+formatIDs(added)+".")
	}
	if len(already) > 0 {
		parts = append(parts, tr(language, "Уже были с доступом: ", "Already allowed: ")+formatIDs(already)+".")
	}
	_ = b.sendMessage(ctx, chatID, strings.Join(parts, "\n"))
}

func (b *Bot) disallowUsers(ctx context.Context, chatID, adminID int64, text string) {
	if !b.requireAdmin(ctx, chatID, adminID) {
		return
	}

	ids, err := parseTelegramUserIDArgs(commandArgs(text))
	if err != nil {
		_ = b.sendMessage(ctx, chatID, tr(b.userLanguage(adminID), err.Error(), "Specify a numeric Telegram ID, for example /disallow 123456789."))
		return
	}

	removed, protected, missing := b.removeAllowedUserIDs(ids)
	language := b.userLanguage(adminID)
	parts := []string{}
	if len(removed) > 0 {
		parts = append(parts, tr(language, "Удалены из runtime whitelist: ", "Removed from runtime whitelist: ")+formatIDs(removed)+".")
	}
	if len(protected) > 0 {
		parts = append(parts, tr(language, "Не удалены из-за .env/admin-настроек: ", "Protected by .env/admin settings: ")+formatIDs(protected)+".")
	}
	if len(missing) > 0 {
		parts = append(parts, tr(language, "Не были в runtime whitelist: ", "Not in the runtime whitelist: ")+formatIDs(missing)+".")
	}
	_ = b.sendMessage(ctx, chatID, strings.Join(parts, "\n"))
}

func (b *Bot) showAllowedUsers(ctx context.Context, chatID, adminID int64) {
	if !b.requireAdmin(ctx, chatID, adminID) {
		return
	}

	b.accessMu.RLock()
	dynamic := sortedIDs(b.allowedUsers)
	b.accessMu.RUnlock()

	admins := sortedIDs(b.cfg.AdminUsers)
	envAllowed := sortedIDs(b.cfg.AllowedUsers)
	language := b.userLanguage(adminID)
	if len(admins) == 0 && len(envAllowed) == 0 && len(dynamic) == 0 {
		_ = b.sendMessage(ctx, chatID, tr(language, "Whitelist пустой: бот сейчас открыт для всех пользователей.", "The whitelist is empty: the bot is open to everyone."))
		return
	}

	_ = b.sendMessage(ctx, chatID, fmt.Sprintf(
		tr(language, "Доступ к боту:\nАдмины: %s\nИз .env: %s\nДобавлены командами: %s", "Bot access:\nAdmins: %s\nFrom .env: %s\nAdded by commands: %s"),
		formatIDsOrDash(admins),
		formatIDsOrDash(envAllowed),
		formatIDsOrDash(dynamic),
	))
}

func (b *Bot) requireAdmin(ctx context.Context, chatID, userID int64) bool {
	if b.cfg.IsAdmin(userID) {
		return true
	}
	_ = b.sendMessage(ctx, chatID, tr(b.userLanguage(userID), "Эта команда доступна только администратору.", "This command is available only to an administrator."))
	return false
}

func (b *Bot) addAllowedUserIDs(ids []int64) ([]int64, []int64) {
	added := []int64{}
	already := []int64{}

	b.accessMu.Lock()
	for _, id := range ids {
		if b.cfg.IsAdmin(id) {
			already = append(already, id)
			continue
		}
		if _, ok := b.cfg.AllowedUsers[id]; ok {
			already = append(already, id)
			continue
		}
		if _, ok := b.allowedUsers[id]; ok {
			already = append(already, id)
			continue
		}
		b.allowedUsers[id] = struct{}{}
		added = append(added, id)
	}
	snapshot := copyIDSet(b.allowedUsers)
	b.accessMu.Unlock()

	if len(added) > 0 {
		if err := b.writeAllowedUsers(snapshot); err != nil {
			b.log.Error("save allowed users failed", "path", b.cfg.AllowedUsersFile, "error", err)
		}
	}
	return sortedInt64s(added), sortedInt64s(already)
}

func (b *Bot) removeAllowedUserIDs(ids []int64) ([]int64, []int64, []int64) {
	removed := []int64{}
	protected := []int64{}
	missing := []int64{}

	b.accessMu.Lock()
	for _, id := range ids {
		if b.cfg.IsAdmin(id) {
			protected = append(protected, id)
			continue
		}
		if _, ok := b.cfg.AllowedUsers[id]; ok {
			protected = append(protected, id)
			continue
		}
		if _, ok := b.allowedUsers[id]; !ok {
			missing = append(missing, id)
			continue
		}
		delete(b.allowedUsers, id)
		removed = append(removed, id)
	}
	snapshot := copyIDSet(b.allowedUsers)
	b.accessMu.Unlock()

	if len(removed) > 0 {
		if err := b.writeAllowedUsers(snapshot); err != nil {
			b.log.Error("save allowed users failed", "path", b.cfg.AllowedUsersFile, "error", err)
		}
	}
	return sortedInt64s(removed), sortedInt64s(protected), sortedInt64s(missing)
}

func parseTelegramUserIDArgs(args string) ([]int64, error) {
	args = strings.TrimSpace(args)
	if args == "" {
		return nil, errors.New("Укажите Telegram ID: /allow 123456789")
	}

	parts := strings.FieldsFunc(args, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\t' || r == ' '
	})
	ids := make([]int64, 0, len(parts))
	seen := map[int64]struct{}{}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if strings.HasPrefix(part, "@") {
			return nil, errors.New("По username добавлять ненадежно: username меняется, а бот не всегда может получить по нему user ID. Попросите пользователя отправить /whoami и добавьте числовой Telegram ID.")
		}
		id, err := strconv.ParseInt(part, 10, 64)
		if err != nil || id <= 0 {
			return nil, fmt.Errorf("Некорректный Telegram ID %q. Нужен числовой ID, например /allow 123456789.", part)
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil, errors.New("Укажите Telegram ID: /allow 123456789")
	}
	return ids, nil
}

func (b *Bot) loadAllowedUsers() error {
	if strings.TrimSpace(b.cfg.AllowedUsersFile) == "" {
		return nil
	}
	raw, err := os.ReadFile(b.cfg.AllowedUsersFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}

	users, err := unmarshalAllowedUserIDs(raw)
	if err != nil {
		return err
	}

	b.accessMu.Lock()
	for _, userID := range users {
		b.allowedUsers[userID] = struct{}{}
	}
	b.accessMu.Unlock()
	return nil
}

func (b *Bot) writeAllowedUsers(users map[int64]struct{}) error {
	if strings.TrimSpace(b.cfg.AllowedUsersFile) == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(b.cfg.AllowedUsersFile), 0o755); err != nil {
		return err
	}

	raw, err := json.MarshalIndent(sortedIDs(users), "", "  ")
	if err != nil {
		return err
	}

	tmpFile := b.cfg.AllowedUsersFile + ".tmp"
	if err := os.WriteFile(tmpFile, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpFile, b.cfg.AllowedUsersFile)
}

func unmarshalAllowedUserIDs(raw []byte) ([]int64, error) {
	var ids []int64
	if err := json.Unmarshal(raw, &ids); err == nil {
		return normalizeUserIDs(ids), nil
	}

	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, err
	}

	ids = make([]int64, 0, len(object))
	for key := range object {
		id, err := strconv.ParseInt(key, 10, 64)
		if err != nil || id <= 0 {
			return nil, fmt.Errorf("parse allowed user id %q", key)
		}
		ids = append(ids, id)
	}
	return normalizeUserIDs(ids), nil
}

func normalizeUserIDs(ids []int64) []int64 {
	seen := map[int64]struct{}{}
	result := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return sortedInt64s(result)
}

func copyIDSet(users map[int64]struct{}) map[int64]struct{} {
	result := make(map[int64]struct{}, len(users))
	for userID := range users {
		result[userID] = struct{}{}
	}
	return result
}

func sortedIDs(users map[int64]struct{}) []int64 {
	ids := make([]int64, 0, len(users))
	for userID := range users {
		ids = append(ids, userID)
	}
	return sortedInt64s(ids)
}

func sortedInt64s(ids []int64) []int64 {
	sort.Slice(ids, func(i, j int) bool {
		return ids[i] < ids[j]
	})
	return ids
}

func formatIDs(ids []int64) string {
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, strconv.FormatInt(id, 10))
	}
	return strings.Join(parts, ", ")
}

func formatIDsOrDash(ids []int64) string {
	if len(ids) == 0 {
		return "-"
	}
	return formatIDs(ids)
}
