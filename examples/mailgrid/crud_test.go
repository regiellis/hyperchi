package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestEditAndRemoveRecipients(t *testing.T) {
	a := newTestApp(t, nil)
	a.signIn()
	listID := a.seedList("Edit", 2, nil)
	r, err := one[Recipient](a.s.db, `SELECT * FROM recipients WHERE list_id = ? ORDER BY id LIMIT 1`, listID)
	if err != nil {
		t.Fatal(err)
	}
	res, body := a.get(fmt.Sprintf("/recipients/%d/edit", r.ID), true)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, `name="field.company"`) {
		t.Fatalf("editor: %d %s", res.StatusCode, body)
	}
	path := fmt.Sprintf("/recipients/%d", r.ID)
	res, body = a.post(path, url.Values{"email": {"person001@example.com"}, "name": {"X"}, "field.company": {"Y"}})
	if res.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(body, "Another recipient on this list has this email.") {
		t.Fatalf("duplicate email: %d %s", res.StatusCode, body)
	}
	res, body = a.post(path, url.Values{"email": {"nope"}, "name": {"X"}})
	if res.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(body, "Not a valid email address.") {
		t.Fatalf("bad email: %d %s", res.StatusCode, body)
	}
	res, _ = a.post(path, url.Values{"email": {"Renamed@Example.com"}, "name": {"Renamed"}, "field.company": {""}})
	if res.StatusCode != http.StatusOK || res.Header.Get("HX-Trigger") != "recipients-changed" {
		t.Fatalf("save: %d %v", res.StatusCode, res.Header)
	}
	r, err = getRecipient(a.s.db, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if r.Email != "renamed@example.com" || r.Name != "Renamed" || r.FieldsJSON != "{}" {
		t.Fatalf("saved %+v", r)
	}

	req, err := http.NewRequest(http.MethodDelete, a.srv.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("HX-Request", "true")
	req.Header.Set("X-CSRF-Token", a.csrf())
	if res, _ := a.do(req); res.StatusCode != http.StatusOK || res.Header.Get("HX-Trigger") != "recipients-changed" {
		t.Fatalf("delete: %d", res.StatusCode)
	}
	if n := a.count("recipients", "list_id = ?", listID); n != 1 {
		t.Fatalf("%d recipients left", n)
	}
	res, body = a.get(fmt.Sprintf("/lists/%d?q=renamed", listID), true)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "No recipient matches") {
		t.Fatalf("search after delete: %s", body)
	}
}

func TestComposeLetter(t *testing.T) {
	a := newTestApp(t, nil)
	a.signIn()
	res, body := a.post("/letters", url.Values{"title": {"Q3"}, "subject": {"Hi {{name}}"}, "body": {"{{ first name }}"}, "format": {"text"}})
	if res.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(body, "is not a merge field") {
		t.Fatalf("stray tag: %d %s", res.StatusCode, body)
	}
	res, body = a.post("/letters", url.Values{"title": {"Q3"}, "subject": {"Hi {{name}}"}, "body": {"x"}, "format": {"pdf"}})
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("bad format: %d %s", res.StatusCode, body)
	}
	res, _ = a.post("/letters", url.Values{"title": {"Q3"}, "subject": {"Hi {{name}}"}, "body": {"Dear {{name}}"}, "format": {"html"}, "strict": {"true"}})
	if res.StatusCode != http.StatusOK || res.Header.Get("HX-Redirect") != "/letters/1" {
		t.Fatalf("create: %d %v", res.StatusCode, res.Header)
	}
	res, _ = a.post("/letters/1", url.Values{"title": {"Q3 final"}, "subject": {"Hello {{name}}"}, "body": {"Dear {{name}}"}, "format": {"text"}})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("update: %d", res.StatusCode)
	}
	l, err := getLetter(context.Background(), a.s.db, 1)
	if err != nil {
		t.Fatal(err)
	}
	if l.Title != "Q3 final" || l.Format != formatText || l.Strict {
		t.Fatalf("saved %+v", l)
	}
	if n := a.count("audit_log", "action IN ('wrote a letter', 'edited a letter')"); n != 2 {
		t.Fatalf("%d audit rows", n)
	}
}

func TestSendoutNeedsTheOutboxConfirmation(t *testing.T) {
	a := newTestApp(t, nil)
	a.signIn()
	listID := a.seedList("Confirm", 2, nil)
	letterID := a.seedLetter("Hi", "Body", formatText, false)
	form := url.Values{"letter_id": {fmt.Sprint(letterID)}, "list_id": {fmt.Sprint(listID)}}
	res, body := a.post("/sendouts", form)
	if res.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(body, "not emailed") {
		t.Fatalf("without confirmation: %d %s", res.StatusCode, body)
	}
	if n := a.count("sendouts", ""); n != 0 {
		t.Fatalf("%d send-outs created", n)
	}
	res, body = a.get(fmt.Sprintf("/sendouts/new?letter_id=%d&list_id=%d", letterID, listID), true)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "Nothing is emailed.") || !strings.Contains(body, `hc-bind:disabled="!ok"`) {
		t.Fatalf("plan: %s", body)
	}
}
