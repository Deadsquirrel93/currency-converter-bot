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
- official Bank of Russia XML exchange rates with a 60-minute file cache
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

Leave both user lists empty to make the bot public:

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

## Deployment

Update an existing deployment with:

```bash
./deploy.sh
```

The script pulls with `git pull --ff-only`, rebuilds the image, and recreates the Compose services. If settings are not persisted, inspect `docker compose logs bot` and verify that `data` is writable by UID `10001`.

Multiple compatible XML sources can be configured as a comma-separated fallback list:

```dotenv
CBR_DAILY_URLS=https://www.cbr.ru/scripts/XML_daily.asp,https://cbr.ru/scripts/XML_daily.asp
```
