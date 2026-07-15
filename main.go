package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var (
	successRE            = regexp.MustCompile(`(?i)\bsuccessful (?:1fa|2fa|totp|duo|u2f|webauthn)? ?authentication attempt`)
	firstFactorSuccessRE = regexp.MustCompile(`(?i)^successful 1fa authentication attempt made by user '([^']+)'$`)
	totpSuccessRE        = regexp.MustCompile(`(?i)^successful totp authentication attempt made by user '([^']+)'$`)
	failureRE            = regexp.MustCompile(`(?i)\bunsuccessful (?:1fa|2fa|totp|duo|u2f|webauthn)? ?authentication attempt`)
	userRE               = regexp.MustCompile(`(?i)\buser[= ]+['"]?([^\s'"]+)`)
	ipRE                 = regexp.MustCompile(`(?i)\bremote(?:_| )ip[= ]+['"]?([^\s'"]+)`)
	timeRE               = regexp.MustCompile(`\btime=["']([^"']+)["']`)
)

type config struct {
	token, chatID               string
	logPath                     string
	readExisting, notifySuccess bool
	notifyFailure               bool
	timeout                     time.Duration
	retryInitial, retryMax      time.Duration
	retryAttempts               int
	readyFile                   string
	readyMaxAge                 time.Duration
	uid, gid                    *int
}

type loginEvent struct {
	successful bool
	user       string
	remoteIP   string
	timestamp  string
	kind       string
}

func boolEnv(name string, fallback bool) bool {
	v, ok := os.LookupEnv(name)
	if !ok {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	return err == nil && b
}

func loadConfig() (config, error) {
	c := config{
		token: os.Getenv("TELEGRAM_BOT_TOKEN"), chatID: os.Getenv("TELEGRAM_CHAT_ID"),
		logPath:      envFirst("LOG_PATH", "AUTHELIA_LOG_PATH", "/config/logs/authelia.log"),
		readExisting: boolEnv("READ_EXISTING_LOGS", false), notifySuccess: boolEnv("NOTIFY_SUCCESS", true),
		notifyFailure: boolEnv("NOTIFY_FAILURE", true),
		readyFile:     env("READINESS_FILE", "/tmp/authelia-telegram-notifier.ready"),
	}
	if c.token == "" || c.chatID == "" {
		return c, fmt.Errorf("TELEGRAM_BOT_TOKEN and TELEGRAM_CHAT_ID are required")
	}
	seconds, err := strconv.ParseFloat(env("TELEGRAM_API_TIMEOUT_SECONDS", "10"), 64)
	if err != nil || seconds <= 0 {
		return c, fmt.Errorf("TELEGRAM_API_TIMEOUT_SECONDS must be a positive number")
	}
	c.timeout = time.Duration(seconds * float64(time.Second))
	if c.retryAttempts, err = intEnv("LOG_RETRY_MAX_ATTEMPTS", 10); err != nil || c.retryAttempts < 1 {
		return c, fmt.Errorf("LOG_RETRY_MAX_ATTEMPTS must be a positive integer")
	}
	if c.retryInitial, err = durationSecondsEnv("LOG_RETRY_INITIAL_SECONDS", 1); err != nil || c.retryInitial <= 0 {
		return c, fmt.Errorf("LOG_RETRY_INITIAL_SECONDS must be a positive number")
	}
	if c.retryMax, err = durationSecondsEnv("LOG_RETRY_MAX_SECONDS", 30); err != nil || c.retryMax < c.retryInitial {
		return c, fmt.Errorf("LOG_RETRY_MAX_SECONDS must be at least LOG_RETRY_INITIAL_SECONDS")
	}
	if c.readyMaxAge, err = durationSecondsEnv("HEALTHCHECK_MAX_AGE_SECONDS", 90); err != nil || c.readyMaxAge <= 0 {
		return c, fmt.Errorf("HEALTHCHECK_MAX_AGE_SECONDS must be a positive number")
	}
	if c.uid, err = optionalIntEnv("PUID"); err != nil {
		return c, err
	}
	if c.gid, err = optionalIntEnv("PGID"); err != nil {
		return c, err
	}
	return c, nil
}

func env(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

func envFirst(primary, secondary, fallback string) string {
	if v := os.Getenv(primary); v != "" {
		return v
	}
	return env(secondary, fallback)
}

func intEnv(name string, fallback int) (int, error) {
	if v := os.Getenv(name); v != "" {
		return strconv.Atoi(v)
	}
	return fallback, nil
}

func optionalIntEnv(name string) (*int, error) {
	if v := os.Getenv(name); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return nil, fmt.Errorf("%s must be a non-negative integer", name)
		}
		return &n, nil
	}
	return nil, nil
}

