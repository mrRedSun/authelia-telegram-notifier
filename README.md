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
     level: debug
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

### Required Authelia log level

Successful 1FA and TOTP events are emitted by Authelia at `debug` severity. Failures are emitted at `error`, which is why an `info` configuration shows failed attempts but not successful ones. To receive both notification types, use `level: debug` as shown above. At `info`, this notifier can reliably notify only failed attempts; inferring success from authorization or session events would be unreliable.

The parser recognizes these structured success signals:

- 1FA: `level=debug`, `path=/api/firstfactor`, and `Successful 1FA authentication attempt made by user '...'`.
- TOTP: `level=debug`, `path=/api/secondfactor/totp`, and `Successful TOTP authentication attempt made by user '...'`.

The TOTP event is the strongest indication that a two-factor login completed. A two-factor login emits both 1FA and TOTP success lines; the notifier holds the 1FA notification for five seconds and, if the matching TOTP event arrives, sends only the TOTP notification. One-factor logins still send their 1FA notification after that short window. Set `SUCCESS_COALESCE_WINDOW_SECONDS=0` to disable this behavior. The notifier logs `detected successful authentication event (TOTP)` before sending its Telegram request, making it easy to distinguish parsing issues from Telegram delivery failures.

### Log-file permissions and health

Authelia commonly creates its file log as `root:root` with mode `0600`. The published image runs as root by default specifically so it can read that file through its read-only mount. It never writes to the Authelia configuration directory.

If your security policy requires a non-root notifier, use one of these options:

- Change the host log file ownership or group and mode to grant the notifier read access.
- Start the container with `user: "<uid>:<gid>"`, provided that identity can read the log file.
- Keep the container initially root and set `PUID` and `PGID` to drop privileges after startup. Those IDs must have read access to the mounted log.

The watcher logs every failed `stat`, open, or read operation with the file path, retries with bounded exponential backoff, and exits non-zero after ten consecutive failures by default. Docker then restarts it. Its Docker health check is healthy only after the log has been opened and read successfully; it becomes unhealthy if that watcher state is lost.

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
2. Configure Authelia JSON debug logging to a persistent file under its existing /config mount: /config/logs/authelia.log.
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
| `LOG_PATH` | No | Preferred log path inside the notifier container; default `/config/logs/authelia.log`. |
| `AUTHELIA_LOG_PATH` | No | Legacy alias for `LOG_PATH`; used only when `LOG_PATH` is unset. |
| `READ_EXISTING_LOGS` | No | Set to `true` to process existing log lines on startup. |
| `NOTIFY_SUCCESS` | No | Set to `false` to suppress success messages. |
| `NOTIFY_FAILURE` | No | Set to `false` to suppress failure messages. |
| `SUCCESS_COALESCE_WINDOW_SECONDS` | No | Delay 1FA successes while waiting for a matching TOTP success; default `5`, or `0` to disable. |
| `TELEGRAM_API_TIMEOUT_SECONDS` | No | HTTP timeout; default `10`. |
| `LOG_RETRY_MAX_ATTEMPTS` | No | Consecutive stat/open/read failures before exit; default `10`. |
| `LOG_RETRY_INITIAL_SECONDS` | No | First retry delay; default `1`. |
| `LOG_RETRY_MAX_SECONDS` | No | Maximum retry delay; default `30`. |
| `PUID` / `PGID` | No | Optional UID/GID to use after startup; requires an initially-root container. |

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
