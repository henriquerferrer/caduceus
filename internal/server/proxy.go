package server

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// intercepted reports whether Caduceus answers for host when used as an
// HTTPS proxy. Only hosts carrying an emulated API qualify, so a provider's
// other products keep their own hosts and are tunnelled through untouched.
// Twilio's messaging API lives on api.twilio.com and Verify on
// verify.twilio.com, each with regional forms such as
// api.dublin.ie1.twilio.com.
func intercepted(host string) bool {
	host = strings.ToLower(host)
	return (strings.HasPrefix(host, "api.") || strings.HasPrefix(host, "verify.")) &&
		strings.HasSuffix(host, ".twilio.com")
}

// CA is the local certificate authority that signs certificates for the
// provider hosts. Applications trust it through its PEM file.
type CA struct {
	PEM  []byte
	Path string

	cert   *x509.Certificate
	key    *ecdsa.PrivateKey
	mu     sync.Mutex
	leaves map[string]*tls.Certificate
}

// LoadCA reads the CA from dir, creating it on first use.
func LoadCA(dir string) (*CA, error) {
	certPath, keyPath := filepath.Join(dir, "ca.pem"), filepath.Join(dir, "ca-key.pem")
	certPEM, err := os.ReadFile(certPath)
	if errors.Is(err, os.ErrNotExist) {
		return createCA(dir, certPath, keyPath)
	}
	if err != nil {
		return nil, err
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, err
	}
	certBlock, _ := pem.Decode(certPEM)
	keyBlock, _ := pem.Decode(keyPEM)
	if certBlock == nil || keyBlock == nil {
		return nil, fmt.Errorf("invalid PEM in %s", dir)
	}
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, err
	}
	key, err := x509.ParseECPrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, err
	}
	return &CA{PEM: certPEM, Path: certPath, cert: cert, key: key, leaves: map[string]*tls.Certificate{}}, nil
}

func createCA(dir, certPath, keyPath string) (*CA, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          randomSerial(),
		Subject:               pkix.Name{CommonName: "Caduceus Local CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		IsCA:                  true,
		BasicConstraintsValid: true,
		MaxPathLenZero:        true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		return nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return &CA{PEM: certPEM, Path: certPath, cert: cert, key: key, leaves: map[string]*tls.Certificate{}}, nil
}

func randomSerial() *big.Int {
	n, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	return n
}

// leaf returns a certificate for host signed by the CA, creating it once.
func (ca *CA) leaf(host string) (*tls.Certificate, error) {
	ca.mu.Lock()
	defer ca.mu.Unlock()
	if c, ok := ca.leaves[host]; ok {
		return c, nil
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: randomSerial(),
		Subject:      pkix.Name{CommonName: host},
		DNSNames:     []string{host},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(0, 0, 365),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		return nil, err
	}
	c := &tls.Certificate{Certificate: [][]byte{der, ca.cert.Raw}, PrivateKey: key}
	ca.leaves[host] = c
	return c, nil
}

// connListener feeds hijacked proxy connections to an http.Server.
type connListener struct{ conns chan net.Conn }

func (l *connListener) Accept() (net.Conn, error) { return <-l.conns, nil }
func (l *connListener) Close() error              { return nil }
func (l *connListener) Addr() net.Addr            { return &net.TCPAddr{} }

// handleConnect serves an HTTPS proxy request. Provider hosts are answered by
// Caduceus over TLS; any other host gets a plain tunnel to the real server.
func (s *Server) handleConnect(w http.ResponseWriter, r *http.Request) {
	host, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		host = r.Host
	}
	capture := s.opts.CA != nil && intercepted(host)

	var upstream net.Conn
	if !capture {
		if upstream, err = net.DialTimeout("tcp", r.Host, 10*time.Second); err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "proxying unsupported", http.StatusInternalServerError)
		return
	}
	conn, _, err := hj.Hijack()
	if err != nil {
		return
	}
	conn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))

	if capture {
		s.tunnelled.conns <- tls.Server(conn, &tls.Config{
			NextProtos: []string{"http/1.1"},
			GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
				return s.opts.CA.leaf(cmpOr(hello.ServerName, host))
			},
		})
		return
	}
	go func() {
		io.Copy(upstream, conn)
		upstream.Close()
	}()
	io.Copy(conn, upstream)
	conn.Close()
}

// forward relays a plain-HTTP proxy request for a host Caduceus doesn't emulate.
func (s *Server) forward(w http.ResponseWriter, r *http.Request) {
	r.RequestURI = ""
	resp, err := s.direct.RoundTrip(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	for k, v := range resp.Header {
		w.Header()[k] = v
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}
