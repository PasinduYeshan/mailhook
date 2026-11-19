package server

import (
	"bytes"
	"encoding/json"
	"html"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// Link is a URL found in a capture; Text is the anchor text, if any.
type Link struct {
	URL  string `json:"url"`
	Text string `json:"text,omitempty"`
}

// SMS is the message a webhook appears to carry.
type SMS struct {
	To   string `json:"to,omitempty"`
	From string `json:"from,omitempty"`
	Text string `json:"text"`
}

const maxLinks = 50

var (
	reDrop    = regexp.MustCompile(`(?is)<!--.*?-->|<(?:script|style|head|title)\b[^>]*>.*?</(?:script|style|head|title)\s*>`)
	reBlock   = regexp.MustCompile(`(?i)<(?:br|/?p|/?div|/?tr|/?h[1-6]|/?li|/?table|/?blockquote)\b[^>]*>`)
	reTag     = regexp.MustCompile(`(?s)<[^>]*>`)
	reAnchor  = regexp.MustCompile(`(?is)<a\b([^>]*)>(.*?)</a\s*>`)
	reHref    = regexp.MustCompile(`(?is)\bhref\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'>]+))`)
	reURL     = regexp.MustCompile(`(?i)\bhttps?://[^\s<>"'` + "`" + `]+`)
	reHSpace  = regexp.MustCompile(`[ \t\f\v\r\x{00a0}]+`)
	reCodeKw  = regexp.MustCompile(`(?i)\b(?:code|otp|pin|passcode|password|one[- ]?time|verification|verify|token)\D{0,40}?\b([0-9]{3}[- ][0-9]{3}|[0-9]{4,10})\b`)
	reCodeSix = regexp.MustCompile(`\b([0-9]{6})\b`)
	reXMLText = regexp.MustCompile(`(?is)<(?:[\w.-]+:)?([\w.-]+)\b[^>]*>([^<]*)</`)
	reFormish = regexp.MustCompile(`^[^=&\s]+=[^&\s]*(?:&[^=&\s]+=[^&\s]*)*$`)
)

// htmlToText reduces an HTML document to readable plain text.
func htmlToText(s string) string {
	s = reDrop.ReplaceAllString(s, " ")
	s = reBlock.ReplaceAllString(s, "\n")
	s = reTag.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	var out []string
	blank := false
	for _, ln := range strings.Split(s, "\n") {
		ln = strings.TrimSpace(reHSpace.ReplaceAllString(ln, " "))
		if ln == "" {
			blank = len(out) > 0
			continue
		}
		if blank {
			out = append(out, "")
			blank = false
		}
		out = append(out, ln)
	}
	return strings.Join(out, "\n")
}

func collapse(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// trimURL drops punctuation that usually ends a sentence, not a URL.
func trimURL(u string) string {
	for {
		t := strings.TrimRight(u, ".,;:!?'\"")
		if strings.HasSuffix(t, ")") && !strings.Contains(t, "(") {
			t = t[:len(t)-1]
		}
		if strings.HasSuffix(t, "]") && !strings.Contains(t, "[") {
			t = t[:len(t)-1]
		}
		if t == u {
			return u
		}
		u = t
	}
}

type linkSet struct {
	links []Link
	seen  map[string]int
}

func (ls *linkSet) add(u, text string) {
	u = strings.TrimSpace(u)
	low := strings.ToLower(u)
	if u == "" || strings.HasPrefix(u, "#") || len(ls.links) >= maxLinks {
		return
	}
	for _, p := range []string{"javascript:", "cid:", "data:", "about:"} {
		if strings.HasPrefix(low, p) {
			return
		}
	}
	if text == u || text == strings.TrimPrefix(u, "mailto:") {
		text = ""
	}
	if ls.seen == nil {
		ls.seen = map[string]int{}
	}
	if i, ok := ls.seen[u]; ok {
		if ls.links[i].Text == "" {
			ls.links[i].Text = text
		}
		return
	}
	ls.seen[u] = len(ls.links)
	ls.links = append(ls.links, Link{URL: u, Text: text})
}

func (ls *linkSet) addText(s string) {
	for _, u := range reURL.FindAllString(s, -1) {
		ls.add(trimURL(u), "")
	}
}

// extractLinks returns anchors from htmlBody, then bare URLs from the text.
func extractLinks(htmlBody string, texts ...string) []Link {
	var ls linkSet
	if htmlBody != "" {
		clean := reDrop.ReplaceAllString(htmlBody, " ")
		for _, m := range reAnchor.FindAllStringSubmatch(clean, -1) {
			h := reHref.FindStringSubmatch(m[1])
			if h == nil {
				continue
			}
			href := h[1] + h[2] + h[3]
			ls.add(html.UnescapeString(href), collapse(htmlToText(m[2])))
		}
		ls.addText(htmlToText(htmlBody))
	}
	for _, t := range texts {
		ls.addText(t)
	}
	return ls.links
}

// extractCodes finds likely one-time codes, preferring numbers near words
// such as "code" or "OTP"; URLs are ignored.
func extractCodes(text string) []string {
	text = reURL.ReplaceAllString(text, " ")
	var codes []string
	seen := map[string]bool{}
	add := func(c string) {
		if !seen[c] && len(codes) < 3 {
			seen[c] = true
			codes = append(codes, c)
		}
	}
	for _, m := range reCodeKw.FindAllStringSubmatch(text, -1) {
		add(m[1])
	}
	if len(codes) == 0 {
		for _, m := range reCodeSix.FindAllStringSubmatch(text, -1) {
			add(m[1])
		}
	}
	return codes
}

// ---------------------------------------------------------------------------
// SMS detection for webhooks
// ---------------------------------------------------------------------------

var (
	smsTextKeys = keyRank("text", "message", "msg", "body", "content", "sms", "smstext", "smsbody",
		"messagetext", "messagebody", "textmessage")
	smsToKeys = keyRank("to", "recipient", "recipients", "phone", "phonenumber", "mobile", "mobilenumber",
		"msisdn", "destination", "destinations", "number", "receiver", "tonumber", "dest")
	smsFromKeys = keyRank("from", "sender", "senderid", "source", "originator", "fromnumber")
)

func keyRank(keys ...string) map[string]int {
	m := make(map[string]int, len(keys))
	for i, k := range keys {
		m[k] = i
	}
	return m
}

func normKey(k string) string {
	return strings.NewReplacer("_", "", "-", "", " ", "").Replace(strings.ToLower(k))
}

func scalar(v any) (string, bool) {
	switch t := v.(type) {
	case string:
		t = strings.TrimSpace(t)
		return t, t != ""
	case json.Number:
		return t.String(), true
	}
	return "", false
}

func phoneValue(v any) (string, bool) {
	if s, ok := scalar(v); ok {
		return s, true
	}
	if arr, ok := v.([]any); ok {
		var parts []string
		for _, e := range arr {
			s, ok := scalar(e)
			if !ok {
				return "", false
			}
			parts = append(parts, s)
		}
		return strings.Join(parts, ", "), len(parts) > 0
	}
	return "", false
}

// findKey does a breadth-first search for the best-ranked key whose value
// accept takes, so top-level fields win over nested ones.
func findKey(root any, keys map[string]int, accept func(any) (string, bool)) string {
	level := []any{root}
	for depth := 0; len(level) > 0 && depth < 8; depth++ {
		best, bestRank := "", len(keys)
		var next []any
		for _, v := range level {
			switch t := v.(type) {
			case map[string]any:
				names := make([]string, 0, len(t))
				for k := range t {
					names = append(names, k)
				}
				sort.Strings(names)
				for _, k := range names {
					if r, ok := keys[normKey(k)]; ok && r < bestRank {
						if s, ok := accept(t[k]); ok {
							best, bestRank = s, r
						}
					}
					next = append(next, t[k])
				}
			case []any:
				next = append(next, t...)
			}
		}
		if best != "" {
			return best
		}
		level = next
	}
	return ""
}

func smsFromMap(m map[string]any) *SMS {
	text := findKey(m, smsTextKeys, scalar)
	if text == "" {
		return nil
	}
	return &SMS{
		Text: text,
		To:   findKey(m, smsToKeys, phoneValue),
		From: findKey(m, smsFromKeys, scalar),
	}
}

func valuesToMap(v url.Values) map[string]any {
	m := make(map[string]any, len(v))
	for k, vs := range v {
		if len(vs) > 0 {
			m[k] = vs[0]
		}
	}
	return m
}

// extractSMS guesses the message text, recipient and sender of a webhook
// from JSON, form, XML or plain-text bodies, then the query string.
func extractSMS(contentType, body, query string) *SMS {
	b := strings.TrimSpace(body)
	ct := strings.ToLower(contentType)
	var sms *SMS
	switch {
	case b == "":
	case strings.HasPrefix(b, "{") || strings.HasPrefix(b, "["):
		dec := json.NewDecoder(strings.NewReader(b))
		dec.UseNumber()
		var v any
		if dec.Decode(&v) == nil && !dec.More() {
			if m, ok := v.(map[string]any); ok {
				sms = smsFromMap(m)
			} else {
				sms = smsFromMap(map[string]any{"_": v})
			}
			break
		}
		sms = plainSMS(b)
	case reFormish.MatchString(b):
		if v, err := url.ParseQuery(b); err == nil {
			sms = smsFromMap(valuesToMap(v))
		}
	case strings.Contains(ct, "x-www-form-urlencoded"):
		sms = plainSMS(b) // e.g. curl -d 'text' without a key
	case strings.HasPrefix(b, "<"):
		m := map[string]any{}
		for _, x := range reXMLText.FindAllStringSubmatch(b, -1) {
			if t := strings.TrimSpace(html.UnescapeString(x[2])); t != "" {
				if _, dup := m[x[1]]; !dup {
					m[x[1]] = t
				}
			}
		}
		sms = smsFromMap(m)
	default:
		sms = plainSMS(b)
	}
	if v, err := url.ParseQuery(query); err == nil && len(v) > 0 {
		q := valuesToMap(v)
		if sms == nil {
			sms = smsFromMap(q)
		} else {
			if sms.To == "" {
				sms.To = findKey(q, smsToKeys, phoneValue)
			}
			if sms.From == "" {
				sms.From = findKey(q, smsFromKeys, scalar)
			}
		}
	}
	return sms
}

// plainSMS treats a short, printable body as the message itself.
func plainSMS(b string) *SMS {
	if len(b) > 2000 || !utf8.ValidString(b) {
		return nil
	}
	for _, r := range b {
		if r < 0x20 && r != '\n' && r != '\r' && r != '\t' {
			return nil
		}
	}
	return &SMS{Text: b}
}

// enrichHook fills the derived SMS, Links and Codes fields.
func enrichHook(h *Hook) {
	ct := ""
	if v := h.Headers["Content-Type"]; len(v) > 0 {
		ct = v[0]
	}
	h.SMS = extractSMS(ct, h.Body, h.Query)
	if h.SMS != nil {
		h.Links = extractLinks("", h.SMS.Text)
		h.Codes = extractCodes(h.SMS.Text)
		return
	}
	q, _ := url.QueryUnescape(h.Query)
	h.Links = extractLinks("", unescapeJSONSlashes(h.Body), q)
}

func unescapeJSONSlashes(s string) string {
	if !strings.Contains(s, `\/`) {
		return s
	}
	return strings.ReplaceAll(s, `\/`, "/")
}

// enrichMail fills the derived Links and Codes fields.
func enrichMail(m *Mail) {
	text := m.Text
	if text == "" && m.HTML != "" {
		text = htmlToText(m.HTML)
	}
	m.Links = extractLinks(m.HTML, m.Text)
	m.Codes = extractCodes(m.Subject + "\n" + text)
}

// ---------------------------------------------------------------------------
// Charsets (stdlib only: UTF-8, ASCII, Latin-1, Latin-9, Windows-1252)
// ---------------------------------------------------------------------------

var cp1252 = [32]rune{
	'€', 0x81, '‚', 'ƒ', '„', '…', '†', '‡', 'ˆ', '‰', 'Š', '‹', 'Œ', 0x8d, 'Ž', 0x8f,
	0x90, '‘', '’', '“', '”', '•', '–', '—', '˜', '™', 'š', '›', 'œ', 0x9d, 'ž', 'Ÿ',
}

var latin9 = map[byte]rune{0xa4: '€', 0xa6: 'Š', 0xa8: 'š', 0xb4: 'Ž', 0xb8: 'ž', 0xbc: 'Œ', 0xbd: 'œ', 0xbe: 'Ÿ'}

// knownCharset reports whether toUTF8 can convert cs.
func knownCharset(cs string) bool {
	switch normCharset(cs) {
	case "", "utf8", "usascii", "ascii", "iso88591", "latin1", "l1", "iso885915", "latin9",
		"windows1252", "cp1252", "xuserdefined":
		return true
	}
	return false
}

func normCharset(cs string) string {
	return strings.NewReplacer("-", "", "_", "", " ", "").Replace(strings.ToLower(strings.Trim(cs, `"' `)))
}

// toUTF8 converts b from charset cs. Unknown charsets pass through, with
// invalid UTF-8 read as Windows-1252, the usual mislabelled case.
func toUTF8(b []byte, cs string) string {
	n := normCharset(cs)
	switch n {
	case "", "utf8", "usascii", "ascii":
		if utf8.Valid(b) {
			return string(b)
		}
		n = "windows1252"
	}
	if !knownCharset(n) && utf8.Valid(b) {
		return string(b)
	}
	var sb strings.Builder
	sb.Grow(len(b) + len(b)/4)
	for _, c := range b {
		switch {
		case c < 0x80:
			sb.WriteByte(c)
		case (n == "iso885915" || n == "latin9") && latin9[c] != 0:
			sb.WriteRune(latin9[c])
		case c < 0xa0 && n != "iso88591" && n != "latin1" && n != "l1" && n != "iso885915" && n != "latin9":
			sb.WriteRune(cp1252[c-0x80])
		default:
			sb.WriteRune(rune(c))
		}
	}
	return sb.String()
}

// textOf decodes a text part's bytes using its charset parameter.
func textOf(b []byte, params map[string]string) string {
	return toUTF8(bytes.TrimPrefix(b, []byte("\xef\xbb\xbf")), params["charset"])
}
