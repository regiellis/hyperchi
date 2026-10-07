package helpers

import (
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestJSONAccessors(t *testing.T) {
	j, err := FromJSON(`{"name":"ana","age":41,"ratio":0.5,"ok":true,"off":false,"tags":["a",1],"nested":{"k":"v"},"n":null,"numstr":"12","fstr":"1.5"}`)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		got  any
		want any
	}{
		{"GetString", j.GetString("name"), "ana"},
		{"GetString wrong type", j.GetString("age"), ""},
		{"GetStringOr missing", j.GetStringOr("missing", "d"), "d"},
		{"GetStringOr present", j.GetStringOr("name", "d"), "ana"},
		{"GetInt from float64", j.GetInt("age"), 41},
		{"GetInt truncates", j.GetInt("ratio"), 0},
		{"GetInt from string", j.GetInt("numstr"), 12},
		{"GetIntOr bad string", j.GetIntOr("fstr", 3), 3},
		{"GetFloat", j.GetFloat("ratio"), 0.5},
		{"GetFloat from string", j.GetFloat("fstr"), 1.5},
		{"GetFloatOr missing", j.GetFloatOr("missing", 2.5), 2.5},
		{"GetBool", j.GetBool("ok"), true},
		{"GetBool missing", j.GetBool("missing"), false},
		{"GetBoolOr keeps false", j.GetBoolOr("off", true), false},
		{"GetBoolOr wrong type", j.GetBoolOr("name", true), true},
		{"GetSlice", len(j.GetSlice("tags")), 2},
		{"GetSlice wrong type", len(j.GetSlice("name")), 0},
		{"GetJSON", j.GetJSON("nested").GetString("k"), "v"},
		{"GetJSON wrong type", len(j.GetJSON("name")), 0},
		{"GetJSON null", len(j.GetJSON("n")), 0},
		{"Has null key", j.Has("n"), true},
		{"Has missing", j.Has("missing"), false},
		{"Get", j.Get("name"), "ana"},
		{"Get missing", j.Get("missing"), nil},
	}
	for _, tc := range tests {
		if tc.got != tc.want {
			t.Errorf("%s = %#v, want %#v", tc.name, tc.got, tc.want)
		}
	}
}

func TestJSONIntBounds(t *testing.T) {
	for _, v := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), 1e300, -1e300} {
		if got := (JSON{"v": v}).GetIntOr("v", 7); got != 7 {
			t.Errorf("GetIntOr(%v) = %d, want fallback", v, got)
		}
	}
	if got := (JSON{"v": -3.9}).GetInt("v"); got != -3 {
		t.Errorf("GetInt(-3.9) = %d, want -3", got)
	}
	if got := (JSON{"v": 5}).GetFloat("v"); got != 5 {
		t.Errorf("GetFloat(int 5) = %v", got)
	}
}

func TestJSONMutationAndEncoding(t *testing.T) {
	j := NewJSON().Set("b", 2).Set("a", "x")
	keys := j.Keys()
	sort.Strings(keys)
	if strings.Join(keys, ",") != "a,b" || len(j.Values()) != 2 {
		t.Fatalf("Keys/Values: %v", keys)
	}
	if got := j.ToString(); got != `{"a":"x","b":2}` {
		t.Errorf("ToString = %s", got)
	}
	if got := j.ToPrettyString(); got != "{\n  \"a\": \"x\",\n  \"b\": 2\n}" {
		t.Errorf("ToPrettyString = %q", got)
	}

	clone := j.Clone()
	clone.Set("c", 3).Delete("a")
	if j.Has("c") || !j.Has("a") || clone.Has("a") {
		t.Error("Clone must be independent at the top level")
	}
	merged := j.Merge(JSON{"b": 20, "d": 4})
	if merged.GetInt("b") != 20 || merged.GetInt("d") != 4 || j.GetInt("b") != 20 {
		t.Error("Merge overwrites into the receiver")
	}

	bad := JSON{"ch": make(chan int)}
	if bad.ToString() != "{}" || bad.ToPrettyString() != "{}" {
		t.Error("unencodable JSON should render {}")
	}
	if ToJSON(math.NaN()) != "{}" || ToPrettyJSON(make(chan int)) != "{}" {
		t.Error("ToJSON/ToPrettyJSON fallback")
	}
	if ToJSON([]int{1, 2}) != "[1,2]" || ToPrettyJSON(map[string]int{"a": 1}) != "{\n  \"a\": 1\n}" {
		t.Error("ToJSON/ToPrettyJSON")
	}
}

