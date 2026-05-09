# Currency Converter Telegram Bot

Telegram-бот на Go, который отвечает только пользователям из whitelist по Telegram ID и конвертирует суммы между валютами.

## Возможности

- whitelist пользователей через `TELEGRAM_ALLOWED_USER_IDS`
- команды `/from USD`, `/to EUR`, `/help`, `/list`
- ввод суммы свободным текстом: `12 345,67 usd` -> `12345.67`
- курсы валют из официального XML ЦБ РФ
- файловый кеш курсов на 60 минут
- graceful shutdown по `SIGINT`/`SIGTERM`
- Docker multi-stage build и `restart: unless-stopped` в Compose

## Запуск

1. Скопируйте пример конфига:

```bash
cp .env.example .env
```

2. Заполните `.env`:

```dotenv
TELEGRAM_BOT_TOKEN=123456:telegram_token
TELEGRAM_ALLOWED_USER_IDS=111111111,222222222
DEFAULT_FROM=USD
DEFAULT_TO=RUB
```

3. Запустите:

```bash
docker compose up -d --build
```

## Использование

В Telegram:

```text
/from USD
/to RUB
12 345,67
```

Бот ответит конвертацией из `USD` в `RUB`.

`/help` показывает справку, а `/list` возвращает список поддерживаемых популярных валют с кодом, названием и страной.

Поддерживается `RUB` как базовая валюта, а остальные валюты берутся из ежедневных данных ЦБ РФ.
