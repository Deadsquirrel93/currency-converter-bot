# Currency Converter Telegram Bot

[🇷🇺 Документация на русском](README.md) | 🇬🇧 English documentation

A Telegram bot written in Go that converts amounts between currencies. Access can be restricted with a Telegram ID whitelist or left open to everyone.

## Features

- Russian and English interface with a 🇷🇺/🇬🇧 language picker on first use
- `/lang en` and `/lang ru` for changing the saved language
- currency pair selection, quick conversions, swaps, current rates, and daily subscriptions
- inline mode: `@your_bot 100 usd rub`
- flexible amounts such as `12,345.67`, `100x9`, and multiple lines
- quick conversions such as `100 usd to eur`, `100 dollars to rubles`, or `250 euro`
- aliases and symbols including `$`, `€`, `₽`, `dollar`, `ruble`, `pound`, `yuan`, `yen`, and `tenge`
- input multiplier, configurable rounding, input/result percentage modifiers, and extra conversion buttons
- official Bank of Russia XML exchange rates with a 60-minute file cache (`RATES_CACHE_TTL`)
- rate changes since yesterday and seven days ago, plus 30-day minimum and maximum
- persistent user settings, subscriptions, and runtime whitelist
- graceful shutdown and a multi-stage Docker image

## Configuration and launch

Copy the example configuration:

```bash
cp .env.example .env
```

Set the required values:

```dotenv
TELEGRAM_BOT_TOKEN=YOUR_TELEGRAM_BOT_TOKEN
TELEGRAM_ADMIN_USER_IDS=YOUR_TELEGRAM_USER_ID
TELEGRAM_ALLOWED_USER_IDS=YOUR_TELEGRAM_USER_ID,ANOTHER_TELEGRAM_USER_ID
DEFAULT_FROM=USD
DEFAULT_TO=RUB
RATES_CACHE_FILE=/app/data/rates_cache.json
USER_SETTINGS_FILE=/app/data/user_settings.json
ALLOWED_USERS_FILE=/app/data/allowed_users.json
SUBSCRIPTIONS_FILE=/app/data/subscriptions.json
SUBSCRIPTION_TIMEZONE=Asia/Tashkent
CBR_DAILY_URLS=https://www.cbr.ru/scripts/XML_daily.asp
```

To make the bot public, leave the admin list, the env whitelist, and the runtime whitelist (`ALLOWED_USERS_FILE`) empty:

```dotenv
TELEGRAM_ADMIN_USER_IDS=
TELEGRAM_ALLOWED_USER_IDS=
```

Prepare the persistent data directory and start the bot:

```bash
mkdir -p data
sudo chown -R 10001:10001 data
docker compose up -d --build
```

The container runs with UID/GID `10001`, so the host `data` directory must be writable by that user.

## Language selection

On `/start` or the first direct request without a saved language, the bot shows `🇷🇺 Русский` and `🇬🇧 English` buttons. The selection is stored in `data/user_settings.json`.

```text
/lang en
/lang ru
/lang
```

`/lang` without an argument displays the buttons again. `/reset` preserves the selected language; `/delete` removes it together with all other user settings.

## Usage

```text
/from USD
/to RUB
12,345.67
100 dollars to rubles
100$ in rub
```

Amounts on separate lines are added before conversion. Multiplication with `x`, `х`, or `*` is also supported:

```text
70 x 5 liters of milk
12 * 4 kg of chicken
```

Enable inline mode with BotFather's `/setinline`, then use:

```text
@your_bot 100 usd rub
```

Until a user picks a language in a private chat, inline results use the language of their Telegram app (English for `en`, Russian otherwise). When access is restricted, inline results are returned only to allowed Telegram IDs.

Thousands separators: `1 000 000`, `1.000.000`, and `1,000,000` all mean one million, and `12,345.67` is 12345.67. A single comma is read by interface language: with English, `1,000` is 1000 and `1,5` is 1.5; with Russian, a single comma is always decimal.

## Commands

- `/lang en|ru` — change the interface language
- `/from USD`, `/to RUB`, `/swap` — configure the currency pair
- `/rate [USD RUB]` — show the current rate and historical changes
- `/subscribe 09:00 [USD RUB]` — enable a daily rate message
- `/subscription`, `/unsubscribe` — inspect or disable the subscription
- `/with USD EUR RUB`, `/with off` — configure extra conversion buttons
- `/with_modify yes|no` — apply percentage modifiers to button conversions
- `/inline_modify yes|no` — apply percentage modifiers to explicit currencies in text
- `/multi 1000` — multiply the input amount before conversion
- `/round auto|0|2|4|6` — configure result rounding
- `/modify_from 1.5`, `/modify_to 1.5` — adjust input/result by a percentage
- `/settings`, `/reset`, `/delete` — inspect, reset, or delete user settings
- `/list`, `/whoami`, `/help` — currencies, Telegram ID, and command help
- `/allow ID`, `/disallow ID`, `/allowed` — administrator-only whitelist commands

Explicit-currency conversions do not use `/multi`. They use percentage modifiers only after `/inline_modify yes`.

`/delete` removes all of the user's data: settings, the saved language, and the subscription. The next message shows the language picker again.

Supported currencies are the ones listed by `/list`: `RUB` plus popular currencies published by the Bank of Russia. Cross rates are calculated through the ruble.

## Daily subscriptions

`/subscribe 09:00 [USD RUB]` sends the rate every day in the timezone from `SUBSCRIPTION_TIMEZONE` (an IANA name such as `Asia/Tashkent` or `Europe/Moscow`; default `Asia/Tashkent`). The timezone database is embedded into the binary, so the image does not need `tzdata`. With an invalid name the bot refuses to start and logs the reason.

Each user has one subscription: a new `/subscribe` replaces it, and messages go to the chat where the command was sent. If the time has already passed today, the bot sends the current rate right away and the next message arrives tomorrow. If the bot was offline at the scheduled time, it sends today's rate after startup.

## Deployment

Update an existing deployment with:

```bash
./deploy.sh
```

The script pulls with `git pull --ff-only`, rebuilds the image, and recreates the Compose services. If settings are not persisted, inspect `docker compose logs bot` and verify that `data` is writable by UID `10001`.

Multiple compatible XML sources can be configured as a comma-separated fallback list (the legacy `CBR_DAILY_URL` name is also accepted):

```dotenv
CBR_DAILY_URLS=https://www.cbr.ru/scripts/XML_daily.asp,https://cbr.ru/scripts/XML_daily.asp
```

Other optional settings:

- `RATES_CACHE_TTL` — how long rates are cached, default `60m`; if the Bank of Russia is unavailable, the last cached rates are used
- `TELEGRAM_API_BASE` — Bot API base URL, default `https://api.telegram.org` (for a self-hosted Bot API server)
