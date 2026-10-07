// Flash messages: one-time notices carried to the next request.

package hyperchi

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

// Flash is a one-time message for the next request, such as "Saved." after a
// form post. Kind is the app's own label, such as "success" or "error", which
// templates usually turn into a class.
type Flash struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

const (
	flashCookieName = "hyperchi_flash"
	flashSessionKey = "hyperchi.flashes"
	// flashMaxAge bounds how long an unread flash cookie stays valid.
	flashMaxAge = time.Hour

	// maxFlashes is the most messages kept. Adding more drops the oldest.
	maxFlashes = 10
	// maxFlashKindBytes and maxFlashMessageBytes truncate longer values. One
	// message of the maximum size always fits in a cookie.
	maxFlashKindBytes    = 32
	maxFlashMessageBytes = 1024
)

// flashState is a request's view of its flash messages.
type flashState struct {
	loaded    bool
	incoming  []Flash // stored by earlier requests
	hadCookie bool    // the request carried a flash cookie
	read      bool    // Flashes has returned incoming
	pending   []Flash // added by Flash during this request
}

// Flash stores a message for the next request: the page a redirect lands on,
// or the next page the browser loads. This request's own render does not show
// it. To show a message now, pass it in the template data.
//
// Messages travel in a signed cookie (HttpOnly, SameSite Lax, Secure in
// production), so they need no server state. Behind SessionMiddleware they are
// kept in the session instead. At most 10 are kept, and adding an 11th drops
// the oldest. A kind is cut to 32 bytes and a message to 1024. The cookie is
// signed, not encrypted: keep secrets out of messages.
//
//	if err := c.Flash("success", "Post published."); err != nil {
//		return err
//	}
//	return c.Redirect("/posts")
//
// It fails with ErrNoSecretKey in production without SECRET_KEY (cookie
// storage only) or when the response has already been written.
func (c *Context) Flash(kind, message string) error {
	st := c.flashState()
	st.pending = append(st.pending, Flash{
		Kind:    truncateUTF8(kind, maxFlashKindBytes),
		Message: truncateUTF8(message, maxFlashMessageBytes),
	})
	return c.saveFlashes()
}

// Flashes returns the messages stored for this request and clears them, so a
// second call, or the next request, gets none. Templates rendered with View,
// Page, or Fragment receive them as .flashes, which is this same read: a
// render consumes them whether or not the template shows them.
//
//	{{range .flashes}}<p class="flash flash-{{.Kind}}">{{.Message}}</p>{{end}}
func (c *Context) Flashes() []Flash {
	st := c.flashState()
	if st.read {
		return nil
	}
	st.read = true
	if len(st.incoming) == 0 && !st.hadCookie {
		return nil
	}
	if err := c.saveFlashes(); err != nil {
		log.Printf("hyperchi: clear flash messages: %v", err)
	}
	return st.incoming
}

// flashState loads the request's stored messages on first use: the flash
// cookie, then the session's. An unreadable cookie (tampered, expired, or
// signed by a removed key) holds no messages and is cleared on the next save.
func (c *Context) flashState() *flashState {
	if c.flash == nil {
		c.flash = &flashState{}
	}
	st := c.flash
	if st.loaded {
		return st
	}
	st.loaded = true
	if ck, err := c.r.Cookie(flashCookieName); err == nil {
		st.hadCookie = true
		raw, err := c.app.keys.verify(flashCookieName, ck.Value, c.app.now(), flashMaxAge)
		if err == nil {
			st.incoming = decodeFlashes(raw)
		}
	}
	if s := c.Session(); s != nil {
		stored, err := sessionFlashes(s)
		if err != nil {
			log.Printf("hyperchi: drop unreadable flash messages in session: %v", err)
		}
		st.incoming = append(st.incoming, stored...)
	}
	return st
}

