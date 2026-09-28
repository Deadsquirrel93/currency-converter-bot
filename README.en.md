# Currency Converter Telegram Bot

[🇷🇺 Документация на русском](README.md) | 🇬🇧 English documentation

A Telegram bot written in Go that converts amounts between currencies. Access can be restricted with a Telegram ID whitelist or left open to everyone.

## Features

- Russian and English interface with a 🇷🇺/🇬🇧 language picker on first use
- `/lang en` and `/lang ru` for changing the saved language
- currency pair selection, quick conversions, swaps, current rates, and daily subscriptions
- arithmetic in amounts: `100+50 usd`, `(12*3)+5`, `1,000 / 4`
- a per-user time zone (`/tz`), defaulting to `SUBSCRIPTION_TIMEZONE`
- a "new rate" subscription that fires as soon as the Bank of Russia sets the next day's rate
- threshold alerts: `/alert USD RUB > 95`
- a 30-day rate chart as an image: `/chart USD RUB`, also a button under `/rate`
- inline mode: `@your_bot 100 usd rub`
- flexible amounts such as `12,345.67`, `100x9`, and multiple lines
- quick conversions such as `100 usd to eur`, `100 dollars to rubles`, or `250 euro`
- aliases and symbols including `$`, `€`, `₽`, `dollar`, `ruble`, `pound`, `yuan`, `yen`, and `tenge`
- input multiplier, configurable rounding, input/result percentage modifiers, and extra conversion buttons
- official Bank of Russia XML exchange rates with a 60-minute file cache (`RATES_CACHE_TTL`)
- rate changes since yesterday and seven days ago, plus 30-day minimum and maximum
- persistent user settings, subscriptions, alerts, and runtime whitelist
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
NEW_RATE_SUBSCRIPTIONS_FILE=/app/data/new_rate_subscriptions.json
ALERTS_FILE=/app/data/alerts.json
SUBSCRIPTION_TIMEZONE=Asia/Tashkent
CBR_DAILY_URLS=https://www.cbr.ru/scripts/XML_daily.asp
```

Data file paths are optional: by default every file is created in the `data` directory next to the binary, which is `/app/data` in the container.

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

Amounts can be arithmetic expressions with parentheses and `+`, `-`, `*`, `/` (also `×`, `÷`, `х`):

```text
100+50 usd
(12*3)+5
1,000 / 4
100 usd + 50 usd
```

Multiplication and division come before addition and subtraction. A minus between numbers without spaces (`12-05-2024`, `8-800-555-35-35`, `10-15 usd`) is read as part of a date, phone number, or range, not as subtraction; put spaces around it to subtract: `100 - 50`. Inside an expression with other operators or parentheses it works without spaces: `(100-50)*2`. Division by zero, a negative result, or an expression longer than 200 characters or deeper than 10 levels of parentheses gives a clear error.

Enable inline mode with BotFather's `/setinline`, then use:

```text
@your_bot 100 usd rub
```

Until a user picks a language in a private chat, inline results use the language of their Telegram app (English for `en`, Russian otherwise). When access is restricted, inline results are returned only to allowed Telegram IDs.

Thousands separators: `1 000 000`, `1.000.000`, and `1,000,000` all mean one million, and `12,345.67` is 12345.67. A single comma is read by interface language: with English, `1,000` is 1000 and `1,5` is 1.5; with Russian, a single comma is always decimal.

If a line contains several separate numbers (`iPhone 15 for 1000`, `2 coffees at 350`), the bot does not glue them together; it asks to keep one number or to multiply them: `2 x 350`. A currency may stand before the multiplication sign: `100 usd x 9`.

Limits: modifier percentages must be above `-100` and at most `1000`, the `/multi` multiplier at most one billion, amounts at most 10^15, expressions at most 200 characters and 10 levels of parentheses, and at most 10 alerts per user.

## Commands

- `/lang en|ru` — change the interface language
- `/from USD`, `/to RUB`, `/swap` — configure the currency pair
- `/rate [USD RUB]` — show the current rate and historical changes
- `/chart [USD RUB]` — send a 30-day rate chart
- `/subscribe 09:00 [USD RUB]` — enable a daily rate message
- `/subscribe new [USD RUB]` — get the new Bank of Russia rate as soon as it is set
- `/subscription`, `/unsubscribe [daily|new]` — inspect or disable subscriptions
- `/tz Europe/Moscow`, `/tz`, `/tz default` — set, show, or reset your time zone
- `/alert USD RUB > 95`, `/alerts`, `/alert off N` — threshold alerts
- `/with USD EUR RUB`, `/with off` — configure extra conversion buttons
- `/with_modify yes|no` — apply percentage modifiers to button conversions
- `/inline_modify yes|no` — apply percentage modifiers to explicit currencies in text
- `/multi 1000` — multiply the input amount before conversion
- `/round auto|0|2|4|6` — configure result rounding
- `/modify_from 1.5`, `/modify_to 1.5` — adjust input/result by a percentage
- `/settings`, `/reset`, `/delete` — inspect, reset (language and time zone are kept), or delete user data
- `/list`, `/whoami`, `/help` — currencies, Telegram ID, and command help
- `/allow ID`, `/disallow ID`, `/allowed` — administrator-only whitelist commands

Explicit-currency conversions do not use `/multi`. They use percentage modifiers only after `/inline_modify yes`.

`/delete` removes all of the user's data: settings, the saved language and time zone, subscriptions, and alerts. The next message shows the language picker again.

Supported currencies are the ones listed by `/list`: `RUB` plus popular currencies published by the Bank of Russia. Cross rates are calculated through the ruble.

## Rates and charts

The Bank of Russia sets rates on business days (usually in the afternoon, Moscow time), and they come into force the next calendar day. After publication `/rate` already shows tomorrow's rate and says its date ("Bank of Russia rate for 29.09.2026:"), and "since yesterday" compares it with the rate in force today. History days are counted in the user's time zone (`/tz`).

`/rate` has a "📈 30-day chart" button. `/chart [USD RUB]` sends a PNG chart of the last 30 days; the caption holds the latest rate, the change over the period, and the minimum and maximum. The image is drawn in memory with the standard library only.

## Time zones

`/tz Europe/Moscow` sets the user's time zone for the daily subscription and the `/rate` history days. It accepts IANA names (any case: `europe/moscow`), `UTC`, `msk`, and offsets such as `+3`, `UTC+3`, `GMT+05:30`. `/tz` shows the current zone and time; `/tz default` goes back to `SUBSCRIPTION_TIMEZONE`. When the zone changes, the last delivery date is moved to the new zone, so the subscription is neither repeated nor skipped; a delivery that would come less than 12 hours after the previous one is skipped.

## Daily subscriptions

`/subscribe 09:00 [USD RUB]` sends the rate every day in the user's time zone, or in `SUBSCRIPTION_TIMEZONE` if the user has not chosen one (an IANA name such as `Asia/Tashkent` or `Europe/Moscow`; default `Asia/Tashkent`). The timezone database is embedded into the binary, so the image does not need `tzdata`. With an invalid `SUBSCRIPTION_TIMEZONE` the bot refuses to start and logs the reason.

Each user has one daily subscription: a new `/subscribe 09:00` replaces it, and messages go to the chat where the command was sent. If the time has already passed today, the bot sends the current rate right away and the next message arrives tomorrow. If the bot was offline at the scheduled time, it sends today's rate after startup.

## New rate subscription

`/subscribe new [USD RUB]` sends the rate as soon as the Bank of Russia sets the next day's rate, with the change from the current rate: one message per rate date. If tomorrow's rate is already out when you subscribe, it is sent right away. It can be kept together with the daily subscription. `/unsubscribe` disables both, `/unsubscribe daily` or `/unsubscribe new` only one.

The bot polls the Bank of Russia every 10 minutes from 15:30 to 20:00 Moscow time and hourly otherwise, and only while someone has a new rate subscription or an alert.

## Alerts

```text
/alert USD RUB > 95
/alert EUR RUB < 90.5
/alert > 95
/alerts
/alert off 2
```

An alert fires once when the Bank of Russia rate crosses the threshold and is then removed. Conditions: `>`, `<`, `>=`, `<=`, or the words `above` and `below`. Without currencies the pair from the settings is used. If the condition already holds at the current rate, the alert is not created. Alerts are checked together with the new rate check and are sent to the chat where they were created.

`/alerts` lists them with the current rate and remove buttons; `/alert off N` or `/alert off all` removes them too. Alert numbers do not shift after a removal. A user can keep at most 10 alerts.

## Groups

The bot can be added to a group. There it reacts only to commands (`/rate` or `/rate@your_bot`), to messages starting with `@your_bot` (for example `@your_bot 100 usd`), and to replies to its own messages; other chatter is ignored. Users outside the whitelist get no replies in groups.

## Flood and failure protection

- Each user can send up to 30 requests per minute; above that the bot warns once and stays silent until the minute ends.
- Users outside the whitelist see the access notice at most once every 10 minutes.
- Past-day rates are cached in memory, so `/rate` and subscriptions do not re-download the same dates from the Bank of Russia, and history collection has a time limit.
- If a user blocks the bot or the bot is removed from a group, the subscription is deleted. On temporary Telegram errors delivery is retried after 5, 15, and 45 minutes, then postponed until the next day.
- New rate messages and alerts follow the same rules: an unreachable chat drops the subscription or the alerts for that chat; temporary errors are retried after 5, 15, and 45 minutes, then the new rate is skipped until the next rate date and the alert is dropped.
- After `/disallow` the user's subscriptions and alerts stop (and resume if access is granted again).
- While Telegram is unreachable the retry pause grows from 3 seconds to a minute; `429 Too Many Requests` is honored.
- Admin commands `/allow`, `/disallow`, `/allowed` appear in the command menu only for admins (an admin must have messaged the bot at least once).

## Deployment

Update an existing deployment with:

```bash
./deploy.sh
```

The script pulls with `git pull --ff-only`, rebuilds the image, and recreates the Compose services. If settings are not persisted, inspect `docker compose logs bot` and verify that `data` is writable by UID `10001`.

The container runs hardened: read-only root filesystem (only `/app/data` is writable), all capabilities dropped, `no-new-privileges`, memory and process limits. Logs are capped at three 10 MB files, so they cannot fill the disk.

Data files are created with mode `600` and the data directory with `700`, because they contain Telegram user IDs. For an existing directory, fix the permissions from inside the container (the container user owns it):

```bash
docker compose exec bot sh -c 'chmod 700 /app/data && chmod 600 /app/data/*.json'
```

If a settings, subscriptions, or whitelist file is corrupted, the bot does not overwrite it with empty data: it renames the file to `*.corrupt-<time>`, logs an error, and keeps running, so the data can be recovered manually.

Multiple compatible XML sources can be configured as a comma-separated fallback list (the legacy `CBR_DAILY_URL` name is also accepted):

```dotenv
CBR_DAILY_URLS=https://www.cbr.ru/scripts/XML_daily.asp,https://cbr.ru/scripts/XML_daily.asp
```

Other optional settings:

- `RATES_CACHE_TTL` — how long rates are cached, default `60m`; if the Bank of Russia is unavailable, the last cached rates are used
- `TELEGRAM_API_BASE` — Bot API base URL, default `https://api.telegram.org` (for a self-hosted Bot API server)
- `SUBSCRIPTION_TIMEZONE` — the default time zone for users who have not chosen one with `/tz`
- `NEW_RATE_SUBSCRIPTIONS_FILE` — new rate subscriptions file, default `data/new_rate_subscriptions.json`
- `ALERTS_FILE` — alerts file, default `data/alerts.json`