func TestFromJSONErrors(t *testing.T) {
	for _, in := range []string{"", "[1]", `"s"`, "{bad", "3"} {
		if _, err := FromJSON(in); err == nil {
			t.Errorf("FromJSON(%q) accepted a non-object", in)
		}
		if got := FromJSONOr(in); got == nil || len(got) != 0 {
			t.Errorf("FromJSONOr(%q) = %v, want empty object", in, got)
		}
	}
	if got := FromJSONOr(`{"a":1}`); got.GetInt("a") != 1 {
		t.Error("FromJSONOr valid")
	}
}

func TestParseJSON(t *testing.T) {
	type item struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	items, err := ParseJSON[[]item](`[{"id":1,"name":"é"}]`)
	if err != nil || len(items) != 1 || items[0].Name != "é" {
		t.Fatalf("ParseJSON = %v, %v", items, err)
	}
	if _, err := ParseJSON[item](`{"id":"x"}`); err == nil {
		t.Error("ParseJSON type mismatch accepted")
	}
	if got := ParseJSONOr("nope", 9); got != 9 {
		t.Errorf("ParseJSONOr fallback = %d", got)
	}
	if got := ParseJSONOr("4", 9); got != 4 {
		t.Errorf("ParseJSONOr = %d", got)
	}
}

func TestQueryParams(t *testing.T) {
	q := NewQueryParams().
		Set("q", "a b&c").
		Add("tag", "x").Add("tag", "y").
		SetInt("page", 2).
		SetBool("draft", false).
		SetIf(true, "yes", "1").
		SetIf(false, "no", "1").
		SetIfNotEmpty("empty", "").
		SetIfNotEmpty("full", "v")

	if got := q.String(); got != "draft=false&full=v&page=2&q=a+b%26c&tag=x&tag=y&yes=1" {
		t.Errorf("String = %s", got)
	}
	if q.Values().Get("page") != "2" || len(q.Values()["tag"]) != 2 {
		t.Error("Values")
	}
	if got := NewQueryParams().Set("a", "1").URL("/search"); got != "/search?a=1" {
		t.Errorf("URL = %s", got)
	}
	if got := NewQueryParams().Set("a", "1").URL("/search?x=0"); got != "/search?x=0&a=1" {
		t.Errorf("URL with existing query = %s", got)
	}
	if got := NewQueryParams().URL("/plain"); got != "/plain" {
		t.Errorf("URL without params = %s", got)
	}
}

