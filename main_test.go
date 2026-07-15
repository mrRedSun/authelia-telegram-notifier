package main

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestParseJSONSuccess(t *testing.T) {
	e, ok := parseEvent(`{"level":"debug","msg":"Successful 1FA authentication attempt made by user 'alice'","path":"/api/firstfactor","remote_ip":"203.0.113.8","time":"2026-07-15T12:00:00Z"}`)
	if !ok || !e.successful || e.kind != "1FA" || e.user != "alice" || e.remoteIP != "203.0.113.8" {
		t.Fatalf("unexpected event: %#v, ok=%t", e, ok)
	}
}

func TestParseJSONTOTPSuccess(t *testing.T) {
	e, ok := parseEvent(`{"level":"debug","method":"POST","msg":"Successful TOTP authentication attempt made by user 'USERNAME'","path":"/api/secondfactor/totp","remote_ip":"192.0.2.10","time":"2026-07-15T18:10:43+03:00"}`)
	if !ok || !e.successful || e.kind != "TOTP" || e.user != "USERNAME" || e.remoteIP != "192.0.2.10" {
		t.Fatalf("unexpected event: %#v, ok=%t", e, ok)
	}
}

func TestFollowExitsAfterPersistentOpenFailure(t *testing.T) {
	c := config{
		logPath:       t.TempDir() + "/missing.log",
		retryAttempts: 1,
		retryInitial:  time.Millisecond,
		retryMax:      time.Millisecond,
		readyFile:     t.TempDir() + "/ready",
	}
	if err := follow(c, func(string) {}); err == nil {
		t.Fatal("expected persistent watcher failure")
	}
}

func TestFollowExitsAfterUnreadableTarget(t *testing.T) {
	c := config{
		logPath:       t.TempDir(), // A directory cannot be read as a log file.
		retryAttempts: 1,
		retryInitial:  time.Millisecond,
		retryMax:      time.Millisecond,
		readyFile:     t.TempDir() + "/ready",
	}
	if err := follow(c, func(string) {}); err == nil {
		t.Fatal("expected unreadable watcher failure")
	}
}

func TestSuccessCoalescerPrefersTOTP(t *testing.T) {
	sent := make(chan loginEvent, 2)
	coalescer := newSuccessCoalescer(20*time.Millisecond, func(event loginEvent) { sent <- event })
	coalescer.submit(loginEvent{successful: true, kind: "1FA", user: "alice", remoteIP: "192.0.2.10"})
	time.Sleep(2 * time.Millisecond)
	coalescer.submit(loginEvent{successful: true, kind: "TOTP", user: "alice", remoteIP: "192.0.2.10"})
	select {
	case event := <-sent:
		if event.kind != "TOTP" {
			t.Fatalf("sent %s instead of TOTP", event.kind)
		}
	case <-time.After(time.Second):
		t.Fatal("did not send TOTP event")
	}
	select {
	case event := <-sent:
		t.Fatalf("unexpected duplicate event: %s", event.kind)
	case <-time.After(40 * time.Millisecond):
	}
}

func TestParseLogfmtFailure(t *testing.T) {
	e, ok := parseEvent(`level=error msg="Unsuccessful TOTP authentication attempt" user=bob remote_ip=198.51.100.5`)
	if !ok || e.successful || e.user != "bob" || e.remoteIP != "198.51.100.5" {
		t.Fatalf("unexpected event: %#v, ok=%t", e, ok)
	}
}

func TestUnknownUserIsNotExtractedFromError(t *testing.T) {
	e, ok := parseEvent(`{"level":"error","msg":"Unsuccessful 1FA authentication attempt by user ''","error":"user not found","path":"/api/firstfactor","remote_ip":"194.44.117.6","time":"2026-07-15T18:04:43+03:00"}`)
	if !ok || e.successful || e.user != "" {
		t.Fatalf("unexpected event: %#v, ok=%t", e, ok)
	}
}

func TestParsePlainAutheliaLog(t *testing.T) {
	e, ok := parseEvent(`time="2026-07-15T17:40:04+03:00" level=error msg="Unsuccessful 1FA authentication attempt by user 'alice'" method=POST remote_ip=203.0.113.8`)
	if !ok || e.successful || e.user != "alice" || e.remoteIP != "203.0.113.8" || e.timestamp != "2026-07-15T17:40:04+03:00" {
		t.Fatalf("unexpected event: %#v, ok=%t", e, ok)
	}
}

func TestIgnoreOtherLogs(t *testing.T) {
	if _, ok := parseEvent(`{"msg":"Configuration loaded"}`); ok {
		t.Fatal("unexpected event")
	}
}

func TestEscapesTelegramHTML(t *testing.T) {
	m := message(loginEvent{user: "<admin>"})
	if !strings.Contains(m, "&lt;admin&gt;") {
		t.Fatalf("message is not escaped: %s", m)
	}
}

func TestValidateOpenReadsExistingFile(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "authelia.log")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = file.WriteString("event\n"); err != nil {
		t.Fatal(err)
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	opened, reader, err := validateOpen(file.Name(), true)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	line, err := reader.ReadString('\n')
	if err != nil || line != "event\n" {
		t.Fatalf("unexpected read: %q, %v", line, err)
	}
}

func TestHealthcheckRequiresFreshReadiness(t *testing.T) {
	path := t.TempDir() + "/ready"
	if healthcheck(config{readyFile: path, readyMaxAge: time.Second}) != 1 {
		t.Fatal("missing readiness file must be unhealthy")
	}
	if err := markReady(path); err != nil {
		t.Fatal(err)
	}
	if healthcheck(config{readyFile: path, readyMaxAge: time.Second}) != 0 {
		t.Fatal("fresh readiness file must be healthy")
	}
}
