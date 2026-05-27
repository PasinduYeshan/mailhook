package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"strings"
	"time"
)

func runSMTP(ctx context.Context, addr string, st *store) error {
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("smtp listen: %w", err)
	}
	go func() { <-ctx.Done(); ln.Close() }()
	log.Printf("smtp  listening on %s", addr)
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			continue
		}
		go handleSMTP(conn, st)
	}
}

func handleSMTP(c net.Conn, st *store) {
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Minute))
	br := bufio.NewReader(c)
	bw := bufio.NewWriter(c)

	send := func(line string) {
		bw.WriteString(line)
		bw.WriteString("\r\n")
		bw.Flush()
	}

	send("220 mailhook SMTP ready")

	var from string
	var rcpts []string

	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		upper := strings.ToUpper(line)

		switch {
		case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
			send("250 OK")
		case strings.HasPrefix(upper, "MAIL FROM:"):
			from = extractAddr(line[10:])
			send("250 OK")
		case strings.HasPrefix(upper, "RCPT TO:"):
			rcpts = append(rcpts, extractAddr(line[8:]))
			send("250 OK")
		case upper == "DATA":
			send("354 End data with <CR><LF>.<CR><LF>")
			raw, err := readSMTPData(br)
			if err != nil {
				return
			}
			m := parseMail(from, rcpts, raw)
			st.addMail(m)
			send("250 OK queued " + m.ID)
			from = ""
			rcpts = nil
		case upper == "RSET":
			from = ""
			rcpts = nil
			send("250 OK")
		case upper == "NOOP":
			send("250 OK")
		case upper == "QUIT":
			send("221 Bye")
			return
		default:
			// Accept and ignore unknown verbs — sink, not real MTA.
			send("250 OK")
		}
	}
}

func extractAddr(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "<"); i >= 0 {
		if j := strings.Index(s[i:], ">"); j > 0 {
			return s[i+1 : i+j]
		}
	}
	return s
}

func readSMTPData(br *bufio.Reader) (string, error) {
	var buf bytes.Buffer
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return "", err
		}
		if line == ".\r\n" || line == ".\n" {
			break
		}
		// RFC 5321 dot-stuffing: leading "." escapes ".".
		if strings.HasPrefix(line, "..") {
			line = line[1:]
		}
		buf.WriteString(line)
	}
	return buf.String(), nil
}

// ---------------------------------------------------------------------------
// MIME parsing
// ---------------------------------------------------------------------------

func parseMail(from string, to []string, raw string) Mail {
	m := Mail{
		ID:      newID(),
		Time:    time.Now().UTC(),
		From:    from,
		To:      to,
		Raw:     raw,
		Headers: map[string]string{},
	}
	msg, err := mail.ReadMessage(strings.NewReader(raw))
	if err != nil {
		m.Body = raw
		return m
	}
	for k, v := range msg.Header {
		m.Headers[k] = strings.Join(v, ", ")
	}
	m.Subject = decodeMIMEHeader(msg.Header.Get("Subject"))

	ct := msg.Header.Get("Content-Type")
	mediaType, params, _ := mime.ParseMediaType(ct)
	collectMIMEParts(msg.Body, mediaType, params, msg.Header.Get("Content-Transfer-Encoding"), &m)

	switch {
	case m.Text != "":
		m.Body = m.Text
	case m.HTML != "":
		m.Body = m.HTML
	}
	if m.Body == "" {
		body, _ := io.ReadAll(msg.Body)
		m.Body = string(body)
	}
	return m
}

// collectMIMEParts walks one part (recursing into multipart trees) and fills
// m.Text / m.HTML with the first matching part of each type.
func collectMIMEParts(body io.Reader, mediaType string, params map[string]string, transferEnc string, m *Mail) {
	if strings.HasPrefix(mediaType, "multipart/") {
		boundary := params["boundary"]
		if boundary == "" {
			return
		}
		mr := multipart.NewReader(body, boundary)
		for {
			p, err := mr.NextPart()
			if err != nil {
				return
			}
			pCT := p.Header.Get("Content-Type")
			pMediaType, pParams, _ := mime.ParseMediaType(pCT)
			pEnc := p.Header.Get("Content-Transfer-Encoding")
			if strings.HasPrefix(pMediaType, "multipart/") {
				collectMIMEParts(p, pMediaType, pParams, pEnc, m)
				continue
			}
			raw, _ := io.ReadAll(p)
			decoded := decodeTransfer(pEnc, raw)
			switch {
			case strings.HasPrefix(pMediaType, "text/html") && m.HTML == "":
				m.HTML = string(decoded)
			case strings.HasPrefix(pMediaType, "text/plain") && m.Text == "":
				m.Text = string(decoded)
			}
		}
	}
	// Single-part body.
	raw, _ := io.ReadAll(body)
	decoded := decodeTransfer(transferEnc, raw)
	text := string(decoded)
	switch {
	case strings.HasPrefix(mediaType, "text/html"):
		m.HTML = text
	case strings.HasPrefix(mediaType, "text/plain"), mediaType == "":
		m.Text = text
	}
}

func decodeTransfer(enc string, body []byte) []byte {
	switch strings.ToLower(strings.TrimSpace(enc)) {
	case "quoted-printable":
		if decoded, err := io.ReadAll(quotedprintable.NewReader(bytes.NewReader(body))); err == nil {
			return decoded
		}
	case "base64":
		cleaned := strings.Map(func(r rune) rune {
			if r == '\n' || r == '\r' || r == ' ' || r == '\t' {
				return -1
			}
			return r
		}, string(body))
		if decoded, err := base64.StdEncoding.DecodeString(cleaned); err == nil {
			return decoded
		}
	}
	return body
}

func decodeMIMEHeader(s string) string {
	dec := new(mime.WordDecoder)
	if out, err := dec.DecodeHeader(s); err == nil {
		return out
	}
	return s
}
