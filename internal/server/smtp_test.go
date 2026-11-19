package server

import (
	"strings"
	"testing"
)

func crlf(s string) string { return strings.ReplaceAll(s, "\n", "\r\n") }

func TestParseMailLatin1QuotedPrintable(t *testing.T) {
	raw := crlf(`From: =?iso-8859-1?q?Syst=E8me?= <noreply@example.fr>
To: zoe@example.fr
Subject: =?iso-8859-1?q?V=E9rification?=
Content-Type: text/plain; charset=ISO-8859-1
Content-Transfer-Encoding: quoted-printable

Bonjour Zo=EB, votre code est : 384021
`)
	m := parseMail("noreply@example.fr", []string{"zoe@example.fr"}, raw)
	if m.Subject != "Vérification" {
		t.Errorf("subject = %q", m.Subject)
	}
	if got := m.Headers["From"]; got != "Système <noreply@example.fr>" {
		t.Errorf("From header = %q", got)
	}
	if !strings.Contains(m.Text, "Bonjour Zoë") {
		t.Errorf("text = %q", m.Text)
	}
	if len(m.Codes) != 1 || m.Codes[0] != "384021" {
		t.Errorf("codes = %q", m.Codes)
	}
}

func TestParseMailMultipart(t *testing.T) {
	raw := crlf(`From: billing@shop.test
To: dave@example.com
Subject: Invoice
MIME-Version: 1.0
Content-Type: multipart/mixed; boundary="OUT"

--OUT
Content-Type: multipart/related; boundary="REL"

--REL
Content-Type: multipart/alternative; boundary="ALT"

--ALT
Content-Type: text/plain; charset=utf-8

Pay at https://shop.test/pay/42
--ALT
Content-Type: text/html; charset=utf-8
Content-Transfer-Encoding: base64

PHA+PGltZyBzcmM9ImNpZDpsb2dvQHNob3AiPiA8YSBocmVmPSJodHRwczovL3Nob3AudGVzdC9w
YXkvNDIiPlBheSBub3c8L2E+PC9wPg==
--ALT--
--REL
Content-Type: image/png
Content-ID: <logo@shop>
Content-Transfer-Encoding: base64

iVBORw0KGgo=
--REL--
--OUT
Content-Type: application/pdf; name="inv.pdf"
Content-Disposition: attachment; filename="inv.pdf"
Content-Transfer-Encoding: base64

JVBERi0xLjQ=
--OUT--
`)
	m := parseMail("billing@shop.test", []string{"dave@example.com"}, raw)
	if m.Text != "Pay at https://shop.test/pay/42" {
		t.Errorf("text = %q", m.Text)
	}
	if !strings.Contains(m.HTML, `cid:logo@shop`) {
		t.Errorf("html = %q", m.HTML)
	}
	if len(m.Attachments) != 2 {
		t.Fatalf("attachments = %+v", m.Attachments)
	}
	img, pdf := m.Attachments[0], m.Attachments[1]
	if img.ContentID != "logo@shop" || !img.Inline || img.ContentType != "image/png" || img.Size != 8 {
		t.Errorf("inline image = %+v", img)
	}
	if pdf.Filename != "inv.pdf" || pdf.Inline || string(m.parts[1]) != "%PDF-1.4" {
		t.Errorf("pdf = %+v %q", pdf, m.parts[1])
	}
	if len(m.Links) != 1 || m.Links[0] != (Link{URL: "https://shop.test/pay/42", Text: "Pay now"}) {
		t.Errorf("links = %+v", m.Links)
	}
}

func TestParseMailWithoutHeaders(t *testing.T) {
	m := parseMail("a@x", []string{"b@x"}, "just some text with code 555123\r\n")
	if m.Body == "" || m.Text == "" || len(m.Codes) != 1 {
		t.Errorf("body=%q text=%q codes=%q", m.Body, m.Text, m.Codes)
	}
}
