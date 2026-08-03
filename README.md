# Newsmaker

Единый веб-интерфейс для отправки новостей в Telegram, Discord (webhook), MAX и VK.

## Запуск на сервере

```bash
cd /home/taz/newsmaker
cp .env.example .env   # задать пароль и NEWSMAKER_SECRET
docker compose up -d --build
```

UI: `http://<server>:8080`

Логин по умолчанию из `.env`: `NEWSMAKER_USER` / `NEWSMAKER_PASSWORD`.
