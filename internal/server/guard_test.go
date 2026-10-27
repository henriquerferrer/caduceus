package server

import (
	"bufio"
	"crypto/x509"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/henriquerferrer/caduceus/internal/store"
)

// guardReq sends a request to ts with the given Host (empty keeps the
// server's own) and headers, and returns the status and the body.
func guardReq(t *testing.T, ts *httptest.Server, method, path, host string, headers map[string]string, body io.Reader) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, ts.URL+path, body)
	if err != nil {
		t.Fatal(err)
	}
	if host != "" {
		req.Host = host
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	// The timeout ends an answered event stream, which never closes itself;
	// what it sent so far is returned.
	resp, err := (&http.Client{Timeout: 2 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func guardedServer(t *testing.T, opts Options) (*store.Store, *httptest.Server) {
	t.Helper()
	st := newStore(t)
	ts := httptest.NewServer(New(st, opts))
	t.Cleanup(ts.Close)
	return st, ts
}

// A page that rebinds its own hostname to 127.0.0.1 sends its own Host: it
// must not read codes or message bodies, nor expire a verification.
func TestGuardRefusesReboundHost(t *testing.T) {
	st, ts := guardedServer(t, Options{DeliveryDelay: -1})
	const smsBody = "Your login code is 774411"
	twilioSend(t, ts.URL, url.Values{"To": {"+15550008801"}, "From": {"x"}, "Body": {smsBody}}).Body.Close()
	_, v := startVerification(t, ts.URL, "VAx", url.Values{"To": {"+15550008802"}, "Channel": {"sms"}})
	sid := v["sid"].(string)
	stored, err := st.GetVerification(sid)
	if err != nil || stored.Code == "" {
		t.Fatalf("verification not stored: %+v %v", stored, err)
	}

	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/verifications"},
		{http.MethodGet, "/api/v1/verifications/" + sid},
		{http.MethodGet, "/api/v1/events"},
		{http.MethodGet, "/2010-04-01/Accounts/ACtest/Messages.json"},
		{http.MethodPost, "/api/v1/verifications/" + sid + "/expire"},
	} {
		status, body := guardReq(t, ts, c.method, c.path, "evil.example:8026", nil, nil)
		if status != http.StatusForbidden {
			t.Errorf("%s %s with a rebound Host answered %d, want 403", c.method, c.path, status)
		}
		if strings.Contains(body, stored.Code) || strings.Contains(body, smsBody) {
			t.Errorf("%s %s leaked a code or an SMS body: %s", c.method, c.path, body)
		}
	}
	if got, err := st.GetVerification(sid); err != nil || got.Status != "pending" {
		t.Fatalf("verification after the refused expire: %+v %v", got, err)
	}
}

// A cross-site page sends the developer's browser to an allowed Host: its
// writes must be refused and change nothing.
func TestGuardRefusesCrossSiteWrites(t *testing.T) {
	var hooked atomic.Int32
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hooked.Add(1) }))
	defer hook.Close()
	st, ts := guardedServer(t, Options{DeliveryDelay: -1})
	_, v := startVerification(t, ts.URL, "VAx", url.Values{"To": {"+15550008803"}, "Channel": {"sms"}})
	sid := v["sid"].(string)
	before, err := st.List(store.Filter{})
	if err != nil {
		t.Fatal(err)
	}

	variants := []map[string]string{
		{"Origin": "http://evil.example"},
		{"Sec-Fetch-Site": "cross-site"},
		{"Origin": "http://localhost:3000"},
	}
	for _, headers := range variants {
		for _, c := range []struct {
			path, contentType, body string
		}{
			{"/api/v1/inbound", "application/json", `{"from":"+15550008804","to":"+15550000000","body":"forged reply","webhook":"` + hook.URL + `"}`},
			{"/api/v1/send", "application/json", `{"to":"+15550008805","body":"forged send"}`},
			{"/v2/Services/VAx/Verifications/" + sid, "application/x-www-form-urlencoded", "Status=approved"},
		} {
			h := map[string]string{"Content-Type": c.contentType}
			for k, v := range headers {
				h[k] = v
			}
			if status, body := guardReq(t, ts, http.MethodPost, c.path, "", h, strings.NewReader(c.body)); status != http.StatusForbidden {
				t.Errorf("POST %s with %v answered %d %s, want 403", c.path, headers, status, body)
			}
		}
	}

	if n := hooked.Load(); n != 0 {
		t.Errorf("the inbound webhook was called %d times", n)
	}
	if after, err := st.List(store.Filter{}); err != nil || len(after) != len(before) {
		t.Errorf("messages changed: %d before, %d after (%v)", len(before), len(after), err)
	}
	if got, err := st.GetVerification(sid); err != nil || got.Status != "pending" {
		t.Errorf("verification after the refused update: %+v %v", got, err)
	}
}

