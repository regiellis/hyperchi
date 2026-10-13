package main

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/regiellis/hyperchi/hyperchi"
)

func TestMergeFieldsInOrderOfFirstUse(t *testing.T) {
	got := mergeFields("Hi {{ Name }}", "{{company}} and {{name}}, {{ city}} {{1bad}} {{ two words }}")
	if strings.Join(got, ",") != "name,company,city" {
		t.Fatalf("got %v", got)
	}
}

func TestMergeReportsMissingFields(t *testing.T) {
	m := mergeLetter("Hello {{name}}", "Your firm: {{company}}. City: {{city}}.", formatText,
		map[string]string{"name": "Ada", "company": "  "})
	if m.Subject != "Hello Ada" || m.Body != "Your firm:   . City: ." {
		t.Fatalf("got %+v", m)
	}
	if strings.Join(m.Missing, ",") != "city,company" {
		t.Fatalf("missing %v", m.Missing)
	}
}

func TestMergeEscapesValuesInHTMLLetters(t *testing.T) {
	values := map[string]string{"name": `<script>alert("x")</script>`, "company": "Tom & Jerry's"}
	m := mergeLetter("For {{name}}", "<p>Dear <b>{{name}}</b> of {{company}}</p>", formatHTML, values)
	if m.Body != "<p>Dear <b>&lt;script&gt;alert(&#34;x&#34;)&lt;/script&gt;</b> of Tom &amp; Jerry&#39;s</p>" {
		t.Fatalf("body %s", m.Body)
	}
	// The subject is plain text and goes in a header, not an HTML document.
	if m.Subject != `For <script>alert("x")</script>` {
		t.Fatalf("subject %s", m.Subject)
	}
	text := mergeLetter("x", "Dear {{name}}", formatText, values)
	if text.Body != `Dear <script>alert("x")</script>` {
		t.Fatalf("a plain-text letter must not be HTML-escaped: %s", text.Body)
	}
}

func TestMergeSubjectCannotAddHeaders(t *testing.T) {
	m := mergeLetter("Hi {{name}}", "", formatText, map[string]string{"name": "Eve\r\nBcc: all@example.com"})
	if strings.ContainsAny(m.Subject, "\r\n") || m.Subject != "Hi Eve Bcc: all@example.com" {
		t.Fatalf("subject %q", m.Subject)
	}
}

func TestLetterFormRejectsStrayTags(t *testing.T) {
	err := LetterForm{Subject: "Hi {{name}}", Body: "Dear {{ first name }}"}.Validate()
	fields := hyperchi.FieldErrors(err)
	if len(fields["body"]) != 1 || !strings.Contains(fields["body"][0], "{{ first name }}") {
		t.Fatalf("got %v", err)
	}
	if err := (LetterForm{Subject: "Hi {{name}}", Body: "{{company}}"}).Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestPreviewWarnsWhenTheListLacksAField(t *testing.T) {
	a := newTestApp(t, nil)
	a.signIn()
	listID := a.seedList("Mixed", 4, func(i int) map[string]string {
		if i == 0 {
			return map[string]string{"company": "<Acme & Sons>"}
		}
		return map[string]string{}
	})
	recipient, err := one[Recipient](a.s.db, `SELECT * FROM recipients WHERE list_id = ? AND email = 'person000@example.com'`, listID)
	if err != nil {
		t.Fatal(err)
	}
	res, body := a.post("/letters/preview", url.Values{
		"title": {"t"}, "subject": {"Hi {{name}}"}, "format": {"html"},
		"body":    {"<p>{{company}} in {{region}}</p>"},
		"list_id": {fmt.Sprint(listID)}, "recipient_id": {fmt.Sprint(recipient.ID)},
	})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", res.StatusCode, body)
	}
	for _, want := range []string{
		"3 of 4 recipients on Mixed have no value for {{company}}",
		"Mixed has no {{region}} values at all",
		"This recipient has no value for {{region}}",
		// The merged value is escaped inside the letter, and the letter is
		// escaped again as the iframe's srcdoc attribute.
		"&lt;p&gt;&amp;lt;Acme &amp;amp; Sons&amp;gt; in &lt;/p&gt;",
		`sandbox=""`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("preview lacks %q:\n%s", want, body)
		}
	}
}

func TestPreviewWithoutAList(t *testing.T) {
	a := newTestApp(t, nil)
	a.signIn()
	res, body := a.post("/letters/preview", url.Values{
		"subject": {"Hi {{name}}"}, "body": {"Dear {{name}}"}, "format": {"text"}, "list_id": {""},
	})
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "Pick a list to preview a real recipient.") {
		t.Fatalf("status %d: %s", res.StatusCode, body)
	}
}
