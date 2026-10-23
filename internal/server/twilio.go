package server

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/henriquerferrer/caduceus/internal/store"
)

const twilioAPIVersion = "2010-04-01"

// twilioMessage renders a message as Twilio's Message resource.
func twilioMessage(m store.Message) map[string]any {
	uri := fmt.Sprintf("/%s/Accounts/%s/Messages/%s.json", twilioAPIVersion, m.AccountSID, m.ID)
	direction := "outbound-api"
	if m.Direction == store.Inbound {
		direction = "inbound"
	}
	var sent any
	if m.Status != "queued" {
		sent = m.UpdatedAt.Format(time.RFC1123Z)
	}
	return map[string]any{
		"sid":                   m.ID,
		"account_sid":           m.AccountSID,
		"api_version":           twilioAPIVersion,
		"to":                    m.To,
		"from":                  m.From,
		"body":                  m.Body,
		"status":                m.Status,
		"direction":             direction,
		"num_segments":          strconv.Itoa(m.Segments),
		"num_media":             "0",
		"date_created":          m.CreatedAt.Format(time.RFC1123Z),
		"date_updated":          m.UpdatedAt.Format(time.RFC1123Z),
		"date_sent":             sent,
		"price":                 nil,
		"price_unit":            "USD",
		"error_code":            nil,
		"error_message":         nil,
		"messaging_service_sid": nil,
		"uri":                   uri,
		"subresource_uris":      map[string]string{"media": strings.TrimSuffix(uri, ".json") + "/Media.json"},
	}
}

func twilioError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, http.StatusBadRequest, map[string]any{
		"code":      code,
		"message":   msg,
		"more_info": fmt.Sprintf("https://www.twilio.com/docs/errors/%d", code),
		"status":    http.StatusBadRequest,
	})
}

func twilioStatusForm(m store.Message) url.Values {
	return url.Values{
		"MessageSid":    {m.ID},
		"SmsSid":        {m.ID},
		"MessageStatus": {m.Status},
		"SmsStatus":     {m.Status},
		"AccountSid":    {m.AccountSID},
		"From":          {m.From},
		"To":            {m.To},
		"ApiVersion":    {twilioAPIVersion},
	}
}

func twilioInboundForm(m store.Message) url.Values {
	return url.Values{
		"MessageSid":  {m.ID},
		"SmsSid":      {m.ID},
		"AccountSid":  {m.AccountSID},
		"From":        {m.From},
		"To":          {m.To},
		"Body":        {m.Body},
		"NumMedia":    {"0"},
		"NumSegments": {strconv.Itoa(m.Segments)},
		"ApiVersion":  {twilioAPIVersion},
	}
}

func (s *Server) twilioRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /2010-04-01/Accounts/{account}/Messages.json", s.twilioCreate)
	mux.HandleFunc("GET /2010-04-01/Accounts/{account}/Messages.json", s.twilioList)
	mux.HandleFunc("GET /2010-04-01/Accounts/{account}/Messages/{sid}", s.twilioFetch)
}

func (s *Server) twilioCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		twilioError(w, 20001, "Could not parse the request body.")
		return
	}
	to, body := r.PostForm.Get("To"), r.PostForm.Get("Body")
	from := cmpOr(r.PostForm.Get("From"), r.PostForm.Get("MessagingServiceSid"))
	switch {
	case to == "":
		twilioError(w, 21604, "A 'To' phone number is required.")
		return
	case from == "":
		twilioError(w, 21603, "A 'From' phone number is required.")
		return
	case body == "":
		twilioError(w, 21602, "Message body is required.")
		return
	}
	m, err := s.capture(store.Message{
		Provider:       "twilio",
		AccountSID:     r.PathValue("account"),
		To:             to,
		From:           from,
		Body:           body,
		StatusCallback: r.PostForm.Get("StatusCallback"),
	})
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, twilioMessage(m))
}

func (s *Server) twilioFetch(w http.ResponseWriter, r *http.Request) {
	m, err := s.store.Get(strings.TrimSuffix(r.PathValue("sid"), ".json"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]any{
			"code":      20404,
			"message":   "The requested resource " + r.URL.Path + " was not found",
			"more_info": "https://www.twilio.com/docs/errors/20404",
			"status":    http.StatusNotFound,
		})
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, twilioMessage(m))
}

func (s *Server) twilioList(w http.ResponseWriter, r *http.Request) {
	msgs, err := s.store.List(store.Filter{To: r.URL.Query().Get("To")})
	if err != nil {
		s.fail(w, err)
		return
	}
	out := make([]map[string]any, len(msgs))
	for i, m := range msgs {
		out[i] = twilioMessage(m)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"messages":          out,
		"page":              0,
		"page_size":         len(out),
		"start":             0,
		"end":               max(len(out)-1, 0),
		"uri":               r.URL.RequestURI(),
		"first_page_uri":    r.URL.RequestURI(),
		"next_page_uri":     nil,
		"previous_page_uri": nil,
	})
}
