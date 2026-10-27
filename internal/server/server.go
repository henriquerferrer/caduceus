// Package server exposes the provider-compatible ingest endpoints, the
// Caduceus REST API and the web UI.
package server

import (
	_ "embed"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/henriquerferrer/caduceus/internal/sms"
	"github.com/henriquerferrer/caduceus/internal/store"
)

//go:embed ui/index.html
var indexHTML []byte

type Options struct {
	// DeliveryDelay is the pause before each simulated status change
	// (queued → sent → delivered). Negative disables the simulation.
	DeliveryDelay time.Duration
	// InboundWebhook is where simulated inbound messages are posted when the
	// request doesn't name a webhook itself.
	InboundWebhook string
	// CA enables proxy mode: with it, Caduceus answers HTTPS proxy requests
	// for provider hosts itself.
	CA     *CA
	Logger *slog.Logger
}

type Server struct {
	store  *store.Store
	opts   Options
	log    *slog.Logger
	client *http.Client
	mux    *http.ServeMux
	// messaging and verify hold only the emulated provider routes, for
	// api.* and verify.* hosts respectively. Proxied requests that match none
	// of their host's routes go to the real provider.
	messaging *http.ServeMux
	verify    *http.ServeMux
	// verifyMu serialises every Verify read-modify-write, and the inbox
	// clear that deletes verifications, so concurrent requests never create
	// two pending verifications, lose an attempt or lose their verification
	// between its read and its write.
	//
	// The REST reads of verifications store an expiry without taking it, so a
	// handler's write is conditional on the status the handler read
	// (store.UpdateVerification). A write that returns store.ErrStale changed
	// nothing: the handler reads again and answers from the current state.
	verifyMu sync.Mutex
	upstream *httputil.ReverseProxy
	// direct reaches real servers for proxied traffic Caduceus doesn't capture.
	direct    *http.Transport
	tunnelled *connListener
}

func New(st *store.Store, opts Options) *Server {
	s := &Server{
		store:     st,
		opts:      opts,
		log:       opts.Logger,
		client:    &http.Client{Timeout: 10 * time.Second},
		mux:       http.NewServeMux(),
		messaging: http.NewServeMux(),
		verify:    http.NewServeMux(),
		direct:    &http.Transport{},
	}
	if s.log == nil {
		s.log = slog.Default()
	}
	s.upstream = &httputil.ReverseProxy{
		Transport: s.direct,
		Rewrite: func(r *httputil.ProxyRequest) {
			r.Out.URL.Scheme = "https"
			r.Out.URL.Host = r.In.Host
		},
	}

	s.mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(indexHTML)
	})
	if opts.CA != nil {
		s.tunnelled = &connListener{conns: make(chan net.Conn)}
		go http.Serve(s.tunnelled, http.HandlerFunc(s.serveTunnelled))
		s.mux.HandleFunc("GET /ca.pem", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/x-pem-file")
			w.Write(opts.CA.PEM)
		})
	}

	s.mux.HandleFunc("POST /api/v1/send", s.handleSend)
	s.mux.HandleFunc("POST /api/v1/inbound", s.handleInbound)
	s.mux.HandleFunc("GET /api/v1/messages", s.handleList)
	s.mux.HandleFunc("DELETE /api/v1/messages", s.handleClear)
	s.mux.HandleFunc("GET /api/v1/messages/{id}", s.handleGet)
	s.mux.HandleFunc("POST /api/v1/messages/{id}/status", s.handleSetStatus)
	s.mux.HandleFunc("GET /api/v1/wait", s.handleWait)
	s.mux.HandleFunc("GET /api/v1/verifications", s.handleListVerifications)
	s.mux.HandleFunc("GET /api/v1/verifications/{sid}", s.handleGetVerification)
	s.mux.HandleFunc("POST /api/v1/verifications/{sid}/expire", s.handleExpireVerification)
	s.mux.HandleFunc("GET /api/v1/events", s.handleEvents)

	// Provider routes are also served directly, for clients that can set a base URL.
	for _, mux := range []*http.ServeMux{s.mux, s.messaging} {
		s.twilioRoutes(mux)
	}
	for _, mux := range []*http.ServeMux{s.mux, s.verify} {
		s.verifyRoutes(mux)
	}
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodConnect:
		s.handleConnect(w, r)
	case r.URL.IsAbs():
		// A plain-HTTP proxy request: never the Caduceus API or UI.
		s.serveProvider(w, r, r.URL.Hostname(), s.forward)
	default:
		s.mux.ServeHTTP(w, r)
	}
}

// serveTunnelled handles a request that arrived through the HTTPS proxy for a
// provider host.
func (s *Server) serveTunnelled(w http.ResponseWriter, r *http.Request) {
	host, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		host = r.Host
	}
	s.serveProvider(w, r, host, s.upstream.ServeHTTP)
}

