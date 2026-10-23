//go:build e2e

// Package e2e runs the built caduceus binary and drives it the way an
// application under test would: through the HTTPS proxy, with no code changes.
package e2e

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type instance struct {
	base   string // http://127.0.0.1:port
	caPath string
}

// start builds the binary once per test and runs it on a free port.
func start(t *testing.T) instance {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "caduceus")
	build := exec.Command("go", "build", "-o", bin, "../cmd/caduceus")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()

	data := filepath.Join(dir, "data")
	cmd := exec.Command(bin, "-listen", addr, "-data-dir", data, "-delivery-delay", "-1s")
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cmd.Process.Signal(os.Interrupt)
		cmd.Wait()
	})

	base := "http://" + addr
	deadline := time.Now().Add(10 * time.Second)
	for {
		resp, err := http.Get(base + "/api/v1/messages")
		if err == nil {
			resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("caduceus did not start: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	return instance{base: base, caPath: filepath.Join(data, "ca.pem")}
}

// wait asks caduceus for the newest message sent to the number.
func (c instance) wait(t *testing.T, to string) map[string]any {
	t.Helper()
	resp, err := http.Get(c.base + "/api/v1/wait?timeout=10s&to=" + url.QueryEscape(to))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("wait: status %d", resp.StatusCode)
	}
	var m map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		t.Fatal(err)
	}
	return m
}

// A client that only knows "use this proxy and trust this CA" reaches the
// emulated Twilio API at its real hostname.
func TestProxyCapturesTwilioMessage(t *testing.T) {
	c := start(t)
	pem, err := os.ReadFile(c.caPath)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pem) {
		t.Fatal("ca.pem holds no certificate")
	}
	proxy, _ := url.Parse(c.base)
	client := &http.Client{Transport: &http.Transport{
		Proxy:           http.ProxyURL(proxy),
		TLSClientConfig: &tls.Config{RootCAs: roots},
	}}

	form := url.Values{"To": {"+15550001111"}, "From": {"+15559990000"}, "Body": {"Your code is 482913"}}
	req, _ := http.NewRequest("POST", "https://api.twilio.com/2010-04-01/Accounts/ACtest/Messages.json", strings.NewReader(form.Encode()))
	req.SetBasicAuth("ACtest", "secret")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("send: status %d", resp.StatusCode)
	}

	if got := c.wait(t, "+15550001111")["otp"]; got != "482913" {
		t.Fatalf("otp = %v, want 482913", got)
	}
}

// The official twilio Node SDK, unmodified, configured only by environment.
func TestTwilioNodeSDK(t *testing.T) {
	if _, err := os.Stat("twilio-node/node_modules/twilio"); err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal("twilio-node is not installed; run npm ci in e2e/twilio-node")
		}
		t.Skip("run `npm ci` in e2e/twilio-node to enable")
	}
	c := start(t)
	to := "+15550002222"
	cmd := exec.Command("node", "send.mjs", to, "Code 771204")
	cmd.Dir = "twilio-node"
	cmd.Env = append(os.Environ(),
		"HTTP_PROXY="+c.base,
		"HTTPS_PROXY="+c.base,
		"NODE_EXTRA_CA_CERTS="+c.caPath,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("send.mjs: %v\n%s", err, out)
	}
	if got := c.wait(t, to)["otp"]; got != "771204" {
		t.Fatalf("otp = %v, want 771204", got)
	}
}