// saveFlashes writes the messages the next request should see: the unread
// incoming ones and those added now, bounded. They go to the session when the
// request has one, otherwise to the signed cookie, replacing any flash cookie
// this response already sets.
func (c *Context) saveFlashes() error {
	if c.w.written {
		return errResponseWritten
	}
	st := c.flashState()
	var out []Flash
	if !st.read {
		out = append(out, st.incoming...)
	}
	out = append(out, st.pending...)
	if len(out) > maxFlashes {
		out = out[len(out)-maxFlashes:]
	}

	if s := c.Session(); s != nil {
		if st.hadCookie {
			c.setFlashCookie(c.expiredFlashCookie())
		}
		if len(out) == 0 {
			if s.Has(flashSessionKey) {
				s.Delete(flashSessionKey)
			}
			return nil
		}
		body, err := json.Marshal(out)
		if err != nil {
			return err
		}
		s.Set(flashSessionKey, string(body))
		return nil
	}

	if len(out) == 0 {
		if st.hadCookie {
			c.setFlashCookie(c.expiredFlashCookie())
		} else {
			c.setFlashCookie(nil)
		}
		return nil
	}
	for {
		ck, err := c.flashCookie(out)
		if err != nil {
			return err
		}
		if len(ck.String()) <= maxCookieBytes || len(out) == 1 {
			c.setFlashCookie(ck)
			return nil
		}
		out = out[1:] // too large: drop the oldest
	}
}

// flashCookie signs messages into the flash cookie.
func (c *Context) flashCookie(messages []Flash) (*http.Cookie, error) {
	signed, err := c.app.keys.sign(flashCookieName, encodeFlashes(messages), c.app.now())
	if err != nil {
		return nil, err
	}
	ck := &http.Cookie{Name: flashCookieName, Value: signed, MaxAge: int(flashMaxAge / time.Second)}
	c.app.secureCookieDefaults(ck)
	return ck, nil
}

// expiredFlashCookie deletes the flash cookie. Its attributes match the cookie
// that was set, or the browser keeps it.
func (c *Context) expiredFlashCookie() *http.Cookie {
	ck := &http.Cookie{Name: flashCookieName, MaxAge: -1}
	c.app.secureCookieDefaults(ck)
	return ck
}

// setFlashCookie replaces the response's flash Set-Cookie line with ck, or
// removes it when ck is nil, so repeated saves send one cookie.
func (c *Context) setFlashCookie(ck *http.Cookie) {
	h := c.w.Header()
	lines := h.Values("Set-Cookie")
	kept := lines[:0:0]
	for _, line := range lines {
		if !strings.HasPrefix(line, flashCookieName+"=") {
			kept = append(kept, line)
		}
	}
	h.Del("Set-Cookie")
	for _, line := range kept {
		h.Add("Set-Cookie", line)
	}
	if ck != nil {
		http.SetCookie(c.w, ck)
	}
}

// encodeFlashes packs messages as length-prefixed kind and message pairs.
// Unlike JSON, no character expands, so the size bound is predictable.
func encodeFlashes(messages []Flash) []byte {
	var buf []byte
	for _, f := range messages {
		buf = binary.AppendUvarint(buf, uint64(len(f.Kind)))
		buf = append(buf, f.Kind...)
		buf = binary.AppendUvarint(buf, uint64(len(f.Message)))
		buf = append(buf, f.Message...)
	}
	return buf
}

// decodeFlashes reverses encodeFlashes. The app signed the input, so a
// malformed one is a bug. It yields the messages decoded before the fault.
func decodeFlashes(buf []byte) []Flash {
	var out []Flash
	next := func() (string, bool) {
		n, size := binary.Uvarint(buf)
		if size <= 0 || n > uint64(len(buf)-size) {
			return "", false
		}
		s := string(buf[size : size+int(n)])
		buf = buf[size+int(n):]
		return s, true
	}
	for len(buf) > 0 && len(out) < maxFlashes {
		kind, ok := next()
		if !ok {
			break
		}
		message, ok := next()
		if !ok {
			break
		}
		out = append(out, Flash{Kind: kind, Message: message})
	}
	return out
}

// sessionFlashes reads the messages a session holds.
func sessionFlashes(s *KVSession) ([]Flash, error) {
	if !s.Has(flashSessionKey) {
		return nil, nil
	}
	raw, ok := s.Get(flashSessionKey).(string)
	if !ok {
		return nil, errors.New("flash messages are not a string")
	}
	var out []Flash
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, err
	}
	return out, nil
}

// truncateUTF8 cuts s to at most n bytes without splitting a character.
func truncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