func durationSecondsEnv(name string, fallback float64) (time.Duration, error) {
	v, err := strconv.ParseFloat(env(name, strconv.FormatFloat(fallback, 'f', -1, 64)), 64)
	if err != nil {
		return 0, err
	}
	return time.Duration(v * float64(time.Second)), nil
}

func applyIdentity(c config) error {
	if c.uid == nil && c.gid == nil {
		return nil
	}
	if os.Geteuid() != 0 {
		return fmt.Errorf("PUID/PGID require the container to start as root")
	}
	if c.gid != nil && *c.gid != os.Getegid() {
		if err := syscall.Setgid(*c.gid); err != nil {
			return fmt.Errorf("set PGID: %w", err)
		}
	}
	if c.uid != nil && *c.uid != os.Geteuid() {
		if err := syscall.Setuid(*c.uid); err != nil {
			return fmt.Errorf("set PUID: %w", err)
		}
	}
	return nil
}

func parseEvent(line string) (loginEvent, bool) {
	var fields map[string]any
	_ = json.Unmarshal([]byte(line), &fields)
	message := line
	if v, ok := fields["msg"].(string); ok {
		message = v
	} else if v, ok := fields["message"].(string); ok {
		message = v
	}
	success, failure := successRE.MatchString(message), failureRE.MatchString(message)
	level, path := field(fields, "level"), field(fields, "path")
	kind := ""
	if match := firstFactorSuccessRE.FindStringSubmatch(message); len(match) == 2 && level == "debug" && path == "/api/firstfactor" {
		success, kind = true, "1FA"
		if field(fields, "user") == "" {
			fields["user"] = match[1]
		}
	}
	if match := totpSuccessRE.FindStringSubmatch(message); len(match) == 2 && level == "debug" && path == "/api/secondfactor/totp" {
		success, kind = true, "TOTP"
		if field(fields, "user") == "" {
			fields["user"] = match[1]
		}
	}
	if success == failure {
		return loginEvent{}, false
	}
	e := loginEvent{successful: success, user: field(fields, "user"), remoteIP: field(fields, "remote_ip"), timestamp: field(fields, "time"), kind: kind}
	if e.timestamp == "" {
		e.timestamp = field(fields, "timestamp")
	}
	if e.timestamp == "" {
		e.timestamp = capture(timeRE, line)
	}
	if e.user == "" {
		e.user = capture(userRE, line)
	}
	if e.remoteIP == "" {
		e.remoteIP = capture(ipRE, line)
	}
	return e, true
}

func field(fields map[string]any, key string) string {
	if v, ok := fields[key]; ok && v != nil {
		return fmt.Sprint(v)
	}
	return ""
}
func capture(re *regexp.Regexp, text string) string {
	m := re.FindStringSubmatch(text)
	if len(m) == 2 {
		return m[1]
	}
	return ""
}

func message(e loginEvent) string {
	title := "🚨 Authelia login failed"
	if e.successful {
		title = "✅ Authelia login successful"
	}
	parts := []string{title}
	for _, f := range []struct{ label, value string }{{"User", e.user}, {"Source IP", e.remoteIP}, {"Time", e.timestamp}} {
		if f.value != "" {
			parts = append(parts, "<b>"+f.label+":</b> "+html.EscapeString(f.value))
		}
	}
	return strings.Join(parts, "\n")
}

