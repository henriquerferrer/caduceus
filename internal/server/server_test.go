package server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/henriquerferrer/caduceus/internal/store"
)

const messagesPath = "/2010-04-01/Accounts/ACtest/Messages.json"

func newStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open("", 100)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(New(newStore(t), Options{}))
	t.Cleanup(ts.Close)
	return ts
}

func decode(t *testing.T, resp *http.Response, v any) {
	t.Helper()
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatal(err)
	}
}

func twilioSend(t *testing.T, base string, form url.Values) *http.Response {
	t.Helper()
	resp, err := http.PostForm(base+messagesPath, form)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestTwilioSendThenWait(t *testing.T) {
	ts := newTestServer(t)
	resp := twilioSend(t, ts.URL, url.Values{"To": {"+351912345678"}, "From": {"+15005550006"}, "Body": {"Your code is 482913"}})
	var created map[string]any
	decode(t, resp, &created)
	if resp.StatusCode != http.StatusCreated || !strings.HasPrefix(created["sid"].(string), "SM") || created["status"] != "queued" {
		t.Fatalf("unexpected create response: %d %v", resp.StatusCode, created)
	}

	resp, err := http.Get(ts.URL + "/api/v1/wait?to=" + url.QueryEscape("+351 912 345 678"))
	if err != nil {
		t.Fatal(err)
	}
	var m store.Message
	decode(t, resp, &m)
	if m.OTP != "482913" || m.ID != created["sid"] {
		t.Fatalf("wait returned %+v", m)
	}
}

func TestWaitBlocksUntilMessageArrives(t *testing.T) {
	ts := newTestServer(t)
	got := make(chan store.Message, 1)
	go func() {
		resp, err := http.Get(ts.URL + "/api/v1/wait?to=15551234567&timeout=5s")
		if err != nil {
			t.Error(err)
			return
		}
		var m store.Message
		decode(t, resp, &m)
		got <- m
	}()
	time.Sleep(50 * time.Millisecond)
	twilioSend(t, ts.URL, url.Values{"To": {"+15559999999"}, "From": {"x"}, "Body": {"other number"}}).Body.Close()
	twilioSend(t, ts.URL, url.Values{"To": {"+15551234567"}, "From": {"x"}, "Body": {"code 1234"}}).Body.Close()
	select {
	case m := <-got:
		if m.OTP != "1234" {
			t.Fatalf("got %+v", m)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("wait did not return")
	}
}

func TestWaitTimesOut(t *testing.T) {
	ts := newTestServer(t)
	resp, err := http.Get(ts.URL + "/api/v1/wait?to=1555&timeout=50ms")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusRequestTimeout {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestTwilioValidation(t *testing.T) {
	ts := newTestServer(t)
	resp := twilioSend(t, ts.URL, url.Values{"From": {"+15005550006"}, "Body": {"hi"}})
	var e map[string]any
	decode(t, resp, &e)
	if resp.StatusCode != http.StatusBadRequest || e["code"] != float64(21604) {
		t.Fatalf("got %d %v", resp.StatusCode, e)
	}
}

func TestStatusCallbacks(t *testing.T) {
	statuses := make(chan string, 4)
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		statuses <- r.PostForm.Get("MessageStatus")
	}))
	defer hook.Close()
	ts := newTestServer(t)
	twilioSend(t, ts.URL, url.Values{"To": {"+1555"}, "From": {"x"}, "Body": {"hi"}, "StatusCallback": {hook.URL}}).Body.Close()
	for _, want := range []string{"sent", "delivered"} {
		select {
		case got := <-statuses:
			if got != want {
				t.Fatalf("status = %q, want %q", got, want)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("no %q callback", want)
		}
	}
}

func TestClear(t *testing.T) {
	ts := newTestServer(t)
	twilioSend(t, ts.URL, url.Values{"To": {"+1555"}, "From": {"x"}, "Body": {"hi"}}).Body.Close()
	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/v1/messages", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	resp, _ = http.Get(ts.URL + "/api/v1/messages")
	var list struct{ Messages []store.Message }
	decode(t, resp, &list)
	if len(list.Messages) != 0 {
		t.Fatalf("%d messages left after clear", len(list.Messages))
	}
}

func proxyClient(t *testing.T, proxyURL string, roots *x509.CertPool) *http.Client {
	t.Helper()
	u, err := url.Parse(proxyURL)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{Transport: &http.Transport{
		Proxy:           http.ProxyURL(u),
		TLSClientConfig: &tls.Config{RootCAs: roots},
	}}
}

func TestProxyCapturesProviderHosts(t *testing.T) {
	ca, err := LoadCA(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	st := newStore(t)
	ts := httptest.NewServer(New(st, Options{CA: ca}))
	defer ts.Close()
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(ca.PEM)

	form := url.Values{"To": {"+15551234567"}, "From": {"x"}, "Body": {"code 9911"}}
	resp, err := proxyClient(t, ts.URL, roots).PostForm("https://api.twilio.com"+messagesPath, form)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if got, err := st.List(store.Filter{To: "+15551234567"}); err != nil || len(got) != 1 || got[0].OTP != "9911" {
		t.Fatalf("captured %+v, %v", got, err)
	}
}

func TestProxyTunnelsOtherHosts(t *testing.T) {
	real := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("real server"))
	}))
	defer real.Close()
	ca, err := LoadCA(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(New(newStore(t), Options{CA: ca}))
	defer ts.Close()

	// Only the real server's certificate is trusted, so this passes only if
	// the connection is tunnelled rather than intercepted.
	roots := x509.NewCertPool()
	roots.AddCert(real.Certificate())
	resp, err := proxyClient(t, ts.URL, roots).Get(real.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "real server" {
		t.Fatalf("body = %q", body)
	}
}

func TestCAIsReusedAcrossRestarts(t *testing.T) {
	dir := t.TempDir()
	first, err := LoadCA(dir)
	if err != nil {
		t.Fatal(err)
	}
	second, err := LoadCA(dir)
	if err != nil {
		t.Fatal(err)
	}
	if string(first.PEM) != string(second.PEM) {
		t.Fatal("CA changed between loads")
	}
}

func TestInterceptsOnlyTheMessagingAndVerifyHosts(t *testing.T) {
	for host, want := range map[string]bool{
		"api.twilio.com":               true,
		"api.dublin.ie1.twilio.com":    true,
		"verify.twilio.com":            true,
		"verify.dublin.ie1.twilio.com": true,
		"lookups.twilio.com":           false,
		"api.twilio.com.evil.test":     false,
		"verify.twilio.com.evil.test":  false,
		"example.com":                  false,
	} {
		if got := intercepted(host); got != want {
			t.Errorf("intercepted(%q) = %v, want %v", host, got, want)
		}
	}
}

// Endpoints on the messaging host that Caduceus doesn't emulate, such as
// voice calls, must reach the real provider.
func TestProxyPassesUnemulatedEndpointsUpstream(t *testing.T) {
	seen := make(chan string, 1)
	real := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, _, _ := r.BasicAuth()
		seen <- r.Method + " " + r.Host + r.URL.Path + " as " + user
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"sid":"CAreal"}`))
	}))
	defer real.Close()
	ca, err := LoadCA(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	st := newStore(t)
	srv := New(st, Options{CA: ca})
	// Stand in for api.twilio.com: every upstream connection lands on the fake.
	srv.upstream.Transport = &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, real.Listener.Addr().String())
		},
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	ts := httptest.NewServer(srv)
	defer ts.Close()
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(ca.PEM)

	req, _ := http.NewRequest(http.MethodPost, "https://api.twilio.com/2010-04-01/Accounts/ACtest/Calls.json", strings.NewReader("To=%2B1555"))
	req.SetBasicAuth("ACtest", "secret")
	resp, err := proxyClient(t, ts.URL, roots).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated || string(body) != `{"sid":"CAreal"}` {
		t.Fatalf("got %d %s", resp.StatusCode, body)
	}
	if got, want := <-seen, "POST api.twilio.com/2010-04-01/Accounts/ACtest/Calls.json as ACtest"; got != want {
		t.Fatalf("upstream saw %q, want %q", got, want)
	}
	if got, _ := st.List(store.Filter{}); len(got) != 0 {
		t.Fatalf("a passed-through request was captured: %+v", got)
	}
}

// fakeTwilio stands in for every real Twilio host: it records each request it
// receives and answers with a recognisable body.
func fakeTwilio(t *testing.T, tlsServer bool) (*httptest.Server, chan string) {
	t.Helper()
	seen := make(chan string, 8)
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, _, _ := r.BasicAuth()
		seen <- r.Method + " " + r.Host + r.URL.Path + " as " + user
		w.Write([]byte("real twilio"))
	})
	var real *httptest.Server
	if tlsServer {
		real = httptest.NewTLSServer(h)
	} else {
		real = httptest.NewServer(h)
	}
	t.Cleanup(real.Close)
	return real, seen
}

// dialTo returns a transport whose every connection lands on addr.
func dialTo(addr string) *http.Transport {
	return &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		},
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
}

// newProxy starts Caduceus in proxy mode with every passed-through request
// going to a fake Twilio, and returns a client that trusts Caduceus's CA.
func newProxy(t *testing.T) (*store.Store, *http.Client, chan string) {
	t.Helper()
	real, seen := fakeTwilio(t, true)
	ca, err := LoadCA(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	st := newStore(t)
	srv := New(st, Options{CA: ca})
	srv.upstream.Transport = dialTo(real.Listener.Addr().String())
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(ca.PEM)
	return st, proxyClient(t, ts.URL, roots), seen
}

func do(t *testing.T, c *http.Client, method, target, user string, form url.Values) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, target, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if user != "" {
		req.SetBasicAuth(user, "secret")
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func verificationsURL(base, service string) string {
	return base + "/v2/Services/" + service + "/Verifications"
}

// startVerification posts to the local base URL and decodes the response.
func startVerification(t *testing.T, base, service string, form url.Values) (int, map[string]any) {
	t.Helper()
	resp := do(t, http.DefaultClient, http.MethodPost, verificationsURL(base, service), "ACtest", form)
	var out map[string]any
	decode(t, resp, &out)
	return resp.StatusCode, out
}

func wantTwilioError(t *testing.T, status int, body map[string]any, wantStatus, wantCode int) {
	t.Helper()
	if status != wantStatus || body["code"] != float64(wantCode) || body["status"] != float64(wantStatus) {
		t.Fatalf("got %d %v, want %d code %d", status, body, wantStatus, wantCode)
	}
}

// verifyMessages returns the captured Verify messages for a recipient.
func verifyMessages(t *testing.T, st *store.Store, to string) []store.Message {
	t.Helper()
	msgs, err := st.List(store.Filter{To: to})
	if err != nil {
		t.Fatal(err)
	}
	return msgs
}

func TestProxyRoutesByHost(t *testing.T) {
	st, client, seen := newProxy(t)
	for _, c := range []struct{ method, target, want string }{
		{http.MethodGet, "https://verify.twilio.com/v2/Services/VAx", "GET verify.twilio.com/v2/Services/VAx as ACtest"},
		{http.MethodPost, "https://api.twilio.com/v2/Services/VAx/Verifications", "POST api.twilio.com/v2/Services/VAx/Verifications as ACtest"},
		{http.MethodPost, "https://verify.twilio.com" + messagesPath, "POST verify.twilio.com" + messagesPath + " as ACtest"},
	} {
		form := url.Values{"To": {"+15550003333"}, "Channel": {"sms"}, "From": {"x"}, "Body": {"code 1234"}}
		resp := do(t, client, c.method, c.target, "ACtest", form)
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if string(body) != "real twilio" {
			t.Fatalf("%s %s answered %d %s, want the upstream", c.method, c.target, resp.StatusCode, body)
		}
		if got := <-seen; got != c.want {
			t.Fatalf("upstream saw %q, want %q", got, c.want)
		}
	}
	if got := verifyMessages(t, st, ""); len(got) != 0 {
		t.Fatalf("a passed-through request was captured: %+v", got)
	}
}

// A plain-HTTP request in absolute form for an intercepted host must never
// reach the Caduceus API or UI.
func TestAbsoluteFormNeverReachesCaduceusAPI(t *testing.T) {
	real, seen := fakeTwilio(t, false)
	srv := New(newStore(t), Options{})
	srv.direct = dialTo(real.Listener.Addr().String())
	ts := httptest.NewServer(srv)
	defer ts.Close()
	client := proxyClient(t, ts.URL, nil)
	for _, target := range []string{"http://verify.twilio.com/api/v1/messages", "http://api.twilio.com/api/v1/messages", "http://verify.twilio.com/"} {
		resp := do(t, client, http.MethodGet, target, "", nil)
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if string(body) != "real twilio" {
			t.Fatalf("GET %s answered %d %s, want the upstream", target, resp.StatusCode, body)
		}
		<-seen
	}
}

func TestVerifyStartThroughProxy(t *testing.T) {
	st, client, _ := newProxy(t)
	resp := do(t, client, http.MethodPost, verificationsURL("https://verify.twilio.com", "VAx"), "ACtest",
		url.Values{"To": {"+15550003333"}, "Channel": {"sms"}})
	var v map[string]any
	decode(t, resp, &v)
	sid, _ := v["sid"].(string)
	if resp.StatusCode != http.StatusCreated || !strings.HasPrefix(sid, "VE") || v["status"] != "pending" || v["valid"] != false {
		t.Fatalf("got %d %v", resp.StatusCode, v)
	}
	if attempts, _ := v["send_code_attempts"].([]any); len(attempts) != 1 {
		t.Fatalf("send_code_attempts = %v", v["send_code_attempts"])
	}
	if _, err := time.Parse("2006-01-02T15:04:05Z", v["date_created"].(string)); err != nil {
		t.Fatalf("date_created is not ISO 8601: %v", err)
	}
	if v["service_sid"] != "VAx" || v["to"] != "+15550003333" || v["channel"] != "sms" || v["sna"] != nil ||
		v["url"] != "https://verify.twilio.com/v2/Services/VAx/Verifications/"+sid {
		t.Fatalf("resource fields: %v", v)
	}

	srv := httptest.NewServer(New(st, Options{}))
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/api/v1/wait?to=" + url.QueryEscape("+15550003333") + "&timeout=2s")
	if err != nil {
		t.Fatal(err)
	}
	var m store.Message
	decode(t, resp, &m)
	if len(m.OTP) != 6 || store.Digits(m.OTP) != m.OTP || m.Channel != "sms" || m.VerificationSID != sid ||
		m.Provider != "twilio-verify" || m.From != "Verify" || m.Body != "Your verification code is: "+m.OTP {
		t.Fatalf("wait returned %+v", m)
	}
}

func TestVerifyCustomCode(t *testing.T) {
	ts := newTestServer(t)
	st := ts.Config.Handler.(*Server).store
	for i, code := range []string{"4321", "1234567890", "AB1234"} {
		to := "+1555000100" + string(rune('0'+i))
		if status, v := startVerification(t, ts.URL, "VAx", url.Values{"To": {to}, "Channel": {"sms"}, "CustomCode": {code}}); status != http.StatusCreated {
			t.Fatalf("got %d %v", status, v)
		}
		if got := verifyMessages(t, st, to); len(got) != 1 || got[0].OTP != code {
			t.Fatalf("CustomCode %s captured %+v", code, got)
		}
	}
}

func TestVerifyServiceScoping(t *testing.T) {
	ts := newTestServer(t)
	st := ts.Config.Handler.(*Server).store
	_, v := startVerification(t, ts.URL, "VAone", url.Values{"To": {"+15550003333"}, "Channel": {"sms"}})
	sid := v["sid"].(string)
	before, err := st.GetVerification(sid)
	if err != nil {
		t.Fatal(err)
	}
	other := verificationsURL(ts.URL, "VAother") + "/" + sid
	for _, c := range []struct {
		method string
		form   url.Values
	}{{http.MethodGet, nil}, {http.MethodPost, url.Values{"Status": {"canceled"}}}} {
		resp := do(t, http.DefaultClient, c.method, other, "ACtest", c.form)
		var e map[string]any
		decode(t, resp, &e)
		wantTwilioError(t, resp.StatusCode, e, http.StatusNotFound, 20404)
	}
	after, err := st.GetVerification(sid)
	if err != nil || after.Status != "pending" || !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("verification changed: %+v, %v", after, err)
	}
}

func TestVerifyChannels(t *testing.T) {
	ts := newTestServer(t)
	st := ts.Config.Handler.(*Server).store
	for _, c := range []struct{ channel, to string }{{"email", "ada@example.com"}, {"call", "+15550004444"}, {"sna", "+15550005555"}, {"auto", "+15550006666"}} {
		status, v := startVerification(t, ts.URL, "VAx", url.Values{"To": {c.to}, "Channel": {c.channel}})
		if status != http.StatusCreated || v["channel"] != c.channel {
			t.Fatalf("%s: got %d %v", c.channel, status, v)
		}
		if got := verifyMessages(t, st, c.to); len(got) != 1 || got[0].Channel != c.channel {
			t.Fatalf("%s captured %+v", c.channel, got)
		}
		sna, _ := v["sna"].(map[string]any)
		if (c.channel == "sna") != (sna != nil && sna["url"] != nil && sna["url"] != "") {
			t.Fatalf("%s: sna = %v", c.channel, v["sna"])
		}
	}
	for _, form := range []url.Values{
		{"To": {"+15550003333"}, "Channel": {"email"}},
		{"To": {"ada@example.com"}, "Channel": {"sms"}},
		{"To": {"15550003333"}, "Channel": {"call"}},
		{"To": {"+15550003333"}, "Channel": {"voice"}},
	} {
		status, e := startVerification(t, ts.URL, "VAx", form)
		wantTwilioError(t, status, e, http.StatusBadRequest, 60200)
	}
}

func TestVerifyResendReusesVerification(t *testing.T) {
	ts := newTestServer(t)
	st := ts.Config.Handler.(*Server).store
	to := "+15550003333"
	_, first := startVerification(t, ts.URL, "VAx", url.Values{"To": {to}, "Channel": {"sms"}})
	status, second := startVerification(t, ts.URL, "VAx", url.Values{"To": {to}, "Channel": {"whatsapp"}, "CustomCode": {"9999"}})
	if status != http.StatusCreated || second["sid"] != first["sid"] || second["channel"] != "whatsapp" {
		t.Fatalf("resend: got %d %v", status, second)
	}
	if attempts, _ := second["send_code_attempts"].([]any); len(attempts) != 2 {
		t.Fatalf("send_code_attempts = %v", second["send_code_attempts"])
	}
	msgs := verifyMessages(t, st, to)
	if len(msgs) != 2 || msgs[0].OTP != msgs[1].OTP || msgs[0].Channel != "whatsapp" || msgs[0].VerificationSID != first["sid"] {
		t.Fatalf("captured %+v", msgs)
	}
	for range 3 {
		if status, v := startVerification(t, ts.URL, "VAx", url.Values{"To": {to}, "Channel": {"sms"}}); status != http.StatusCreated {
			t.Fatalf("got %d %v", status, v)
		}
	}
	status, e := startVerification(t, ts.URL, "VAx", url.Values{"To": {to}, "Channel": {"sms"}})
	wantTwilioError(t, status, e, http.StatusTooManyRequests, 60203)
	if got := verifyMessages(t, st, to); len(got) != store.MaxSendAttempts {
		t.Fatalf("%d messages captured, want %d", len(got), store.MaxSendAttempts)
	}
}

func TestVerifyConcurrentSends(t *testing.T) {
	ts := newTestServer(t)
	st := ts.Config.Handler.(*Server).store
	to := "+15550007777"
	type result struct {
		status int
		body   map[string]any
	}
	results := make(chan result, 10)
	for range 10 {
		go func() {
			// No t.Fatal off the test goroutine: report and send a zero result.
			var r result
			resp, err := http.PostForm(verificationsURL(ts.URL, "VAx"), url.Values{"To": {to}, "Channel": {"sms"}})
			if err != nil {
				t.Error(err)
			} else {
				r.status = resp.StatusCode
				if err := json.NewDecoder(resp.Body).Decode(&r.body); err != nil {
					t.Error(err)
				}
				resp.Body.Close()
			}
			results <- r
		}()
	}
	created, refused, sids := 0, 0, map[any]bool{}
	for range 10 {
		r := <-results
		switch {
		case r.status == http.StatusCreated:
			created++
			sids[r.body["sid"]] = true
		case r.status == http.StatusTooManyRequests && r.body["code"] == float64(60203):
			refused++
		default:
			t.Errorf("unexpected response %d %v", r.status, r.body)
		}
	}
	if created != 5 || refused != 5 || len(sids) != 1 {
		t.Fatalf("created %d (sids %v), refused %d", created, sids, refused)
	}
	vs, err := st.ListVerifications(store.Filter{To: to})
	if err != nil || len(vs) != 1 || len(vs[0].SendAttempts) != 5 {
		t.Fatalf("stored %+v, %v", vs, err)
	}
}

// A send whose message cannot be stored changes nothing: no verification is
// created, a reused one keeps its attempts and channel, and the error is
// Twilio's 500 shape.
func TestVerifySendFailureLeavesNoState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "caduceus.db")
	st, err := store.Open(path, 100)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ts := httptest.NewServer(New(st, Options{}))
	t.Cleanup(ts.Close)
	// A second connection to the same file makes message inserts fail.
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	failMessages := func(on bool) {
		t.Helper()
		q := `DROP TRIGGER IF EXISTS fail_messages`
		if on {
			q = `CREATE TRIGGER fail_messages BEFORE INSERT ON messages BEGIN SELECT RAISE(FAIL, 'disk full'); END`
		}
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	wantFailure := func(status int, body map[string]any) {
		t.Helper()
		if status != http.StatusInternalServerError || body["code"] != float64(20500) || body["status"] != float64(500) {
			t.Fatalf("got %d %v, want 500 code 20500", status, body)
		}
	}

	newTo, reusedTo := "+15550008881", "+15550008882"
	failMessages(true)
	wantFailure(startVerification(t, ts.URL, "VAx", url.Values{"To": {newTo}, "Channel": {"sms"}}))
	if vs, err := st.ListVerifications(store.Filter{To: newTo}); err != nil || len(vs) != 0 {
		t.Fatalf("failed send stored %+v, %v", vs, err)
	}

	failMessages(false)
	if status, _ := startVerification(t, ts.URL, "VAx", url.Values{"To": {reusedTo}, "Channel": {"sms"}}); status != http.StatusCreated {
		t.Fatalf("first send: %d", status)
	}
	failMessages(true)
	wantFailure(startVerification(t, ts.URL, "VAx", url.Values{"To": {reusedTo}, "Channel": {"whatsapp"}}))
	vs, err := st.ListVerifications(store.Filter{To: reusedTo})
	if err != nil || len(vs) != 1 || len(vs[0].SendAttempts) != 1 || vs[0].Channel != "sms" {
		t.Fatalf("after a failed resend: %+v, %v", vs, err)
	}
}

// Clearing the inbox deletes verifications, so it waits for verifyMu like
// every other Verify mutation: a send or update between its read and its
// write never sees the verification vanish.
func TestClearWaitsForVerifyMutations(t *testing.T) {
	ts := newTestServer(t)
	srv := ts.Config.Handler.(*Server)
	srv.verifyMu.Lock()
	done := make(chan int, 1)
	go func() {
		req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/v1/messages", nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Error(err)
			done <- 0
			return
		}
		resp.Body.Close()
		done <- resp.StatusCode
	}()
	select {
	case status := <-done:
		srv.verifyMu.Unlock()
		t.Fatalf("clear finished (%d) while a Verify mutation held verifyMu", status)
	case <-time.After(200 * time.Millisecond):
	}
	srv.verifyMu.Unlock()
	select {
	case status := <-done:
		if status != http.StatusNoContent {
			t.Fatalf("clear: %d", status)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("clear did not finish after verifyMu was released")
	}
}

// The Verify routes answer on the local base URL, without the proxy.
func TestVerifyFetchUpdateAndErrorsOnBaseURL(t *testing.T) {
	ts := newTestServer(t)
	_, v := startVerification(t, ts.URL, "VAx", url.Values{"To": {"+15550003333"}, "Channel": {"sms"}})
	sid := v["sid"].(string)
	one := verificationsURL(ts.URL, "VAx") + "/" + sid

	resp := do(t, http.DefaultClient, http.MethodGet, one, "ACtest", nil)
	var got map[string]any
	decode(t, resp, &got)
	if resp.StatusCode != http.StatusOK || got["sid"] != sid || got["status"] != "pending" || got["url"] != one {
		t.Fatalf("fetch: %d %v", resp.StatusCode, got)
	}

	resp = do(t, http.DefaultClient, http.MethodPost, one, "ACtest", url.Values{"Status": {"canceled"}})
	decode(t, resp, &got)
	if resp.StatusCode != http.StatusOK || got["status"] != "canceled" || got["valid"] != false {
		t.Fatalf("update: %d %v", resp.StatusCode, got)
	}

	for _, c := range []struct {
		method, target    string
		form              url.Values
		wantStatus, wantC int
	}{
		{http.MethodGet, verificationsURL(ts.URL, "VAx") + "/VEunknown", nil, 404, 20404},
		{http.MethodPost, verificationsURL(ts.URL, "VAx"), url.Values{"Channel": {"sms"}}, 400, 60200},
		{http.MethodPost, verificationsURL(ts.URL, "VAx"), url.Values{"To": {"+15550003333"}}, 400, 60200},
		{http.MethodPost, verificationsURL(ts.URL, "XYz"), url.Values{"To": {"+15550003333"}, "Channel": {"sms"}}, 404, 20404},
		{http.MethodGet, verificationsURL(ts.URL, "XYz") + "/" + sid, nil, 404, 20404},
	} {
		resp := do(t, http.DefaultClient, c.method, c.target, "ACtest", c.form)
		var e map[string]any
		decode(t, resp, &e)
		wantTwilioError(t, resp.StatusCode, e, c.wantStatus, c.wantC)
	}
}

func TestVerifyApprove(t *testing.T) {
	ts := newTestServer(t)
	_, v := startVerification(t, ts.URL, "VAx", url.Values{"To": {"+15550003333"}, "Channel": {"sms"}})
	resp := do(t, http.DefaultClient, http.MethodPost, verificationsURL(ts.URL, "VAx")+"/"+v["sid"].(string), "ACtest", url.Values{"Status": {"approved"}})
	var got map[string]any
	decode(t, resp, &got)
	if resp.StatusCode != http.StatusOK || got["status"] != "approved" || got["valid"] != true {
		t.Fatalf("approve: %d %v", resp.StatusCode, got)
	}
}

func TestVerifyUpdateNotPending(t *testing.T) {
	ts := newTestServer(t)
	_, v := startVerification(t, ts.URL, "VAx", url.Values{"To": {"+15550003333"}, "Channel": {"sms"}})
	one := verificationsURL(ts.URL, "VAx") + "/" + v["sid"].(string)
	do(t, http.DefaultClient, http.MethodPost, one, "ACtest", url.Values{"Status": {"canceled"}}).Body.Close()
	resp := do(t, http.DefaultClient, http.MethodPost, one, "ACtest", url.Values{"Status": {"approved"}})
	var e map[string]any
	decode(t, resp, &e)
	wantTwilioError(t, resp.StatusCode, e, http.StatusNotFound, 20404)
}

func TestVerifyAccountSID(t *testing.T) {
	ts := newTestServer(t)
	for _, c := range []struct{ user, to, want string }{
		{"ACabc", "+15550002001", "ACabc"},
		{"SKkey", "+15550002002", "AC00000000000000000000000000000000"},
	} {
		resp := do(t, http.DefaultClient, http.MethodPost, verificationsURL(ts.URL, "VAx"), c.user,
			url.Values{"To": {c.to}, "Channel": {"sms"}})
		var v map[string]any
		decode(t, resp, &v)
		if v["account_sid"] != c.want {
			t.Fatalf("user %s: account_sid = %v, want %s", c.user, v["account_sid"], c.want)
		}
	}
}

func TestTwilioMessagesExcludeVerify(t *testing.T) {
	ts := newTestServer(t)
	st := ts.Config.Handler.(*Server).store
	startVerification(t, ts.URL, "VAx", url.Values{"To": {"+15550003333"}, "Channel": {"sms"}})
	captured := verifyMessages(t, st, "+15550003333")
	if len(captured) != 1 {
		t.Fatalf("captured %+v", captured)
	}
	resp, err := http.Post(ts.URL+"/api/v1/inbound", "application/json", strings.NewReader(`{"from":"+15550009999","to":"+15005550006","body":"STOP"}`))
	if err != nil {
		t.Fatal(err)
	}
	var inbound store.Message
	decode(t, resp, &inbound)

	resp, err = http.Get(ts.URL + messagesPath)
	if err != nil {
		t.Fatal(err)
	}
	var list struct{ Messages []map[string]any }
	decode(t, resp, &list)
	if len(list.Messages) != 1 || list.Messages[0]["sid"] != inbound.ID {
		t.Fatalf("Messages.json listed %v", list.Messages)
	}
	for sid, want := range map[string]int{captured[0].ID: http.StatusNotFound, inbound.ID: http.StatusOK} {
		resp, err := http.Get(ts.URL + "/2010-04-01/Accounts/ACtest/Messages/" + sid + ".json")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Fatalf("fetch %s: status %d, want %d", sid, resp.StatusCode, want)
		}
	}
}

func checkURL(base, service string) string {
	return base + "/v2/Services/" + service + "/VerificationCheck"
}

// checkVerification posts a VerificationCheck and decodes the response.
func checkVerification(t *testing.T, c *http.Client, base, service string, form url.Values) (int, map[string]any) {
	t.Helper()
	resp := do(t, c, http.MethodPost, checkURL(base, service), "ACtest", form)
	var out map[string]any
	decode(t, resp, &out)
	return resp.StatusCode, out
}

// fetchVerification gets one verification and decodes the response.
func fetchVerification(t *testing.T, c *http.Client, base, service, sid string) (int, map[string]any) {
	t.Helper()
	resp := do(t, c, http.MethodGet, verificationsURL(base, service)+"/"+sid, "ACtest", nil)
	var out map[string]any
	decode(t, resp, &out)
	return resp.StatusCode, out
}

// sentCode returns the code of the newest Verify message to a recipient.
func sentCode(t *testing.T, st *store.Store, to string) string {
	t.Helper()
	msgs := verifyMessages(t, st, to)
	if len(msgs) == 0 || msgs[0].OTP == "" {
		t.Fatalf("no code captured for %s: %+v", to, msgs)
	}
	return msgs[0].OTP
}

// wrongCode returns a code that differs from code.
func wrongCode(code string) string {
	if code == "000000" {
		return "111111"
	}
	return "000000"
}

func TestVerifyCheckThroughProxy(t *testing.T) {
	st, client, _ := newProxy(t)
	const base, to = "https://verify.twilio.com", "+15550003333"
	resp := do(t, client, http.MethodPost, verificationsURL(base, "VAx"), "ACtest", url.Values{"To": {to}, "Channel": {"sms"}})
	var v map[string]any
	decode(t, resp, &v)
	sid, _ := v["sid"].(string)
	code := sentCode(t, st, to)

	status, got := checkVerification(t, client, base, "VAx", url.Values{"To": {to}, "Code": {wrongCode(code)}})
	if status != http.StatusOK || got["status"] != "pending" || got["valid"] != false || got["sid"] != sid {
		t.Fatalf("wrong code: %d %v", status, got)
	}
	status, got = checkVerification(t, client, base, "VAx", url.Values{"To": {to}, "Code": {code}})
	if status != http.StatusOK || got["status"] != "approved" || got["valid"] != true || got["sid"] != sid {
		t.Fatalf("right code: %d %v", status, got)
	}
	if got["service_sid"] != "VAx" || got["account_sid"] != "ACtest" || got["to"] != to || got["channel"] != "sms" ||
		got["amount"] != nil || got["payee"] != nil {
		t.Fatalf("resource fields: %v", got)
	}
	if codes, ok := got["sna_attempts_error_codes"].([]any); !ok || len(codes) != 0 {
		t.Fatalf("sna_attempts_error_codes = %#v", got["sna_attempts_error_codes"])
	}
	for _, field := range []string{"date_created", "date_updated"} {
		if s, _ := got[field].(string); !isISO8601(s) {
			t.Fatalf("%s = %v, want ISO 8601", field, got[field])
		}
	}

	status, got = checkVerification(t, client, base, "VAx", url.Values{"To": {to}, "Code": {code}})
	wantTwilioError(t, status, got, http.StatusNotFound, 20404)
	status, got = fetchVerification(t, client, base, "VAx", sid)
	wantTwilioError(t, status, got, http.StatusNotFound, 20404)
}

func isISO8601(s string) bool {
	_, err := time.Parse("2006-01-02T15:04:05Z", s)
	return err == nil
}

func TestVerifyCheckByVerificationSid(t *testing.T) {
	ts := newTestServer(t)
	st := ts.Config.Handler.(*Server).store
	const to = "+15550003334"
	_, v := startVerification(t, ts.URL, "VAx", url.Values{"To": {to}, "Channel": {"sms"}})
	sid := v["sid"].(string)
	code := sentCode(t, st, to)

	status, got := checkVerification(t, http.DefaultClient, ts.URL, "VAx", url.Values{"VerificationSid": {sid}, "Code": {wrongCode(code)}})
	if status != http.StatusOK || got["status"] != "pending" || got["valid"] != false || got["sid"] != sid {
		t.Fatalf("wrong code: %d %v", status, got)
	}
	status, got = checkVerification(t, http.DefaultClient, ts.URL, "VAx", url.Values{"VerificationSid": {sid}, "Code": {code}})
	if status != http.StatusOK || got["status"] != "approved" || got["valid"] != true || got["sid"] != sid {
		t.Fatalf("right code: %d %v", status, got)
	}
	status, got = checkVerification(t, http.DefaultClient, ts.URL, "VAx", url.Values{"VerificationSid": {sid}, "Code": {code}})
	wantTwilioError(t, status, got, http.StatusNotFound, 20404)
	status, got = fetchVerification(t, http.DefaultClient, ts.URL, "VAx", sid)
	wantTwilioError(t, status, got, http.StatusNotFound, 20404)
}

func TestVerifyCheckValidation(t *testing.T) {
	ts := newTestServer(t)
	_, v := startVerification(t, ts.URL, "VAx", url.Values{"To": {"+15550003335"}, "Channel": {"sms"}})
	for _, c := range []struct {
		form  url.Values
		param string
	}{
		{url.Values{"Code": {"123456"}}, "To"},
		{url.Values{"To": {"+15550003335"}}, "Code"},
		{url.Values{"VerificationSid": {v["sid"].(string)}}, "Code"},
		{url.Values{"To": {"+15550003335"}, "Code": {""}}, "Code"},
		{url.Values{"To": {"15550003335"}, "Code": {"123456"}}, "To"},
		{url.Values{"To": {"+1 (555) 000-3335"}, "Code": {"123456"}}, "To"},
	} {
		status, got := checkVerification(t, http.DefaultClient, ts.URL, "VAx", c.form)
		if status != http.StatusBadRequest || got["code"] != float64(60200) || got["message"] != "Invalid parameter: "+c.param {
			t.Errorf("form %v: got %d %v, want 400 code 60200 naming %s", c.form, status, got, c.param)
		}
	}
}

// A check's To must be E.164 or an email address, as Twilio's docs require:
// a number in another format is refused rather than matched by its digits.
func TestVerifyCheckToFormat(t *testing.T) {
	ts := newTestServer(t)
	st := ts.Config.Handler.(*Server).store
	const to = "+15550003342"
	_, v := startVerification(t, ts.URL, "VAx", url.Values{"To": {to}, "Channel": {"sms"}})
	sid := v["sid"].(string)
	code := sentCode(t, st, to)
	for _, bad := range []string{"15550003342", "+1 (555) 000-3342"} {
		status, got := checkVerification(t, http.DefaultClient, ts.URL, "VAx", url.Values{"To": {bad}, "Code": {code}})
		if status != http.StatusBadRequest || got["code"] != float64(60200) {
			t.Errorf("To %q: got %d %v, want 400 code 60200", bad, status, got)
		}
	}
	stored, err := st.GetVerification(sid)
	if err != nil || stored.Status != "pending" || stored.CheckAttempts != 0 {
		t.Fatalf("verification changed: %+v, %v", stored, err)
	}

	const email = "Someone@Example.com"
	startVerification(t, ts.URL, "VAx", url.Values{"To": {email}, "Channel": {"email"}})
	status, got := checkVerification(t, http.DefaultClient, ts.URL, "VAx", url.Values{"To": {email}, "Code": {sentCode(t, st, email)}})
	if status != http.StatusOK || got["status"] != "approved" {
		t.Fatalf("email check: %d %v", status, got)
	}
}

func TestVerifyCheckAttemptLimit(t *testing.T) {
	ts := newTestServer(t)
	st := ts.Config.Handler.(*Server).store
	const to = "+15550003336"
	_, v := startVerification(t, ts.URL, "VAx", url.Values{"To": {to}, "Channel": {"sms"}})
	sid := v["sid"].(string)
	code := sentCode(t, st, to)
	wrong := url.Values{"To": {to}, "Code": {wrongCode(code)}}

	for i := range store.MaxCheckAttempts {
		if status, got := checkVerification(t, http.DefaultClient, ts.URL, "VAx", wrong); status != http.StatusOK || got["status"] != "pending" {
			t.Fatalf("wrong check %d: %d %v", i+1, status, got)
		}
	}
	status, got := checkVerification(t, http.DefaultClient, ts.URL, "VAx", url.Values{"To": {to}, "Code": {code}})
	wantTwilioError(t, status, got, http.StatusTooManyRequests, 60202)
	stored, err := st.GetVerification(sid)
	if err != nil || stored.Status != "max_attempts_reached" || stored.CheckAttempts != store.MaxCheckAttempts {
		t.Fatalf("stored %+v, %v", stored, err)
	}

	status, got = checkVerification(t, http.DefaultClient, ts.URL, "VAx", url.Values{"VerificationSid": {sid}, "Code": {code}})
	wantTwilioError(t, status, got, http.StatusNotFound, 20404)
	status, got = fetchVerification(t, http.DefaultClient, ts.URL, "VAx", sid)
	wantTwilioError(t, status, got, http.StatusNotFound, 20404)
}

func TestVerifyCheckCanceled(t *testing.T) {
	ts := newTestServer(t)
	st := ts.Config.Handler.(*Server).store
	const to = "+15550003337"
	_, v := startVerification(t, ts.URL, "VAx", url.Values{"To": {to}, "Channel": {"sms"}})
	sid := v["sid"].(string)
	code := sentCode(t, st, to)
	do(t, http.DefaultClient, http.MethodPost, verificationsURL(ts.URL, "VAx")+"/"+sid, "ACtest", url.Values{"Status": {"canceled"}}).Body.Close()
	for _, form := range []url.Values{{"To": {to}, "Code": {code}}, {"VerificationSid": {sid}, "Code": {code}}} {
		status, got := checkVerification(t, http.DefaultClient, ts.URL, "VAx", form)
		wantTwilioError(t, status, got, http.StatusNotFound, 20404)
	}
	status, got := fetchVerification(t, http.DefaultClient, ts.URL, "VAx", sid)
	wantTwilioError(t, status, got, http.StatusNotFound, 20404)
}

func TestVerifyCheckServiceScoping(t *testing.T) {
	ts := newTestServer(t)
	st := ts.Config.Handler.(*Server).store
	const to = "+15550003338"
	_, v := startVerification(t, ts.URL, "VAone", url.Values{"To": {to}, "Channel": {"sms"}})
	sid := v["sid"].(string)
	code := sentCode(t, st, to)
	before, err := st.GetVerification(sid)
	if err != nil {
		t.Fatal(err)
	}
	for _, form := range []url.Values{{"VerificationSid": {sid}, "Code": {code}}, {"To": {to}, "Code": {code}}} {
		status, got := checkVerification(t, http.DefaultClient, ts.URL, "VAother", form)
		wantTwilioError(t, status, got, http.StatusNotFound, 20404)
	}
	after, err := st.GetVerification(sid)
	if err != nil || after.Status != "pending" || after.CheckAttempts != 0 || !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("verification changed: %+v, %v", after, err)
	}
}

func TestVerifyCheckExpired(t *testing.T) {
	ts := newTestServer(t)
	st := ts.Config.Handler.(*Server).store
	const to = "+15550003339"
	_, v := startVerification(t, ts.URL, "VAx", url.Values{"To": {to}, "Channel": {"sms"}})
	sid := v["sid"].(string)
	code := sentCode(t, st, to)
	stored, err := st.GetVerification(sid)
	if err != nil {
		t.Fatal(err)
	}
	stored.ExpiresAt = time.Now().Add(-time.Minute)
	if _, err := st.UpdateVerification(stored); err != nil {
		t.Fatal(err)
	}
	for _, form := range []url.Values{{"To": {to}, "Code": {code}}, {"VerificationSid": {sid}, "Code": {code}}} {
		status, got := checkVerification(t, http.DefaultClient, ts.URL, "VAx", form)
		wantTwilioError(t, status, got, http.StatusNotFound, 20404)
	}
	status, got := fetchVerification(t, http.DefaultClient, ts.URL, "VAx", sid)
	wantTwilioError(t, status, got, http.StatusNotFound, 20404)
}

func TestVerifyConcurrentChecks(t *testing.T) {
	ts := newTestServer(t)
	st := ts.Config.Handler.(*Server).store
	const to = "+15550003340"
	_, v := startVerification(t, ts.URL, "VAx", url.Values{"To": {to}, "Channel": {"sms"}})
	sid := v["sid"].(string)
	wrong := url.Values{"To": {to}, "Code": {wrongCode(sentCode(t, st, to))}}
	statuses := make(chan int, 10)
	for range 10 {
		go func() {
			// No t.Fatal off the test goroutine: report and send a zero status.
			resp, err := http.PostForm(checkURL(ts.URL, "VAx"), wrong)
			if err != nil {
				t.Error(err)
				statuses <- 0
				return
			}
			resp.Body.Close()
			statuses <- resp.StatusCode
		}()
	}
	counts := map[int]int{}
	for range 10 {
		counts[<-statuses]++
	}
	if counts[http.StatusOK] != store.MaxCheckAttempts || counts[http.StatusTooManyRequests] != 1 || counts[http.StatusNotFound] != 10-store.MaxCheckAttempts-1 {
		t.Fatalf("statuses %v", counts)
	}
	stored, err := st.GetVerification(sid)
	if err != nil || stored.CheckAttempts > store.MaxCheckAttempts || stored.Status != "max_attempts_reached" {
		t.Fatalf("stored %+v, %v", stored, err)
	}
}

// The check route answers on the local base URL; through the proxy only the
// verify host emulates it, so a check sent to api.twilio.com reaches Twilio.
func TestVerifyCheckOnBaseURLAndPassThrough(t *testing.T) {
	ts := newTestServer(t)
	st := ts.Config.Handler.(*Server).store
	const to = "+15550003341"
	startVerification(t, ts.URL, "VAx", url.Values{"To": {to}, "Channel": {"sms"}})
	status, got := checkVerification(t, http.DefaultClient, ts.URL, "VAx", url.Values{"To": {to}, "Code": {sentCode(t, st, to)}})
	if status != http.StatusOK || got["status"] != "approved" {
		t.Fatalf("base URL check: %d %v", status, got)
	}

	_, client, seen := newProxy(t)
	resp := do(t, client, http.MethodPost, checkURL("https://api.twilio.com", "VAx"), "ACtest", url.Values{"To": {to}, "Code": {"123456"}})
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "real twilio" {
		t.Fatalf("check on api.twilio.com answered %d %s, want the upstream", resp.StatusCode, body)
	}
	if got, want := <-seen, "POST api.twilio.com/v2/Services/VAx/VerificationCheck as ACtest"; got != want {
		t.Fatalf("upstream saw %q, want %q", got, want)
	}
}

// apiJSON calls the Caduceus REST API and decodes the JSON answer into out.
func apiJSON(t *testing.T, method, target string, out any) int {
	t.Helper()
	req, err := http.NewRequest(method, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	decode(t, resp, out)
	return resp.StatusCode
}

func TestAPIListVerifications(t *testing.T) {
	ts := newTestServer(t)
	_, first := startVerification(t, ts.URL, "VAx", url.Values{"To": {"+15550004441"}, "Channel": {"sms"}})
	_, second := startVerification(t, ts.URL, "VAx", url.Values{"To": {"Ada@Example.com"}, "Channel": {"email"}})

	var list struct{ Verifications []map[string]any }
	if status := apiJSON(t, http.MethodGet, ts.URL+"/api/v1/verifications", &list); status != http.StatusOK {
		t.Fatalf("list: %d", status)
	}
	if len(list.Verifications) != 2 || list.Verifications[0]["id"] != second["sid"] || list.Verifications[1]["id"] != first["sid"] {
		t.Fatalf("list is not both, newest first: %v", list.Verifications)
	}
	for _, v := range list.Verifications {
		if code, _ := v["code"].(string); code == "" || v["status"] != "pending" || v["expires_at"] == nil ||
			v["send_attempts"] == nil || v["check_attempts"] != float64(0) {
			t.Fatalf("verification lacks its state: %v", v)
		}
	}

	for _, c := range []struct{ to, want string }{
		{"+1 555 000 4441", first["sid"].(string)},
		{"ada@example.com", second["sid"].(string)},
	} {
		list.Verifications = nil
		apiJSON(t, http.MethodGet, ts.URL+"/api/v1/verifications?to="+url.QueryEscape(c.to), &list)
		if len(list.Verifications) != 1 || list.Verifications[0]["id"] != c.want {
			t.Fatalf("?to=%s: %v", c.to, list.Verifications)
		}
	}
}

func TestAPIGetVerification(t *testing.T) {
	ts := newTestServer(t)
	_, v := startVerification(t, ts.URL, "VAx", url.Values{"To": {"+15550004442"}, "Channel": {"sms"}})
	var got store.Verification
	if status := apiJSON(t, http.MethodGet, ts.URL+"/api/v1/verifications/"+v["sid"].(string), &got); status != http.StatusOK ||
		got.ID != v["sid"] || got.Status != "pending" || got.Code == "" {
		t.Fatalf("get: %d %+v", status, got)
	}
	var e map[string]any
	if status := apiJSON(t, http.MethodGet, ts.URL+"/api/v1/verifications/VEunknown", &e); status != http.StatusNotFound || e["error"] == nil {
		t.Fatalf("unknown sid: %d %v", status, e)
	}
}

func TestAPIExpireVerification(t *testing.T) {
	ts := newTestServer(t)
	_, v := startVerification(t, ts.URL, "VAx", url.Values{"To": {"+15550004443"}, "Channel": {"sms"}})
	sid := v["sid"].(string)
	expire := ts.URL + "/api/v1/verifications/" + sid + "/expire"

	var got store.Verification
	if status := apiJSON(t, http.MethodPost, expire, &got); status != http.StatusOK || got.ID != sid || got.Status != "expired" {
		t.Fatalf("expire: %d %+v", status, got)
	}
	// The expired verification is gone from Twilio's point of view too.
	status, body := fetchVerification(t, http.DefaultClient, ts.URL, "VAx", sid)
	wantTwilioError(t, status, body, http.StatusNotFound, 20404)

	var e map[string]any
	if status := apiJSON(t, http.MethodPost, expire, &e); status != http.StatusConflict || e["error"] == nil {
		t.Fatalf("second expire: %d %v", status, e)
	}
	e = nil
	if status := apiJSON(t, http.MethodPost, ts.URL+"/api/v1/verifications/VEunknown/expire", &e); status != http.StatusNotFound || e["error"] == nil {
		t.Fatalf("unknown sid: %d %v", status, e)
	}
}

// Expiring takes verifyMu, so it never lands between a Verify check's read and
// its write.
func TestAPIExpireWaitsForVerifyMutations(t *testing.T) {
	ts := newTestServer(t)
	srv := ts.Config.Handler.(*Server)
	_, v := startVerification(t, ts.URL, "VAx", url.Values{"To": {"+15550004444"}, "Channel": {"sms"}})
	srv.verifyMu.Lock()
	done := make(chan int, 1)
	go func() {
		resp, err := http.Post(ts.URL+"/api/v1/verifications/"+v["sid"].(string)+"/expire", "", nil)
		if err != nil {
			t.Error(err)
			done <- 0
			return
		}
		resp.Body.Close()
		done <- resp.StatusCode
	}()
	select {
	case status := <-done:
		srv.verifyMu.Unlock()
		t.Fatalf("expire finished (%d) while a Verify mutation held verifyMu", status)
	case <-time.After(200 * time.Millisecond):
	}
	srv.verifyMu.Unlock()
	select {
	case status := <-done:
		if status != http.StatusOK {
			t.Fatalf("expire: %d", status)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("expire did not finish after verifyMu was released")
	}
}

func TestClearDeletesVerifications(t *testing.T) {
	ts := newTestServer(t)
	startVerification(t, ts.URL, "VAx", url.Values{"To": {"+15550004445"}, "Channel": {"sms"}})
	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/v1/messages", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	var list struct{ Verifications []store.Verification }
	if status := apiJSON(t, http.MethodGet, ts.URL+"/api/v1/verifications", &list); status != http.StatusOK || len(list.Verifications) != 0 {
		t.Fatalf("after clear: %d %+v", status, list.Verifications)
	}
}

func TestWaitForEmailRecipient(t *testing.T) {
	ts := newTestServer(t)
	resp, err := http.Post(ts.URL+"/api/v1/send", "application/json", strings.NewReader(`{"to":"Ada@Example.com","body":"Your code is 482913"}`))
	if err != nil {
		t.Fatal(err)
	}
	var sent store.Message
	decode(t, resp, &sent)

	var got store.Message
	if status := apiJSON(t, http.MethodGet, ts.URL+"/api/v1/wait?timeout=2s&to="+url.QueryEscape("ada@example.com"), &got); status != http.StatusOK ||
		got.ID != sent.ID || got.OTP != "482913" {
		t.Fatalf("wait for an email: %d %+v", status, got)
	}

	var e map[string]any
	if status := apiJSON(t, http.MethodGet, ts.URL+"/api/v1/wait?timeout=2s&to=nobody", &e); status != http.StatusBadRequest || e["error"] == nil {
		t.Fatalf("?to=nobody: %d %v", status, e)
	}
}

// A message to an email address that arrives while wait is blocked is matched
// by recipient too, not just the one already stored when wait was called.
func TestWaitBlocksUntilEmailMessageArrives(t *testing.T) {
	ts := newTestServer(t)
	got := make(chan store.Message, 1)
	go func() {
		var m store.Message
		if status := apiJSON(t, http.MethodGet, ts.URL+"/api/v1/wait?timeout=5s&to="+url.QueryEscape("ada@example.com"), &m); status != http.StatusOK {
			t.Errorf("wait: %d", status)
		}
		got <- m
	}()
	time.Sleep(50 * time.Millisecond)
	for _, body := range []string{
		`{"to":"grace@example.com","body":"other address 111111"}`,
		`{"to":"+15551234567","body":"a number 222222"}`,
		`{"to":"Ada@Example.com","body":"Your code is 482913"}`,
	} {
		resp, err := http.Post(ts.URL+"/api/v1/send", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
	select {
	case m := <-got:
		if m.OTP != "482913" || !strings.EqualFold(m.To, "ada@example.com") {
			t.Fatalf("got %+v", m)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("wait did not return")
	}
}