// echoServer reports the request back as JSON.
func echoServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		user, pass, basic := r.BasicAuth()
		w.Header().Set("X-Echo", "yes")
		if r.URL.Query().Get("status") == "404" {
			w.WriteHeader(http.StatusNotFound)
		}
		if err := json.NewEncoder(w).Encode(map[string]any{
			"method": r.Method,
			"path":   r.URL.Path,
			"body":   string(body),
			"ctype":  r.Header.Get("Content-Type"),
			"auth":   r.Header.Get("Authorization"),
			"user":   user,
			"pass":   pass,
			"basic":  basic,
			"custom": r.Header.Get("X-Custom"),
		}); err != nil {
			t.Errorf("encode: %v", err)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// echoer returns a function that decodes an echoServer response, failing t
// on any error. It takes the (response, error) pair of a client call directly.
func echoer(t *testing.T) func(*HTTPResponse, error) JSON {
	return func(resp *HTTPResponse, err error) JSON {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		j, err := resp.JSON()
		if err != nil {
			t.Fatal(err)
		}
		return j
	}
}

func TestHTTPClientBasicAuth(t *testing.T) {
	srv := echoServer(t)
	echo := echoer(t)
	resp, err := NewHTTPClient().WithBaseURL(srv.URL).WithAuth("ana", "p@ss:wörd").Get("/")
	j := echo(resp, err)
	if !j.GetBool("basic") || j.GetString("user") != "ana" || j.GetString("pass") != "p@ss:wörd" {
		t.Fatalf("server could not decode Basic credentials from %q", j.GetString("auth"))
	}
}

func TestHTTPClientRequests(t *testing.T) {
	srv := echoServer(t)
	echo := echoer(t)
	client := NewHTTPClient().
		WithBaseURL(srv.URL + "/").
		WithTimeout(5 * time.Second).
		WithBearerToken("tok").
		WithHeaders(map[string]string{"X-Custom": "c1"})

	j := echo(client.Get("api/items"))
	if j.GetString("method") != "GET" || j.GetString("path") != "/api/items" || j.GetString("auth") != "Bearer tok" || j.GetString("custom") != "c1" {
		t.Errorf("GET: %v", j)
	}

	tests := []struct {
		name   string
		call   func() (*HTTPResponse, error)
		method string
		body   string
	}{
		{"post string sent raw", func() (*HTTPResponse, error) { return client.Post("/p", "raw text") }, "POST", "raw text"},
		{"post struct json", func() (*HTTPResponse, error) { return client.Post("/p", JSON{"a": 1}) }, "POST", `{"a":1}`},
		{"post nil", func() (*HTTPResponse, error) { return client.Post("/p", nil) }, "POST", ""},
		{"put", func() (*HTTPResponse, error) { return client.Put("/p", []int{1}) }, "PUT", "[1]"},
		{"put string", func() (*HTTPResponse, error) { return client.Put("/p", "s") }, "PUT", "s"},
		{"patch", func() (*HTTPResponse, error) { return client.Patch("/p", map[string]bool{"x": true}) }, "PATCH", `{"x":true}`},
		{"patch nil", func() (*HTTPResponse, error) { return client.Patch("/p", nil) }, "PATCH", ""},
		{"delete", func() (*HTTPResponse, error) { return client.Delete("/p") }, "DELETE", ""},
		{"absolute URL bypasses base", func() (*HTTPResponse, error) { return client.Get(srv.URL + "/abs") }, "GET", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			j := echoer(t)(tc.call())
			if j.GetString("method") != tc.method || j.GetString("body") != tc.body {
				t.Errorf("got %s %q, want %s %q", j.GetString("method"), j.GetString("body"), tc.method, tc.body)
			}
		})
	}

	j = echo(client.PostForm("/f", url.Values{"k": {"v w"}}))
	if j.GetString("ctype") != "application/x-www-form-urlencoded" || j.GetString("body") != "k=v+w" {
		t.Errorf("PostForm: %v", j)
	}
}

func TestHTTPClientMarshalErrors(t *testing.T) {
	client := NewHTTPClient()
	unencodable := make(chan int)
	for name, call := range map[string]func() (*HTTPResponse, error){
		"post":  func() (*HTTPResponse, error) { return client.Post("http://127.0.0.1:1", unencodable) },
		"put":   func() (*HTTPResponse, error) { return client.Put("http://127.0.0.1:1", unencodable) },
		"patch": func() (*HTTPResponse, error) { return client.Patch("http://127.0.0.1:1", unencodable) },
	} {
		resp, err := call()
		if err == nil || resp != nil || !strings.Contains(err.Error(), "marshal") {
			t.Errorf("%s: %v %v", name, resp, err)
		}
	}
	if resp, err := client.Get("://bad url"); err == nil || resp != nil {
		t.Error("invalid URL accepted")
	}
	if resp, err := client.PostForm("://bad url", nil); err == nil || resp != nil {
		t.Error("PostForm invalid URL accepted")
	}
}