// providerRoutes returns the emulated routes for host: Verify's on a
// verify.* host, Messaging's on an api.* host, none on any other host.
func (s *Server) providerRoutes(host string) *http.ServeMux {
	switch {
	case !intercepted(host):
		return nil
	case strings.HasPrefix(strings.ToLower(host), "verify."):
		return s.verify
	default:
		return s.messaging
	}
}

// serveProvider answers a proxied request with the emulated route for its
// host, and passes any other request to the real provider through pass.
func (s *Server) serveProvider(w http.ResponseWriter, r *http.Request, host string, pass http.HandlerFunc) {
	if routes := s.providerRoutes(host); routes != nil {
		if _, pattern := routes.Handler(r); pattern != "" {
			routes.ServeHTTP(w, r)
			return
		}
		s.log.Info("passing through", "host", host, "method", r.Method, "path", r.URL.Path)
	}
	pass(w, r)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// fail reports a storage error; a missing message is the only expected one.
func (s *Server) fail(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "message not found")
		return
	}
	s.log.Error("storage failed", "err", err)
	writeError(w, http.StatusInternalServerError, "storage error")
}

// capture stores an outbound message and starts its simulated delivery. The
// OTP is read from the body unless m already carries one.
func (s *Server) capture(m store.Message) (store.Message, error) {
	m, err := s.store.Add(outbound(m))
	if err != nil {
		return m, err
	}
	s.captured(m)
	return m, nil
}

// outbound fills in what capture sets on a message before it is stored.
func outbound(m store.Message) store.Message {
	m.ID = store.NewSID("SM")
	m.Direction = store.Outbound
	m.Status = "queued"
	if m.OTP == "" {
		m.OTP = sms.ExtractOTP(m.Body)
	}
	m.Encoding, m.Segments = sms.Segments(m.Body)
	return m
}

// captured logs a stored outbound message and starts its simulated delivery.
func (s *Server) captured(m store.Message) {
	s.log.Info("captured", "provider", m.Provider, "to", m.To, "id", m.ID)
	if s.opts.DeliveryDelay >= 0 {
		go s.simulateDelivery(m.ID)
	}
}

func (s *Server) simulateDelivery(id string) {
	for _, status := range []string{"sent", "delivered"} {
		time.Sleep(s.opts.DeliveryDelay)
		// A manual status override or a clear ends the simulation.
		if cur, err := s.store.Get(id); err != nil || cur.Status == "failed" || cur.Status == "undelivered" {
			return
		}
		s.setStatus(id, status)
	}
}

func (s *Server) setStatus(id, status string) (store.Message, error) {
	m, err := s.store.SetStatus(id, status)
	if err == nil && m.StatusCallback != "" {
		s.postForm(m.StatusCallback, twilioStatusForm(m))
	}
	return m, err
}

func (s *Server) postForm(target string, form url.Values) (int, error) {
	resp, err := s.client.Post(target, "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		s.log.Warn("webhook failed", "url", target, "err", err)
		return 0, err
	}
	resp.Body.Close()
	return resp.StatusCode, nil
}

