package server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
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

func TestInterceptsOnlyTheMessagingHost(t *testing.T) {
	for host, want := range map[string]bool{
		"api.twilio.com":            true,
		"api.dublin.ie1.twilio.com": true,
		"verify.twilio.com":         false,
		"lookups.twilio.com":        false,
		"api.twilio.com.evil.test":  false,
		"example.com":               false,
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
