# Authelia Telegram Login Notifier

A lightweight, dependency-free Go sidecar that tails an Authelia log file and sends a Telegram message for successful and unsuccessful authentication attempts.

## Install with Docker Compose

1. Create a Telegram bot with [@BotFather](https://t.me/BotFather), then obtain the destination chat ID (for example, by messaging the bot and calling `https://api.telegram.org/bot<TOKEN>/getUpdates`).
2. Create a directory for the notifier and its environment file:

   ```sh
   mkdir authelia-telegram-notifier && cd authelia-telegram-notifier
   curl -fsSLO https://raw.githubusercontent.com/mrRedSun/authelia-telegram-notifier/master/.env.example
   mv .env.example .env
   ```

3. Edit `.env` and set `TELEGRAM_BOT_TOKEN` and `TELEGRAM_CHAT_ID`.
4. Configure Authelia to write JSON logs to a persistent directory shared with the notifier:

   ```yaml
   log:
     level: info
     format: json
     file_path: /config/logs/authelia.log
   ```

5. Create `docker-compose.yml`, replacing `/path/to/authelia/config` with the host directory mounted as `/config` in your Authelia container:

   ```yaml
   services:
     authelia-telegram-notifier:
       image: ghcr.io/mrredsun/authelia-telegram-notifier:latest
       container_name: authelia-telegram-notifier
       restart: unless-stopped
       env_file: .env
       volumes:
         - /path/to/authelia/config:/config:ro
   ```

6. Start it and follow its logs:

   ```sh
   docker compose up -d
   docker compose logs -f
   ```

The notifier starts at the end of the log by default, so it only reports new authentication attempts. Set `READ_EXISTING_LOGS=true` once if you intentionally want to process historic lines. It supports both Authelia's default logfmt output and JSON logs; JSON is recommended because it preserves fields consistently.

### Existing Authelia Compose stack

If Authelia is already managed by Docker Compose, add this service to the same `docker-compose.yml`. Reuse the exact config mount used by Authelia; it must be writable by Authelia and mounted read-only by the notifier.

```yaml
  authelia-telegram-notifier:
    image: ghcr.io/mrredsun/authelia-telegram-notifier:latest
    restart: unless-stopped
    env_file: ./authelia-telegram-notifier.env
    volumes:
      - ./authelia:/config:ro
```

Create `authelia-telegram-notifier.env` beside the Compose file using the variables in [`.env.example`](.env.example), then run `docker compose up -d`.

## Install with an AI agent

Give an AI coding or infrastructure agent this prompt, then provide it access to the host's Compose project:

```text
Install the Authelia Telegram Login Notifier into my existing Docker Compose deployment.

Repository: https://github.com/mrRedSun/authelia-telegram-notifier
Image: ghcr.io/mrredsun/authelia-telegram-notifier:latest

Requirements:
1. Inspect my existing Authelia Compose service and configuration; do not change unrelated services.
2. Configure Authelia JSON logging to a persistent file under its existing /config mount: /config/logs/authelia.log.
3. Add an authelia-telegram-notifier sidecar which mounts that exact config directory read-only at /config.
4. Create a protected environment file for TELEGRAM_BOT_TOKEN and TELEGRAM_CHAT_ID. Do not print either secret.
5. Start or update only the relevant Compose services.
6. Verify the notifier can read the log file and is running. Do not send a test message unless I explicitly request it.
7. Report every file changed and the exact commands used.
```

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
