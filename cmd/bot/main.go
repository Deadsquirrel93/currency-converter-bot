package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	// Embed the IANA timezone database: the alpine runtime image has no tzdata,
	// so SUBSCRIPTION_TIMEZONE would otherwise silently fall back to UTC.
	_ "time/tzdata"

	"currency-converter-bot/internal/config"
	"currency-converter-bot/internal/rates"
	"currency-converter-bot/internal/telegram"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	cfg, err := config.Load(".env")
	if err != nil {
		logger.Error("config error", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	provider := rates.NewProvider(cfg.CBRDailyURL, cfg.CacheFile, cfg.CacheTTL)
	bot := telegram.New(cfg, provider, logger)

	logger.Info("bot started")
	if err := bot.Run(ctx); err != nil {
		logger.Error("bot stopped with error", "error", err)
		os.Exit(1)
	}
	logger.Info("bot stopped")
}
