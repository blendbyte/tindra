package main

import (
	"testing"
	"time"
)

func TestSentryUserMap_full(t *testing.T) {
	u := seedUsers[0]
	m := sentryUserMap(u)
	if m["id"] != u.id || m["username"] != u.username || m["email"] != u.email || m["name"] != u.name {
		t.Fatalf("got %#v", m)
	}
}

func TestSentryUserMap_omitsEmptyEmail(t *testing.T) {
	var u seedUser
	for _, s := range seedUsers {
		if s.email == "" && s.name == "" {
			u = s
			break
		}
	}
	if u.id == "" {
		t.Fatal("expected a seed user without email/name")
	}
	m := sentryUserMap(u)
	if m["id"] != u.id {
		t.Fatalf("id: got %v", m["id"])
	}
	if _, ok := m["email"]; ok {
		t.Fatal("email should be omitted")
	}
	if _, ok := m["name"]; ok {
		t.Fatal("name should be omitted")
	}
}

func TestBuildTransaction_includesUser(t *testing.T) {
	tmpl := txTemplate{name: "GET /api/orders", op: "http.server", durationMs: [2]int{10, 20}, platform: "php"}
	tx := buildTransaction(tmpl, time.Now().UTC(), []string{"1.0.0"}, []string{"production"})
	user, ok := tx["user"].(map[string]any)
	if !ok {
		t.Fatal("expected user on transaction")
	}
	if user["id"] == nil || user["id"] == "" {
		t.Fatalf("user.id missing: %#v", user)
	}
}

func TestBuildLogRecord_includesUserAttrs(t *testing.T) {
	rec := buildLogRecord("info", time.Now().UTC(), []string{"1.0.0"}, []string{"production"})
	attrs, ok := rec["attributes"].(map[string]any)
	if !ok {
		t.Fatal("expected attributes")
	}
	if attrs["user.id"] == nil || attrs["user.id"] == "" {
		t.Fatalf("user.id missing: %#v", attrs)
	}
}