// Following a link to the inbox from another site still opens it.
func TestGuardAllowsTopLevelNavigation(t *testing.T) {
	_, ts := guardedServer(t, Options{})
	nav := map[string]string{"Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "navigate"}
	if status, _ := guardReq(t, ts, http.MethodGet, "/", "", nav, nil); status != http.StatusOK {
		t.Fatalf("cross-site navigation to / answered %d, want 200", status)
	}
	if status, _ := guardReq(t, ts, http.MethodPost, "/", "", nav, nil); status != http.StatusForbidden {
		t.Fatalf("cross-site navigating POST to / answered %d, want 403", status)
	}
	framed := map[string]string{"Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "iframe"}
	if status, _ := guardReq(t, ts, http.MethodGet, "/", "", framed, nil); status != http.StatusForbidden {
		t.Fatalf("cross-site framed navigation to / answered %d, want 403", status)
	}
}

// Same-origin browser requests and header-less clients pass.
func TestGuardAllowsSameOriginAndPlainClients(t *testing.T) {
	_, ts := guardedServer(t, Options{DeliveryDelay: -1})
	host := strings.TrimPrefix(ts.URL, "http://")
	for _, headers := range []map[string]string{
		nil,
		{"Sec-Fetch-Site": "same-origin", "Origin": "http://" + host},
		{"Sec-Fetch-Site": "none"},
	} {
		h := map[string]string{"Content-Type": "application/json"}
		for k, v := range headers {
			h[k] = v
		}
		if status, body := guardReq(t, ts, http.MethodPost, "/api/v1/send", "", h, strings.NewReader(`{"to":"+15550008806","body":"hi"}`)); status != http.StatusCreated {
			t.Errorf("POST /api/v1/send with %v answered %d %s, want 201", headers, status, body)
		}
	}
}

func TestGuardHostList(t *testing.T) {
	_, ts := guardedServer(t, Options{})
	for _, host := range []string{"localhost:8026", "127.0.0.1:8026", "[::1]:8026", "LOCALHOST.:8026"} {
		if status, body := guardReq(t, ts, http.MethodGet, "/api/v1/messages", host, nil, nil); status != http.StatusOK {
			t.Errorf("Host %s answered %d %s, want 200", host, status, body)
		}
	}

	status, body := guardReq(t, ts, http.MethodGet, "/api/v1/messages", "caduceus:8026", nil, nil)
	if status != http.StatusForbidden {
		t.Fatalf("Host caduceus:8026 by default answered %d, want 403", status)
	}
	if !strings.Contains(body, "-allowed-hosts") || !strings.Contains(body, "CADUCEUS_ALLOWED_HOSTS") {
		t.Fatalf("refusal does not name the fix: %s", body)
	}

	for _, allowed := range []string{"caduceus", "caduceus:9999"} {
		_, ts := guardedServer(t, Options{AllowedHosts: []string{allowed}})
		if status, body := guardReq(t, ts, http.MethodGet, "/api/v1/messages", "caduceus:8026", nil, nil); status != http.StatusOK {
			t.Errorf("Host caduceus:8026 with AllowedHosts %q answered %d %s, want 200", allowed, status, body)
		}
	}
}

func TestGuardRefusalShapes(t *testing.T) {
	_, ts := guardedServer(t, Options{})
	for _, path := range []string{"/2010-04-01/Accounts/ACtest/Messages.json", "/v2/Services/VAx/Verifications/VEx"} {
		status, body := guardReq(t, ts, http.MethodGet, path, "evil.example", nil, nil)
		var e map[string]any
		if err := json.Unmarshal([]byte(body), &e); err != nil {
			t.Fatalf("%s: %v: %s", path, err, body)
		}
		wantTwilioError(t, status, e, http.StatusForbidden, 20403)
		if msg, _ := e["message"].(string); msg == "" {
			t.Fatalf("%s: no message: %s", path, body)
		}
	}
	status, body := guardReq(t, ts, http.MethodGet, "/api/v1/messages", "evil.example", nil, nil)
	var e map[string]any
	if err := json.Unmarshal([]byte(body), &e); err != nil || status != http.StatusForbidden {
		t.Fatalf("/api/v1/messages: %d %s", status, body)
	}
	if msg, _ := e["error"].(string); msg == "" || e["code"] != nil {
		t.Fatalf("/api/v1/messages refusal is not {\"error\": …}: %s", body)
	}
}

// Proxied requests carry the provider's Host by design and are never guarded.
func TestGuardExemptsProxiedRequests(t *testing.T) {
	ca, err := LoadCA(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	st := newStore(t)
	ts := httptest.NewServer(New(st, Options{CA: ca, DeliveryDelay: -1}))
	defer ts.Close()
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(ca.PEM)
	tunnel := proxyClient(t, ts.URL, roots)
	plain := proxyClient(t, ts.URL, nil)

	for _, c := range []struct {
		client *http.Client
		target string
		form   url.Values
	}{
		{tunnel, "https://api.twilio.com" + messagesPath, url.Values{"To": {"+15550008811"}, "From": {"x"}, "Body": {"code 1111"}}},
		{tunnel, verificationsURL("https://verify.twilio.com", "VAx"), url.Values{"To": {"+15550008812"}, "Channel": {"sms"}}},
		{plain, "http://api.twilio.com" + messagesPath, url.Values{"To": {"+15550008813"}, "From": {"x"}, "Body": {"code 3333"}}},
	} {
		req, err := http.NewRequest(http.MethodPost, c.target, strings.NewReader(c.form.Encode()))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.SetBasicAuth("ACtest", "secret")
		resp, err := c.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusCreated {
			t.Errorf("POST %s through the proxy answered %d %s, want 201", c.target, resp.StatusCode, body)
		}
	}
	if got, err := st.List(store.Filter{}); err != nil || len(got) != 3 {
		t.Fatalf("captured %d messages through the proxy, want 3 (%v)", len(got), err)
	}
}

func TestSplitHosts(t *testing.T) {
	got := SplitHosts(" caduceus, sms.test ,, ,LAN.example:8026")
	want := []string{"caduceus", "sms.test", "LAN.example:8026"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("SplitHosts = %q, want %q", got, want)
	}
	if got := SplitHosts(""); len(got) != 0 {
		t.Fatalf("SplitHosts(\"\") = %q, want none", got)
	}
}

// Each name of a spaced -allowed-hosts list is answered.
func TestGuardAllowsEachListedHost(t *testing.T) {
	_, ts := guardedServer(t, Options{AllowedHosts: SplitHosts("caduceus, sms.test")})
	for _, host := range []string{"caduceus:8026", "sms.test:8026"} {
		if status, body := guardReq(t, ts, http.MethodGet, "/api/v1/messages", host, nil, nil); status != http.StatusOK {
			t.Errorf("Host %s answered %d %s, want 200", host, status, body)
		}
	}
}

// A request without a Host is refused, even when an allowed-hosts entry
// reduces to an empty name.
func TestGuardRefusesEmptyHost(t *testing.T) {
	for _, extra := range [][]string{nil, {"", " ", ":8026", "[]"}} {
		_, ts := guardedServer(t, Options{AllowedHosts: extra})
		// HTTP/1.0 lets a request omit Host; Go's client always sends one.
		conn, err := net.Dial("tcp", strings.TrimPrefix(ts.URL, "http://"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(conn, "GET /api/v1/messages HTTP/1.0\r\n\r\n"); err != nil {
			t.Fatal(err)
		}
		resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		conn.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("empty Host with AllowedHosts %q answered %d %s, want 403", extra, resp.StatusCode, body)
		}
	}
}