func notify(client *http.Client, c config, event loginEvent) error {
	form := url.Values{"chat_id": {c.chatID}, "text": {message(event)}, "parse_mode": {"HTML"}}
	req, err := http.NewRequest(http.MethodPost, "https://api.telegram.org/bot"+c.token+"/sendMessage", bytes.NewBufferString(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("Telegram API returned %s: %s", resp.Status, body)
	}
	var result struct {
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal(body, &result); err != nil || !result.OK {
		return fmt.Errorf("Telegram rejected notification: %s", body)
	}
	return nil
}

func validateOpen(path string, readExisting bool) (*os.File, *bufio.Reader, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	if !readExisting {
		if _, err = file.Seek(0, io.SeekEnd); err != nil {
			_ = file.Close()
			return nil, nil, err
		}
	}
	var probe [1]byte
	if _, err = file.Read(probe[:]); err != nil && err != io.EOF {
		_ = file.Close()
		return nil, nil, err
	}
	if !readExisting {
		_, err = file.Seek(0, io.SeekEnd)
	} else {
		_, err = file.Seek(0, io.SeekStart)
	}
	if err != nil {
		_ = file.Close()
		return nil, nil, err
	}
	return file, bufio.NewReader(file), nil
}

func markReady(path string) error {
	return os.WriteFile(path, []byte{}, 0600)
}

func clearReady(path string) { _ = os.Remove(path) }

func waitRetry(path, operation string, err error, attempt, maximum int, delay time.Duration) error {
	log.Printf("cannot %s log file %s (attempt %d/%d): %v", operation, path, attempt, maximum, err)
	if attempt >= maximum {
		return fmt.Errorf("log watcher unavailable after %d failed attempts: %w", attempt, err)
	}
	time.Sleep(delay)
	return nil
}

func nextDelay(current, maximum time.Duration) time.Duration {
	if current >= maximum/2 {
		return maximum
	}
	return current * 2
}

func follow(c config, handle func(string)) error {
	var file *os.File
	var lastInode os.FileInfo
	var reader *bufio.Reader
	pending := ""
	attempts := 0
	delay := c.retryInitial
	lastReady := time.Time{}
	defer clearReady(c.readyFile)
	for {
		info, err := os.Stat(c.logPath)
		if err != nil {
			attempts++
			if err := waitRetry(c.logPath, "stat", err, attempts, c.retryAttempts, delay); err != nil {
				return err
			}
			delay = nextDelay(delay, c.retryMax)
			continue
		}
		if file == nil || !os.SameFile(info, lastInode) {
			if file != nil {
				_ = file.Close()
				clearReady(c.readyFile)
			}
			file, reader, err = validateOpen(c.logPath, c.readExisting)
			if err != nil {
				file = nil
				reader = nil
				attempts++
				if err := waitRetry(c.logPath, "open/read", err, attempts, c.retryAttempts, delay); err != nil {
					return err
				}
				delay = nextDelay(delay, c.retryMax)
				continue
			}
			lastInode = info
			c.readExisting = true
			pending = ""
			attempts = 0
			delay = c.retryInitial
			if err := markReady(c.readyFile); err != nil {
				return fmt.Errorf("mark watcher ready: %w", err)
			}
			lastReady = time.Now()
			log.Printf("watching %s", c.logPath)
		}
		if time.Since(lastReady) >= 10*time.Second {
			if err := markReady(c.readyFile); err != nil {
				return fmt.Errorf("refresh watcher readiness: %w", err)
			}
			lastReady = time.Now()
		}
		line, err := reader.ReadString('\n')
		if err == nil {
			handle(pending + strings.TrimSuffix(line, "\n"))
			pending = ""
		} else if err == io.EOF {
			pending += line
		}
		if err != nil && err != io.EOF {
			_ = file.Close()
			file = nil
			reader = nil
			clearReady(c.readyFile)
			attempts++
			if retryErr := waitRetry(c.logPath, "read", err, attempts, c.retryAttempts, delay); retryErr != nil {
				return retryErr
			}
			delay = nextDelay(delay, c.retryMax)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func healthcheck(c config) int {
	info, err := os.Stat(c.readyFile)
	if err != nil || time.Since(info.ModTime()) > c.readyMaxAge {
		return 1
	}
	return 0
}

func main() {
	if len(os.Args) == 2 && os.Args[1] == "healthcheck" {
		os.Exit(healthcheck(config{
			readyFile:   env("READINESS_FILE", "/tmp/authelia-telegram-notifier.ready"),
			readyMaxAge: mustDurationSecondsEnv("HEALTHCHECK_MAX_AGE_SECONDS", 90),
		}))
	}
	c, err := loadConfig()
	if err != nil {
		log.Fatal(err)
	}
	if err := applyIdentity(c); err != nil {
		log.Fatal(err)
	}
	log.Printf("successful-login notifications require Authelia log.level=debug; info-level Authelia logs only reliably contain failed attempts")
	client := &http.Client{Timeout: c.timeout}
	if err := follow(c, func(line string) {
		e, ok := parseEvent(line)
		if !ok || (e.successful && !c.notifySuccess) || (!e.successful && !c.notifyFailure) {
			return
		}
		log.Printf("detected %s authentication event%s for user=%s ip=%s", map[bool]string{true: "successful", false: "failed"}[e.successful], eventKindSuffix(e.kind), e.user, e.remoteIP)
		if err := notify(client, c, e); err != nil {
			log.Printf("sending Telegram notification: %v", err)
			return
		}
		log.Printf("sent %s login notification for user=%s ip=%s", map[bool]string{true: "successful", false: "failed"}[e.successful], e.user, e.remoteIP)
	}); err != nil {
		log.Fatal(err)
	}
}

func mustDurationSecondsEnv(name string, fallback float64) time.Duration {
	duration, err := durationSecondsEnv(name, fallback)
	if err != nil || duration <= 0 {
		return time.Duration(fallback * float64(time.Second))
	}
	return duration
}

func eventKindSuffix(kind string) string {
	if kind == "" {
		return ""
	}
	return " (" + kind + ")"
}
