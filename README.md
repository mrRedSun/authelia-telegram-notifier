# Authelia Telegram Login Notifier

A lightweight, dependency-free Go sidecar that tails an Authelia log file and sends a Telegram message for successful and unsuccessful authentication attempts.

## Quick start

1. Create a Telegram bot with [@BotFather](https://t.me/BotFather), then obtain the destination chat ID (for example, by messaging the bot and calling `https://api.telegram.org/bot<TOKEN>/getUpdates`).
2. Copy the environment file and fill in the values:

   ```sh
   cp .env.example .env
   ```

3. Configure Authelia to write JSON logs to a shared file:

   ```yaml
   log:
     level: info
     format: json
     file_path: /config/logs/authelia.log
   ```

4. Start the notifier:

   ```sh
   docker compose up -d --build
   ```

Alternatively, use the published image after replacing `mrredsun` with the GitHub owner if you fork the project:

```sh
docker run -d --restart unless-stopped --env-file .env \
  -v /path/to/authelia:/config:ro \
  ghcr.io/mrredsun/authelia-telegram-notifier:latest
```

The notifier starts at the end of the log by default, so it only reports new authentication attempts. Set `READ_EXISTING_LOGS=true` once if you intentionally want to process historic lines. It supports both Authelia's default logfmt output and JSON logs; JSON is recommended because it preserves fields consistently.

## Docker Compose integration

The included `docker-compose.yml` is a sidecar definition. Add its `authelia-telegram-notifier` service to the same Compose project as Authelia and mount the same configuration directory into both containers. The shared mount must contain `logs/authelia.log`.

Use the named volume shown in the example only if Authelia also uses it. For a bind mount, replace `authelia-config:/config:ro` with your existing Authelia configuration mount, for example `./authelia:/config:ro`.

## Environment variables

| Variable | Required | Description |
| --- | --- | --- |
| `TELEGRAM_BOT_TOKEN` | Yes | Telegram bot token. |
| `TELEGRAM_CHAT_ID` | Yes | Target user, group, or channel chat ID. |
| `AUTHELIA_LOG_PATH` | No | Log path inside the notifier container; default `/config/logs/authelia.log`. |
| `READ_EXISTING_LOGS` | No | Set to `true` to process existing log lines on startup. |
| `NOTIFY_SUCCESS` | No | Set to `false` to suppress success messages. |
| `NOTIFY_FAILURE` | No | Set to `false` to suppress failure messages. |
| `TELEGRAM_API_TIMEOUT_SECONDS` | No | HTTP timeout; default `10`. |

Messages intentionally contain no passwords, tokens, or requested URLs. They include the event outcome, username when logged by Authelia, source IP when available, and timestamp.

## Which events send a message?

Only Authelia authentication events send a notification, such as `Successful 1FA authentication attempt`, `Successful TOTP authentication attempt`, or `Unsuccessful 1FA authentication attempt`. Redirects (`Access ... is not authorized ... status code 302`) and `requires 2FA` lines are normal navigation / intermediate-flow messages, not completed logins, and are deliberately ignored.

## Local run

```sh
go run .
```

## Test

```sh
go test ./...
```
