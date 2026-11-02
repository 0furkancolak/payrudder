package mailx

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"strings"
	"testing"
	"time"
)

var pdf = append([]byte("%PDF-1.3\n"), bytes.Repeat([]byte{0, 1, 2, 250, 251, 252}, 400)...)

// The message as SMTP carries it: the text and the HTML as alternatives and, when there are files, those two inside a multipart/mixed with one base64 part per file.
func TestSMTPCarriesAttachmentsInAMixedMessage(t *testing.T) {
	m := Message{From: "Acme <billing@acme.test>", To: "ada@example.com", Subject: "Your copy", Text: "plain text", HTML: "<p>html</p>",
		Attachments: []Attachment{{Filename: "agreement-ü.pdf", ContentType: "application/pdf", Data: pdf}, {Filename: "second.txt", Data: []byte("two")}}}
	raw := mime822(m, strings.Repeat("ab", 16), time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC))
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	typ, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil || typ != "multipart/mixed" {
		t.Fatalf("top level: %q %v", typ, err)
	}
	mr := multipart.NewReader(msg.Body, params["boundary"])
	first, err := mr.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	altType, altParams, _ := mime.ParseMediaType(first.Header.Get("Content-Type"))
	if altType != "multipart/alternative" {
		t.Fatalf("first part: %q", altType)
	}
	alt := multipart.NewReader(first, altParams["boundary"])
	var texts []string
	for {
		p, err := alt.NextPart()
		if err != nil {
			break
		}
		b, _ := io.ReadAll(quotedprintable.NewReader(p))
		texts = append(texts, string(b))
	}
	if len(texts) != 2 || !strings.Contains(texts[0], "plain text") || !strings.Contains(texts[1], "<p>html</p>") {
		t.Errorf("the alternatives: %q", texts)
	}
	var files []struct {
		name, typ string
		data      []byte
	}
	for {
		p, err := mr.NextPart()
		if err != nil {
			break
		}
		_, dp, _ := mime.ParseMediaType(p.Header.Get("Content-Disposition"))
		ft, _, _ := mime.ParseMediaType(p.Header.Get("Content-Type"))
		b, _ := io.ReadAll(base64.NewDecoder(base64.StdEncoding, p))
		files = append(files, struct {
			name, typ string
			data      []byte
		}{dp["filename"], ft, b})
	}
	if len(files) != 2 || files[0].name != "agreement-ü.pdf" || files[0].typ != "application/pdf" || !bytes.Equal(files[0].data, pdf) || files[1].typ != "application/octet-stream" || string(files[1].data) != "two" {
		t.Errorf("the files: %+v", files)
	}
	// Base64 lines are at most 76 characters.
	for _, line := range strings.Split(string(raw), "\r\n") {
		if len(line) > 998 {
			t.Fatalf("a line of %d characters", len(line))
		}
	}
}

func TestAFileNameCannotBreakOutOfItsHeader(t *testing.T) {
	m := Message{From: "a@acme.test", To: "b@example.com", Subject: "s", Text: "t", HTML: "h",
		Attachments: []Attachment{{Filename: "x.pdf\r\nBcc: spy@example.com\r\n\r\ninjected", ContentType: "application/pdf", Data: []byte("d")}}}
	raw := mime822(m, strings.Repeat("cd", 16), time.Now())
	if strings.Contains(string(raw), "\r\nBcc:") {
		t.Fatalf("a header was injected:\n%s", raw)
	}
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil || msg.Header.Get("Bcc") != "" {
		t.Fatalf("parsed: %v %v", err, msg.Header)
	}
}

// Without files the message is what it always was: multipart/alternative at the top.
func TestAMessageWithoutFilesIsNotMixed(t *testing.T) {
	raw := mime822(Message{From: "a@acme.test", To: "b@example.com", Subject: "s", Text: "t", HTML: "h"}, strings.Repeat("ef", 16), time.Now())
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if typ, _, _ := mime.ParseMediaType(msg.Header.Get("Content-Type")); typ != "multipart/alternative" {
		t.Errorf("content type %q", typ)
	}
}

func TestResendSendsAttachmentsAsBase64(t *testing.T) {
	var body resendBody
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = w.Write([]byte(`{"id":"em_1"}`))
	}))
	defer srv.Close()
	_, err := Resend{BaseURL: srv.URL, APIKey: "k"}.Send(context.Background(), Message{From: "a@acme.test", To: "b@example.com", Subject: "s", Text: "t",
		Attachments: []Attachment{{Filename: "agreement.pdf", ContentType: "application/pdf", Data: pdf}}})
	if err != nil || len(body.Attachments) != 1 || body.Attachments[0].Filename != "agreement.pdf" {
		t.Fatalf("send: %v %+v", err, body.Attachments)
	}
	if got, err := base64.StdEncoding.DecodeString(body.Attachments[0].Content); err != nil || !bytes.Equal(got, pdf) {
		t.Errorf("the content is not the file: %v", err)
	}
	// None: no key at all in the JSON.
	var raw map[string]any
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&raw)
		_, _ = w.Write([]byte(`{"id":"em_2"}`))
	}))
	defer srv2.Close()
	if _, err := (Resend{BaseURL: srv2.URL, APIKey: "k"}).Send(context.Background(), Message{From: "a@acme.test", To: "b@example.com", Subject: "s", Text: "t"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["attachments"]; ok {
		t.Error("a message without files names `attachments`")
	}
}
