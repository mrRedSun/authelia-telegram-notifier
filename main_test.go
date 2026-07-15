package main

import (
	"strings"
	"testing"
)

func TestParseJSONSuccess(t *testing.T) {
	e, ok := parseEvent(`{"msg":"Successful 1FA authentication attempt by user","user":"alice","remote_ip":"203.0.113.8","time":"2026-07-15T12:00:00Z"}`)
	if !ok || !e.successful || e.user != "alice" || e.remoteIP != "203.0.113.8" {
		t.Fatalf("unexpected event: %#v, ok=%t", e, ok)
	}
}

func TestParseLogfmtFailure(t *testing.T) {
	e, ok := parseEvent(`level=error msg="Unsuccessful TOTP authentication attempt" user=bob remote_ip=198.51.100.5`)
	if !ok || e.successful || e.user != "bob" || e.remoteIP != "198.51.100.5" {
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
