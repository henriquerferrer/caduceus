package server

import (
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/henriquerferrer/caduceus/internal/store"
)

// verifyProvider tags the messages a Twilio Verify send captures.
const verifyProvider = "twilio-verify"

// placeholderAccountSID stands in for the account when the request's
// credentials do not name one (an API key, for instance).
const placeholderAccountSID = "AC00000000000000000000000000000000"

// verifyTime is how Twilio Verify renders dates; the SDK parses them as
// ISO 8601, unlike Messaging's RFC 2822.
const verifyTime = "2006-01-02T15:04:05Z"

// verifyChannels are the Channel values a send accepts, and whether each
// addresses a phone number (the rest address an email).
var verifyChannels = map[string]bool{
	"sms": true, "call": true, "whatsapp": true, "sna": true, "auto": true,
	"email": false,
}

func (s *Server) verifyRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v2/Services/{service}/Verifications", s.verifyStart)
	mux.HandleFunc("GET /v2/Services/{service}/Verifications/{sid}", s.verifyFetch)
	mux.HandleFunc("POST /v2/Services/{service}/Verifications/{sid}", s.verifyUpdate)
}

// twilioVerification renders v as Twilio's Verification resource. Its url
// points where the request was answered.
func twilioVerification(r *http.Request, v store.Verification) map[string]any {
	attempts := make([]map[string]any, len(v.SendAttempts))
	for i, a := range v.SendAttempts {
		attempts[i] = map[string]any{"attempt_sid": a.SID, "channel": a.Channel, "time": a.Time.UTC().Format(verifyTime)}
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	var sna any
	if v.Channel == "sna" {
		// The shape the SDK reads. The SNA flow itself is not emulated:
		// Caduceus serves nothing at this local URL.
		sna = map[string]any{"url": "http://" + localAddr(r) + "/sna/" + v.ID}
	}
	return map[string]any{
		"sid":                v.ID,
		"service_sid":        v.ServiceSID,
		"account_sid":        v.AccountSID,
		"to":                 v.To,
		"channel":            v.Channel,
		"status":             v.Status,
		"valid":              v.Status == "approved",
		"lookup":             map[string]any{},
		"amount":             nil,
		"payee":              nil,
		"send_code_attempts": attempts,
		"date_created":       v.CreatedAt.UTC().Format(verifyTime),
		"date_updated":       v.UpdatedAt.UTC().Format(verifyTime),
		"sna":                sna,
		"url":                scheme + "://" + r.Host + "/v2/Services/" + v.ServiceSID + "/Verifications/" + v.ID,
	}
}

// localAddr is the Caduceus address the request arrived on.
func localAddr(r *http.Request) string {
	if a, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr); ok {
		return a.String()
	}
	return "127.0.0.1"
}

// accountSID is the Basic-auth username when it names an account, else a
// placeholder.
func accountSID(r *http.Request) string {
	if user, _, ok := r.BasicAuth(); ok && strings.HasPrefix(user, "AC") {
		return user
	}
	return placeholderAccountSID
}

// randomCode returns a random 6-digit code, Verify's default length.
func randomCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

func isE164(to string) bool {
	return len(to) > 1 && to[0] == '+' && store.Digits(to[1:]) == to[1:]
}

// invalidStart names the first invalid send parameter, or returns "".
func invalidStart(to, channel string) string {
	phone, known := verifyChannels[channel]
	switch {
	case to == "":
		return "To"
	case channel == "" || !known:
		return "Channel"
	case phone && !isE164(to):
		return "To"
	case !phone && !strings.Contains(to, "@"):
		return "To"
	}
	return ""
}

// verifyStart starts a verification, or resends the code of the live pending
// one for the same service and recipient.
func (s *Server) verifyStart(w http.ResponseWriter, r *http.Request) {
	service := r.PathValue("service")
	if !strings.HasPrefix(service, "VA") {
		twilioNotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		twilioError(w, http.StatusBadRequest, 20001, "Could not parse the request body.")
		return
	}
	to, channel := r.PostForm.Get("To"), r.PostForm.Get("Channel")
	if param := invalidStart(to, channel); param != "" {
		twilioError(w, http.StatusBadRequest, 60200, "Invalid parameter: "+param)
		return
	}

	s.verifyMu.Lock()
	defer s.verifyMu.Unlock()
	attempt := store.SendAttempt{SID: store.NewSID("VL"), Channel: channel, Time: time.Now()}
	v, err := s.store.PendingVerification(service, to)
	switch {
	case err == nil:
		if len(v.SendAttempts) >= store.MaxSendAttempts {
			twilioError(w, http.StatusTooManyRequests, 60203, "Max send attempts reached")
			return
		}
		v.Channel = channel
		v.SendAttempts = append(v.SendAttempts, attempt)
		v, err = s.store.UpdateVerification(v)
	case errors.Is(err, store.ErrNotFound):
		code := r.PostForm.Get("CustomCode")
		if code == "" {
			if code, err = randomCode(); err != nil {
				s.fail(w, err)
				return
			}
		}
		v, err = s.store.AddVerification(store.Verification{
			ID: store.NewSID("VE"), ServiceSID: service, AccountSID: accountSID(r),
			To: to, Channel: channel, Code: code, Status: "pending",
			SendAttempts: []store.SendAttempt{attempt},
		})
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	_, err = s.capture(store.Message{
		Provider: verifyProvider, AccountSID: v.AccountSID, From: "Verify", To: v.To,
		Body: "Your verification code is: " + v.Code, OTP: v.Code,
		Channel: channel, VerificationSID: v.ID,
	})
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, twilioVerification(r, v))
}

// serviceVerification returns the verification the path names, or
// ErrNotFound when it does not exist on the path's service. The service SID
// is the emulation's only tenant boundary.
func (s *Server) serviceVerification(r *http.Request) (store.Verification, error) {
	v, err := s.store.GetVerification(r.PathValue("sid"))
	if err == nil && v.ServiceSID != r.PathValue("service") {
		return store.Verification{}, store.ErrNotFound
	}
	return v, err
}

func (s *Server) verifyFetch(w http.ResponseWriter, r *http.Request) {
	s.verifyMu.Lock()
	defer s.verifyMu.Unlock()
	v, err := s.serviceVerification(r)
	if errors.Is(err, store.ErrNotFound) {
		twilioNotFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, twilioVerification(r, v))
}

// verifyUpdate sets a pending verification's status to canceled or approved.
func (s *Server) verifyUpdate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		twilioError(w, http.StatusBadRequest, 20001, "Could not parse the request body.")
		return
	}
	status := r.PostForm.Get("Status")
	if status != "canceled" && status != "approved" {
		twilioError(w, http.StatusBadRequest, 60200, "Invalid parameter: Status")
		return
	}
	s.verifyMu.Lock()
	defer s.verifyMu.Unlock()
	v, err := s.serviceVerification(r)
	if errors.Is(err, store.ErrNotFound) || (err == nil && v.Status != "pending") {
		twilioNotFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	v.Status = status
	if v, err = s.store.UpdateVerification(v); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, twilioVerification(r, v))
}
