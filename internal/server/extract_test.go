package server

import (
	"reflect"
	"strings"
	"testing"
)

func TestExtractCodes(t *testing.T) {
	cases := []struct {
		name, text string
		want       []string
	}{
		{"sms", "Your verification code is: 175117. It expires in 2 minutes.", []string{"175117"}},
		{"dashed", "Your code is 123-456", []string{"123-456"}},
		{"subject", "OTP 482913\nCode: 482913", []string{"482913"}},
		{"far keyword", "Use the following code to complete your verification:\n622681\nThis code will expire in 2 minutes.", []string{"622681"}},
		{"accented", "Votre code de vérification est : 384021", []string{"384021"}},
		{"bare six digits", "just some text 555123", []string{"555123"}},
		{"ignores urls", "Reset: https://x.test/reset?code=998877 now", nil},
		{"ignores long numbers", "Call +94771234567 for your code", nil},
		{"no code", "Nightly build #4521 passed", nil},
	}
	for _, c := range cases {
		if got := extractCodes(c.text); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestExtractLinks(t *testing.T) {
	link := "https://localhost:8190/gate/invite?applicationId=1&executionId=2&inviteToken=3"
	esc := strings.ReplaceAll(link, "&", "&amp;")
	body := `<p><a href="` + esc + `" style="x">
      Reset Password
    </a></p><p><a href='` + esc + `'>` + esc + `</a></p>
	<a href="#top">top</a><a href="javascript:alert(1)">x</a><img src="cid:logo">
	<a href="mailto:help@x.test">help@x.test</a>
	<p>Docs at https://docs.x.test/page. Thanks</p>`
	got := extractLinks(body, "see https://other.x.test/a)")
	want := []Link{
		{URL: link, Text: "Reset Password"},
		{URL: "mailto:help@x.test"},
		{URL: "https://docs.x.test/page"},
		{URL: "https://other.x.test/a"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
}

func TestHTMLToText(t *testing.T) {
	in := "<html><head><title>T</title><style>p{}</style></head><body><h2>Hi&nbsp;there</h2>\n<p>Line   one<br>two</p><!-- c --></body></html>"
	if got, want := htmlToText(in), "Hi there\n\nLine one\ntwo"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestExtractSMS(t *testing.T) {
	cases := []struct {
		name, ct, body, query string
		want                  *SMS
	}{
		{"plain text mislabelled json", "application/json",
			"Your verification code is: 175117.", "",
			&SMS{Text: "Your verification code is: 175117."}},
		{"json", "application/json", `{"to":"+94771234567","body":"Your OTP is 482913"}`, "",
			&SMS{To: "+94771234567", Text: "Your OTP is 482913"}},
		{"wso2 custom", "application/json", `{"content":"code 739201","to":"+9477","from":"WSO2IS"}`, "",
			&SMS{To: "+9477", From: "WSO2IS", Text: "code 739201"}},
		{"twilio form", "application/x-www-form-urlencoded",
			"To=%2B94771234567&From=%2B15005550006&Body=Your+code+is+582014", "",
			&SMS{To: "+94771234567", From: "+15005550006", Text: "Your code is 582014"}},
		{"numeric to", "application/json", `{"from":"WSO2","to":94771234567,"text":"hi"}`, "",
			&SMS{To: "94771234567", From: "WSO2", Text: "hi"}},
		{"infobip nested", "application/json",
			`{"messages":[{"destinations":[{"to":"41793026727"}],"from":"InfoSMS","text":"hello"}]}`, "",
			&SMS{To: "41793026727", From: "InfoSMS", Text: "hello"}},
		{"recipients array", "", `{"recipients":["+1","+2"],"message":"m"}`, "",
			&SMS{To: "+1, +2", Text: "m"}},
		{"query", "", "", "to=94771234567&msg=Your%20code%20is%20901122",
			&SMS{To: "94771234567", Text: "Your code is 901122"}},
		{"xml", "text/xml", `<Envelope><Body><Notify><To>+9477</To><Message>Login code 246810</Message></Notify></Body></Envelope>`, "",
			&SMS{To: "+9477", Text: "Login code 246810"}},
		{"generic event", "application/json", `{"event":"user.created","data":{"id":"u-1","email":"a@b.c"}}`, "", nil},
		{"curl -d text", "application/x-www-form-urlencoded", "Your code is 424242", "",
			&SMS{Text: "Your code is 424242"}},
		{"empty", "", "", "", nil},
		{"binary", "application/octet-stream", "\x00\x01\x02", "", nil},
	}
	for _, c := range cases {
		got := extractSMS(c.ct, c.body, c.query)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %+v, want %+v", c.name, got, c.want)
		}
	}
}

func TestToUTF8(t *testing.T) {
	cases := []struct {
		in, cs, want string
	}{
		{"caf\xe9", "ISO-8859-1", "café"},
		{"\x80 \x93x\x94", "windows-1252", "€ “x”"},
		{"\xa4", "iso-8859-15", "€"},
		{"café", "utf-8", "café"},
		{"caf\xe9", "", "café"}, // mislabelled
	}
	for _, c := range cases {
		if got := toUTF8([]byte(c.in), c.cs); got != c.want {
			t.Errorf("toUTF8(%q, %s) = %q, want %q", c.in, c.cs, got, c.want)
		}
	}
}
