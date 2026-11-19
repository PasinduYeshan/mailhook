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
	"net/textproto"
	"strings"
	"time"
)

func runSMTP(ctx context.Context, ln net.Listener, st *store) error {
	go func() { <-ctx.Done(); ln.Close() }()
	log.Printf("smtp  listening on %s", ln.Addr())
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
	if err != nil || len(msg.Header) == 0 {
		m.Body = raw
		m.Text = raw
		enrichMail(&m)
		return m
	}
	for k, v := range msg.Header {
		for i := range v {
			v[i] = decodeMIMEHeader(v[i])
		}
		m.Headers[k] = strings.Join(v, ", ")
	}
	m.Subject = m.Headers["Subject"]

	mediaType, params, _ := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	part := mimePart{
		header:    textproto.MIMEHeader(msg.Header),
		mediaType: mediaType,
		params:    params,
	}
	collectMIMEParts(msg.Body, part, &m)

	switch {
	case m.Text != "":
		m.Body = m.Text
	case m.HTML != "":
		m.Body = m.HTML
	}
	enrichMail(&m)
	return m
}

type mimePart struct {
	header    textproto.MIMEHeader
	mediaType string
	params    map[string]string
}

// collectMIMEParts walks one part, recursing into multipart trees. The first
// text/plain and text/html bodies fill m.Text / m.HTML; every other leaf is
// kept as an attachment.
func collectMIMEParts(body io.Reader, p mimePart, m *Mail) {
	if strings.HasPrefix(p.mediaType, "multipart/") {
		boundary := p.params["boundary"]
		if boundary == "" {
			return
		}
		mr := multipart.NewReader(body, boundary)
		for {
			sub, err := mr.NextPart()
			if err != nil {
				return
			}
			mt, params, _ := mime.ParseMediaType(sub.Header.Get("Content-Type"))
			collectMIMEParts(sub, mimePart{header: sub.Header, mediaType: mt, params: params}, m)
		}
	}

	raw, _ := io.ReadAll(body)
	data := decodeTransfer(p.header.Get("Content-Transfer-Encoding"), raw)
	disp, dparams, _ := mime.ParseMediaType(p.header.Get("Content-Disposition"))
	filename := dparams["filename"]
	if filename == "" {
		filename = p.params["name"]
	}
	filename = decodeMIMEHeader(filename)
	isBody := disp != "attachment" && filename == ""

	switch {
	case isBody && (p.mediaType == "text/html") && m.HTML == "":
		m.HTML = textOf(data, p.params)
		return
	case isBody && (p.mediaType == "text/plain" || p.mediaType == "") && m.Text == "":
		m.Text = textOf(data, p.params)
		return
	}
	ct := p.mediaType
	if ct == "" {
		ct = "text/plain"
	}
	m.Attachments = append(m.Attachments, Attachment{
		Index:       len(m.parts),
		Filename:    filename,
		ContentType: ct,
		Size:        len(data),
		ContentID:   strings.Trim(p.header.Get("Content-Id"), "<> "),
		Inline:      disp == "inline" || (disp == "" && p.header.Get("Content-Id") != ""),
	})
	m.parts = append(m.parts, data)
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
		if decoded, err := base64.RawStdEncoding.DecodeString(strings.TrimRight(cleaned, "=")); err == nil {
			return decoded
		}
	}
	return body
}

var wordDecoder = &mime.WordDecoder{
	CharsetReader: func(cs string, in io.Reader) (io.Reader, error) {
		if !knownCharset(cs) {
			return nil, fmt.Errorf("unsupported charset %q", cs)
		}
		b, err := io.ReadAll(in)
		if err != nil {
			return nil, err
		}
		return strings.NewReader(toUTF8(b, cs)), nil
	},
}

func decodeMIMEHeader(s string) string {
	if !strings.Contains(s, "=?") {
		return s
	}
	if out, err := wordDecoder.DecodeHeader(s); err == nil {
		return out
	}
	return s
}
