// Package store keeps captured messages in SQLite and notifies subscribers.
package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const (
	Outbound = "outbound"
	Inbound  = "inbound"
)

var ErrNotFound = errors.New("message not found")

const schema = `
CREATE TABLE IF NOT EXISTS messages (
	seq              INTEGER PRIMARY KEY AUTOINCREMENT,
	id               TEXT NOT NULL UNIQUE,
	provider         TEXT NOT NULL,
	direction        TEXT NOT NULL,
	account_sid      TEXT NOT NULL,
	sender           TEXT NOT NULL,
	recipient        TEXT NOT NULL,
	recipient_digits TEXT NOT NULL,
	body             TEXT NOT NULL,
	status           TEXT NOT NULL,
	otp              TEXT NOT NULL,
	encoding         TEXT NOT NULL,
	segments         INTEGER NOT NULL,
	status_callback  TEXT NOT NULL,
	created_at       INTEGER NOT NULL,
	updated_at       INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS messages_recipient ON messages (recipient_digits);`

const columns = `id, provider, direction, account_sid, sender, recipient, body, status, otp,
	encoding, segments, status_callback, created_at, updated_at`

type Message struct {
	ID             string    `json:"id"`
	Provider       string    `json:"provider"`
	Direction      string    `json:"direction"`
	AccountSID     string    `json:"account_sid,omitempty"`
	From           string    `json:"from"`
	To             string    `json:"to"`
	Body           string    `json:"body"`
	Status         string    `json:"status"`
	OTP            string    `json:"otp,omitempty"`
	Encoding       string    `json:"encoding"`
	Segments       int       `json:"segments"`
	StatusCallback string    `json:"status_callback,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// Event is published on every change. Type is "message", "status" or "clear".
type Event struct {
	Type    string   `json:"type"`
	Message *Message `json:"message,omitempty"`
}

type Filter struct {
	To        string // compared by digits only, so "+1 (555) 000" matches "1555000"
	Direction string
	Query     string
	Since     time.Time
	Limit     int
}

type Store struct {
	db   *sql.DB
	max  int
	temp string // database file to delete on Close; empty when persistent

	mu   sync.RWMutex
	subs map[chan Event]struct{}
}

// Open opens the database at path, creating it if needed. An empty path uses
// a temporary database that is deleted on Close. max is the number of
// messages kept before the oldest are pruned; 0 keeps everything.
func Open(path string, max int) (*Store, error) {
	temp := ""
	if path == "" {
		f, err := os.CreateTemp("", "caduceus-*.db")
		if err != nil {
			return nil, err
		}
		f.Close()
		path, temp = f.Name(), f.Name()
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	// One connection serialises writes, which is all this workload needs.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db, max: max, temp: temp, subs: map[chan Event]struct{}{}}, nil
}

func (s *Store) Close() error {
	err := s.db.Close()
	if s.temp != "" {
		os.Remove(s.temp)
	}
	return err
}

// NewSID returns a Twilio-shaped identifier: prefix plus 32 hex characters.
func NewSID(prefix string) string {
	b := make([]byte, 16)
	rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

// Digits strips everything but digits, for comparing phone numbers.
func Digits(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, s)
}

func (s *Store) Add(m Message) (Message, error) {
	// Timestamps are stored in nanoseconds; round-trip through the same
	// precision so the returned message equals what a later read gives.
	now := time.Unix(0, time.Now().UnixNano()).UTC()
	m.CreatedAt, m.UpdatedAt = now, now
	_, err := s.db.Exec(`INSERT INTO messages (`+columns+`, recipient_digits)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.ID, m.Provider, m.Direction, m.AccountSID, m.From, m.To, m.Body, m.Status, m.OTP,
		m.Encoding, m.Segments, m.StatusCallback, now.UnixNano(), now.UnixNano(), Digits(m.To))
	if err != nil {
		return Message{}, err
	}
	if s.max > 0 {
		if _, err := s.db.Exec(`DELETE FROM messages WHERE seq <= (SELECT MAX(seq) FROM messages) - ?`, s.max); err != nil {
			return Message{}, err
		}
	}
	s.publish(Event{Type: "message", Message: &m})
	return m, nil
}

type scanner interface{ Scan(...any) error }

func scan(row scanner) (Message, error) {
	var m Message
	var created, updated int64
	err := row.Scan(&m.ID, &m.Provider, &m.Direction, &m.AccountSID, &m.From, &m.To, &m.Body, &m.Status,
		&m.OTP, &m.Encoding, &m.Segments, &m.StatusCallback, &created, &updated)
	m.CreatedAt, m.UpdatedAt = time.Unix(0, created).UTC(), time.Unix(0, updated).UTC()
	return m, err
}

func (s *Store) Get(id string) (Message, error) {
	m, err := scan(s.db.QueryRow(`SELECT `+columns+` FROM messages WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Message{}, ErrNotFound
	}
	return m, err
}

// List returns matching messages, newest first.
func (s *Store) List(f Filter) ([]Message, error) {
	where, args := []string{"1"}, []any{}
	if to := Digits(f.To); to != "" {
		where, args = append(where, "recipient_digits = ?"), append(args, to)
	}
	if f.Direction != "" {
		where, args = append(where, "direction = ?"), append(args, f.Direction)
	}
	if !f.Since.IsZero() {
		where, args = append(where, "created_at > ?"), append(args, f.Since.UnixNano())
	}
	if f.Query != "" {
		like := "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(f.Query) + "%"
		where = append(where, `(body LIKE ? ESCAPE '\' OR sender LIKE ? ESCAPE '\' OR recipient LIKE ? ESCAPE '\')`)
		args = append(args, like, like, like)
	}
	query := `SELECT ` + columns + ` FROM messages WHERE ` + strings.Join(where, " AND ") + ` ORDER BY seq DESC`
	if f.Limit > 0 {
		query += ` LIMIT ?`
		args = append(args, f.Limit)
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Message{}
	for rows.Next() {
		m, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// Matches reports whether a newly published message passes the filter's To,
// Direction and Since conditions.
func (f Filter) Matches(m Message) bool {
	if to := Digits(f.To); to != "" && Digits(m.To) != to {
		return false
	}
	if f.Direction != "" && m.Direction != f.Direction {
		return false
	}
	return f.Since.IsZero() || m.CreatedAt.After(f.Since)
}

func (s *Store) SetStatus(id, status string) (Message, error) {
	res, err := s.db.Exec(`UPDATE messages SET status = ?, updated_at = ? WHERE id = ?`, status, time.Now().UnixNano(), id)
	if err != nil {
		return Message{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Message{}, ErrNotFound
	}
	m, err := s.Get(id)
	if err != nil {
		return Message{}, err
	}
	s.publish(Event{Type: "status", Message: &m})
	return m, nil
}

func (s *Store) Clear() error {
	if _, err := s.db.Exec(`DELETE FROM messages`); err != nil {
		return err
	}
	s.publish(Event{Type: "clear"})
	return nil
}

// Subscribe returns a channel of future events and a function to stop them.
// A subscriber that falls more than 64 events behind loses events.
func (s *Store) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 64)
	s.mu.Lock()
	s.subs[ch] = struct{}{}
	s.mu.Unlock()
	return ch, func() {
		s.mu.Lock()
		delete(s.subs, ch)
		s.mu.Unlock()
	}
}

func (s *Store) publish(e Event) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for ch := range s.subs {
		select {
		case ch <- e:
		default:
		}
	}
}