func (s *Server) handleSend(w http.ResponseWriter, r *http.Request) {
	var in struct {
		To, From, Body string
		StatusCallback string `json:"status_callback"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if in.To == "" || in.Body == "" {
		writeError(w, http.StatusBadRequest, `"to" and "body" are required`)
		return
	}
	m, err := s.capture(store.Message{Provider: "generic", To: in.To, From: in.From, Body: in.Body, StatusCallback: in.StatusCallback})
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, m)
}

// handleInbound simulates a reply from a phone: it records the message and
// posts a Twilio-shaped inbound webhook to the application.
func (s *Server) handleInbound(w http.ResponseWriter, r *http.Request) {
	var in struct{ To, From, Body, Webhook string }
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if in.From == "" || in.Body == "" {
		writeError(w, http.StatusBadRequest, `"from" and "body" are required`)
		return
	}
	m := store.Message{
		ID: store.NewSID("SM"), Provider: "simulated", Direction: store.Inbound,
		From: in.From, To: in.To, Body: in.Body, Status: "received",
	}
	m.Encoding, m.Segments = sms.Segments(m.Body)
	m, err := s.store.Add(m)
	if err != nil {
		s.fail(w, err)
		return
	}

	out := struct {
		store.Message
		WebhookStatus int    `json:"webhook_status,omitempty"`
		WebhookError  string `json:"webhook_error,omitempty"`
	}{Message: m}
	if target := cmpOr(in.Webhook, s.opts.InboundWebhook); target != "" {
		status, err := s.postForm(target, twilioInboundForm(m))
		out.WebhookStatus = status
		if err != nil {
			out.WebhookError = err.Error()
		}
	}
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	msgs, err := s.store.List(store.Filter{To: q.Get("to"), Direction: q.Get("direction"), Query: q.Get("q")})
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": msgs})
}

func (s *Server) handleGet(w http.ResponseWriter, r *http.Request) {
	m, err := s.store.Get(r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, m)
}

func (s *Server) handleClear(w http.ResponseWriter, r *http.Request) {
	// Clear deletes verifications too, so it is a Verify mutation: it must not
	// land between a Verify handler's read and its write.
	s.verifyMu.Lock()
	defer s.verifyMu.Unlock()
	if err := s.store.Clear(); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleSetStatus(w http.ResponseWriter, r *http.Request) {
	var in struct{ Status string }
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Status == "" {
		writeError(w, http.StatusBadRequest, `"status" is required`)
		return
	}
	m, err := s.setStatus(r.PathValue("id"), in.Status)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, m)
}

// failVerification reports a storage error from a verification route.
func (s *Server) failVerification(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "verification not found")
		return
	}
	s.fail(w, err)
}

func (s *Server) handleListVerifications(w http.ResponseWriter, r *http.Request) {
	vs, err := s.store.ListVerifications(store.Filter{To: r.URL.Query().Get("to")})
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"verifications": vs})
}

func (s *Server) handleGetVerification(w http.ResponseWriter, r *http.Request) {
	v, err := s.store.GetVerification(r.PathValue("sid"))
	if err != nil {
		s.failVerification(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// handleExpireVerification moves a pending verification's expiry to now, so
// tests reach the expired path without waiting out the TTL.
func (s *Server) handleExpireVerification(w http.ResponseWriter, r *http.Request) {
	s.verifyMu.Lock()
	defer s.verifyMu.Unlock()
	for {
		v, err := s.store.GetVerification(r.PathValue("sid"))
		if err != nil {
			s.failVerification(w, err)
			return
		}
		if v.Status != "pending" {
			writeError(w, http.StatusConflict, "verification is "+v.Status+", not pending")
			return
		}
		v.ExpiresAt = time.Now()
		_, err = s.store.UpdateVerification(v, v.Status)
		if errors.Is(err, store.ErrStale) {
			continue // see verifyMu: answer from the current state
		}
		if err != nil {
			s.failVerification(w, err)
			return
		}
		// Reading it back applies the expiry, storing the status as expired.
		if v, err = s.store.GetVerification(v.ID); err != nil {
			s.failVerification(w, err)
			return
		}
		writeJSON(w, http.StatusOK, v)
		return
	}
}

// handleWait returns the newest outbound message to a number or an email
// address, blocking until one arrives if there is none yet. Tests clear the inbox, trigger a send and
// call this to read the code.
func (s *Server) handleWait(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := store.Filter{To: q.Get("to"), Direction: store.Outbound, Limit: 1}
	if store.Digits(f.To) == "" && !strings.Contains(f.To, "@") {
		writeError(w, http.StatusBadRequest, `"to" must be a phone number or an email address`)
		return
	}
	if v := q.Get("since"); v != "" {
		t, err := time.Parse(time.RFC3339Nano, v)
		if err != nil {
			writeError(w, http.StatusBadRequest, `"since" must be an RFC 3339 timestamp`)
			return
		}
		f.Since = t
	}
	timeout := 30 * time.Second
	if v := q.Get("timeout"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			writeError(w, http.StatusBadRequest, `"timeout" must be a duration such as 10s`)
			return
		}
		timeout = min(d, 2*time.Minute)
	}

	// Subscribe before looking, so a message can't slip between the two.
	events, cancel := s.store.Subscribe()
	defer cancel()
	found, err := s.store.List(f)
	if err != nil {
		s.fail(w, err)
		return
	}
	if len(found) > 0 {
		writeJSON(w, http.StatusOK, found[0])
		return
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case e := <-events:
			if e.Type == "message" && f.Matches(*e.Message) {
				writeJSON(w, http.StatusOK, e.Message)
				return
			}
		case <-timer.C:
			writeError(w, http.StatusRequestTimeout, "no message arrived before the timeout")
			return
		case <-r.Context().Done():
			return
		}
	}
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	events, cancel := s.store.Subscribe()
	defer cancel()
	flusher.Flush()
	keepalive := time.NewTicker(25 * time.Second)
	defer keepalive.Stop()
	for {
		select {
		case e := <-events:
			b, _ := json.Marshal(e)
			w.Write([]byte("data: " + string(b) + "\n\n"))
		case <-keepalive.C:
			w.Write([]byte(": keepalive\n\n"))
		case <-r.Context().Done():
			return
		}
		flusher.Flush()
	}
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
