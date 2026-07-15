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
	"time"
)

var (
	successRE = regexp.MustCompile(`(?i)\bsuccessful (?:1fa|2fa|totp|duo|u2f|webauthn)? ?authentication attempt`)
	failureRE = regexp.MustCompile(`(?i)\bunsuccessful (?:1fa|2fa|totp|duo|u2f|webauthn)? ?authentication attempt`)
	userRE    = regexp.MustCompile(`(?i)\buser[= ]+['"]?([^\s'"]+)`)
	ipRE      = regexp.MustCompile(`(?i)\bremote(?:_| )ip[= ]+['"]?([^\s'"]+)`)
	timeRE    = regexp.MustCompile(`\btime=["']([^"']+)["']`)
)

type config struct {
	token, chatID               string
	logPath                     string
	readExisting, notifySuccess bool
	notifyFailure               bool
	timeout                     time.Duration
}

type loginEvent struct {
	successful bool
	user       string
	remoteIP   string
	timestamp  string
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
		logPath:      env("AUTHELIA_LOG_PATH", "/config/logs/authelia.log"),
		readExisting: boolEnv("READ_EXISTING_LOGS", false), notifySuccess: boolEnv("NOTIFY_SUCCESS", true),
		notifyFailure: boolEnv("NOTIFY_FAILURE", true),
	}
	if c.token == "" || c.chatID == "" {
		return c, fmt.Errorf("TELEGRAM_BOT_TOKEN and TELEGRAM_CHAT_ID are required")
	}
	seconds, err := strconv.ParseFloat(env("TELEGRAM_API_TIMEOUT_SECONDS", "10"), 64)
	if err != nil || seconds <= 0 {
		return c, fmt.Errorf("TELEGRAM_API_TIMEOUT_SECONDS must be a positive number")
	}
	c.timeout = time.Duration(seconds * float64(time.Second))
	return c, nil
}

func env(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
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
	if success == failure {
		return loginEvent{}, false
	}
	e := loginEvent{successful: success, user: field(fields, "user"), remoteIP: field(fields, "remote_ip"), timestamp: field(fields, "time")}
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

func follow(path string, readExisting bool, handle func(string)) {
	var file *os.File
	var lastInode os.FileInfo
	var reader *bufio.Reader
	pending := ""
	for {
		info, err := os.Stat(path)
		if err != nil {
			log.Printf("waiting for log file %s: %v", path, err)
			time.Sleep(2 * time.Second)
			continue
		}
		if file == nil || !os.SameFile(info, lastInode) {
			if file != nil {
				_ = file.Close()
			}
			file, err = os.Open(path)
			if err != nil {
				continue
			}
			lastInode = info
			if !readExisting {
				_, _ = file.Seek(0, io.SeekEnd)
				readExisting = true
			}
			reader = bufio.NewReader(file)
			pending = ""
			log.Printf("watching %s", path)
		}
		line, err := reader.ReadString('\n')
		if err == nil {
			handle(pending + strings.TrimSuffix(line, "\n"))
			pending = ""
		} else if err == io.EOF {
			pending += line
		}
		if err != nil && err != io.EOF {
			log.Printf("reading log: %v", err)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func main() {
	c, err := loadConfig()
	if err != nil {
		log.Fatal(err)
	}
	client := &http.Client{Timeout: c.timeout}
	follow(c.logPath, c.readExisting, func(line string) {
		e, ok := parseEvent(line)
		if !ok || (e.successful && !c.notifySuccess) || (!e.successful && !c.notifyFailure) {
			return
		}
		if err := notify(client, c, e); err != nil {
			log.Printf("sending Telegram notification: %v", err)
			return
		}
		log.Printf("sent %s login notification for user=%s ip=%s", map[bool]string{true: "successful", false: "failed"}[e.successful], e.user, e.remoteIP)
	})
}