func TestHTTPResponseHelpers(t *testing.T) {
	srv := echoServer(t)
	client := NewHTTPClient().WithBaseURL(srv.URL)

	ok, err := client.Get("/")
	if err != nil {
		t.Fatal(err)
	}
	if ok.StatusCode() != 200 || !ok.IsOK() || !ok.IsSuccess() || ok.IsError() {
		t.Error("200 predicates")
	}
	if ok.Header("X-Echo") != "yes" || ok.Headers()["X-Echo"][0] != "yes" {
		t.Error("Header/Headers")
	}
	text := ok.TextOr("fallback")
	if !strings.Contains(text, `"method":"GET"`) {
		t.Errorf("TextOr = %q", text)
	}
	if err := ok.Close(); err != nil {
		t.Errorf("Close after read: %v", err)
	}

	missing, err := client.Get("/?status=404")
	if err != nil {
		t.Fatal(err)
	}
	if missing.StatusCode() != 404 || missing.IsOK() || missing.IsSuccess() || !missing.IsError() {
		t.Error("404 predicates: HTTP errors are responses, not transport errors")
	}
	if missing.JSONOr().GetString("method") != "GET" {
		t.Error("JSONOr on a 404 body")
	}

	var nilResp *HTTPResponse
	if nilResp.TextOr("d") != "d" || len(nilResp.JSONOr()) != 0 {
		t.Error("nil response fallbacks")
	}
	if j, err := nilResp.JSON(); err == nil || j == nil {
		t.Error("JSON on nil response")
	}

	notJSON := &HTTPResponse{resp: &http.Response{Body: io.NopCloser(strings.NewReader("<html>"))}}
	var v any
	if err := notJSON.Parse(&v); err == nil || !strings.Contains(err.Error(), "parse") {
		t.Errorf("Parse invalid body: %v", err)
	}
}

func TestPackageLevelHTTP(t *testing.T) {
	srv := echoServer(t)
	echo := echoer(t)

	j := echo(Get(srv.URL + "/g"))
	if j.GetString("method") != "GET" {
		t.Error("Get")
	}
	j = echo(Post(srv.URL, JSON{"a": 1}))
	if j.GetString("method") != "POST" || j.GetString("ctype") != "application/json" || j.GetString("body") != `{"a":1}` {
		t.Errorf("Post: %v", j)
	}
	j = echo(Put(srv.URL, "x"))
	if j.GetString("method") != "PUT" || j.GetString("ctype") != "application/json" {
		t.Errorf("Put: %v", j)
	}
	j = echo(Delete(srv.URL))
	if j.GetString("method") != "DELETE" {
		t.Error("Delete")
	}
	j = echo(PostForm(srv.URL, url.Values{"a": {"1"}}))
	if j.GetString("body") != "a=1" {
		t.Error("PostForm")
	}

	fetched, err := FetchJSON(srv.URL + "/f")
	if err != nil || fetched.GetString("path") != "/f" {
		t.Errorf("FetchJSON = %v, %v", fetched, err)
	}
	if FetchJSONOr(srv.URL+"/fo").GetString("path") != "/fo" {
		t.Error("FetchJSONOr")
	}
	text, err := FetchText(srv.URL + "/t")
	if err != nil || !strings.Contains(text, `"/t"`) {
		t.Errorf("FetchText = %q, %v", text, err)
	}
	if !strings.Contains(FetchTextOr(srv.URL+"/to", "d"), `"/to"`) {
		t.Error("FetchTextOr")
	}

	// An unreachable server: transport errors surface, fallbacks apply.
	closed := httptest.NewServer(http.NotFoundHandler())
	dead := closed.URL
	closed.Close()
	if j, err := FetchJSON(dead); err == nil || j == nil || len(j) != 0 {
		t.Errorf("FetchJSON on a closed server = %v, %v", j, err)
	}
	if j := FetchJSONOr(dead); j == nil || len(j) != 0 {
		t.Error("FetchJSONOr on a closed server")
	}
	if _, err := FetchText(dead); err == nil {
		t.Error("FetchText on a closed server")
	}
	if FetchTextOr(dead, "d") != "d" {
		t.Error("FetchTextOr on a closed server")
	}
}
