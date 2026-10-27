package server

import (
	"fmt"
	"net"
	"net/http"
	"strings"
)

// builtinHosts are the names the local listener answers without
// -allowed-hosts.
var builtinHosts = []string{"localhost", "127.0.0.1", "::1"}

// SplitHosts splits a comma-separated host list, as -allowed-hosts and
// CADUCEUS_ALLOWED_HOSTS take it, dropping blank entries.
func SplitHosts(s string) []string {
	var hosts []string
	for _, h := range strings.Split(s, ",") {
		if h = strings.TrimSpace(h); h != "" {
			hosts = append(hosts, h)
		}
	}
	return hosts
}

// hostName reduces a Host header, or an allowed-hosts entry, to the name it
// is compared by: no port, no IPv6 brackets, lower case, one trailing dot
// dropped.
func hostName(h string) string {
	if name, _, err := net.SplitHostPort(h); err == nil {
		h = name
	}
	h = strings.TrimSuffix(strings.TrimPrefix(h, "["), "]")
	return strings.TrimSuffix(strings.ToLower(h), ".")
}

// allowedHosts returns the set of names the local listener answers.
func allowedHosts(extra []string) map[string]bool {
	set := make(map[string]bool)
	for _, list := range [][]string{builtinHosts, extra} {
		for _, h := range list {
			if name := hostName(strings.TrimSpace(h)); name != "" {
				set[name] = true
			}
		}
	}
	return set
}

// refusal says why a request to the local listener is refused, or returns
// "" when it may be served. Requests through the proxy never reach it.
//
// A Host outside the allowed names is how a DNS-rebound page reaches
// Caduceus; an Origin or Sec-Fetch-Site from another site is a cross-site
// request from the developer's browser.
func (s *Server) refusal(r *http.Request) string {
	if name := hostName(r.Host); !s.allowed[name] {
		return fmt.Sprintf("host %q is not allowed; add it with -allowed-hosts or CADUCEUS_ALLOWED_HOSTS", name)
	}
	if topLevelNavigation(r) {
		return ""
	}
	switch r.Header.Get("Sec-Fetch-Site") {
	case "cross-site", "same-site":
		return "cross-site request refused"
	}
	if origin := r.Header.Get("Origin"); origin != "" && !strings.EqualFold(origin, "http://"+r.Host) {
		return fmt.Sprintf("cross-site request from origin %q refused", origin)
	}
	return ""
}

// topLevelNavigation reports a GET or HEAD that opens a page in a browser
// window, such as following a link to the inbox. A framed navigation is not
// one.
func topLevelNavigation(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	if r.Header.Get("Sec-Fetch-Mode") != "navigate" {
		return false
	}
	dest := r.Header.Get("Sec-Fetch-Dest")
	return dest == "" || dest == "document"
}

// refuse answers a refused request with 403, in Twilio's error shape on the
// provider routes so a base-URL SDK surfaces the message.
func (s *Server) refuse(w http.ResponseWriter, r *http.Request, msg string) {
	s.log.Info("refused", "reason", msg, "host", r.Host, "origin", r.Header.Get("Origin"), "method", r.Method, "path", r.URL.Path)
	if strings.HasPrefix(r.URL.Path, "/"+twilioAPIVersion+"/") || strings.HasPrefix(r.URL.Path, "/v2/") {
		twilioError(w, http.StatusForbidden, 20403, msg)
		return
	}
	writeError(w, http.StatusForbidden, msg)
}
