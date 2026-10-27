// Package store keeps captured messages and verifications in SQLite and
// notifies subscribers.
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

var ErrNotFound = errors.New("not found")

// ErrStale reports a conditional write that found its row with a status other
// than the one its caller read: the row changed since that read, and the write
// changed nothing.
var ErrStale = errors.New("changed since it was read")

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
	updated_at       INTEGER NOT NULL,
	channel          TEXT NOT NULL DEFAULT '',
	verification_sid TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS messages_recipient ON messages (recipient_digits);
CREATE TABLE IF NOT EXISTS verifications (
	seq              INTEGER PRIMARY KEY AUTOINCREMENT,
	id               TEXT NOT NULL UNIQUE,
	service_sid      TEXT NOT NULL,
	account_sid      TEXT NOT NULL,
	recipient        TEXT NOT NULL,
	recipient_digits TEXT NOT NULL,
	channel          TEXT NOT NULL,
	code             TEXT NOT NULL,
	status           TEXT NOT NULL,
	send_attempts    TEXT NOT NULL,
	check_attempts   INTEGER NOT NULL,
	created_at       INTEGER NOT NULL,
	updated_at       INTEGER NOT NULL,
	expires_at       INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS verifications_pending ON verifications (service_sid, status);`

const columns = `id, provider, direction, account_sid, sender, recipient, body, status, otp,
	encoding, segments, status_callback, channel, verification_sid, created_at, updated_at`

type Message struct {
	ID              string    `json:"id"`
	Provider        string    `json:"provider"`
	Direction       string    `json:"direction"`
	AccountSID      string    `json:"account_sid,omitempty"`
	From            string    `json:"from"`
	To              string    `json:"to"`
	Body            string    `json:"body"`
	Status          string    `json:"status"`
	OTP             string    `json:"otp,omitempty"`
	Encoding        string    `json:"encoding"`
	Segments        int       `json:"segments"`
	StatusCallback  string    `json:"status_callback,omitempty"`
	Channel         string    `json:"channel,omitempty"`
	VerificationSID string    `json:"verification_sid,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// Event is published on every change. Type is "message", "status",
// "verification" or "clear".
type Event struct {
	Type         string        `json:"type"`
	Message      *Message      `json:"message,omitempty"`
	Verification *Verification `json:"verification,omitempty"`
}

type Filter struct {
	// To is an email address when it contains "@", matched case-insensitively
	// as a whole address; otherwise a phone number, compared by digits only, so
	// "+1 (555) 000" matches "1555000". A phone number never matches an email
	// recipient.
	To        string
	Direction string
	Query     string
	Since     time.Time
	Limit     int
}

type Store struct {
	db   *sql.DB
	max  int
	temp string // database file to delete on Close; empty when persistent

	now func() time.Time // the clock; tests replace it

	mu   sync.RWMutex
	subs map[chan Event]struct{}
}

// Open opens the database at path, creating it if needed. An empty path uses
// a temporary database that is deleted on Close. max is the number of
// messages, and separately of verifications, kept before the oldest are
// pruned; 0 keeps everything. A database created before verifications existed
// is migrated in place.
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
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db, max: max, temp: temp, now: time.Now, subs: map[chan Event]struct{}{}}, nil
}

// SetClock replaces the store's clock, which stamps writes and decides
// expiry. It is a test seam and is not synchronised: call it before the store
// is in use.
func (s *Store) SetClock(now func() time.Time) {
	s.now = now
}

// migrate brings a messages table created before verifications existed up to
// the current schema: it adds the columns it lacks and normalises its email
// recipients the way new writes store them. A current database has every
// column, so this changes nothing.
func migrate(db *sql.DB) error {
	have := map[string]bool{}
	rows, err := db.Query(`SELECT name FROM pragma_table_info('messages')`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		have[name] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	var missing []string
	for _, c := range []string{"channel", "verification_sid"} {
		if !have[c] {
			missing = append(missing, c)
		}
	}
	if len(missing) == 0 {
		return nil
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, c := range missing {
		if _, err := tx.Exec(`ALTER TABLE messages ADD COLUMN ` + c + ` TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}
	type row struct {
		seq int64
		to  string
	}
	var emails []row
	rows, err = tx.Query(`SELECT seq, recipient FROM messages WHERE instr(recipient, '@') > 0`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.seq, &r.to); err != nil {
			rows.Close()
			return err
		}
		emails = append(emails, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, r := range emails {
		to, digits := recipient(r.to)
		if _, err := tx.Exec(`UPDATE messages SET recipient = ?, recipient_digits = ? WHERE seq = ?`, to, digits, r.seq); err != nil {
			return err
		}
	}
	return tx.Commit()
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

func isEmail(s string) bool { return strings.Contains(s, "@") }

// recipient returns how a recipient is stored and the digits it is indexed by.
// An email address is lowercased and has no digits, so no phone number
// matches it.
func recipient(to string) (stored, digits string) {
	if isEmail(to) {
		return strings.ToLower(to), ""
	}
	return to, Digits(to)
}

// toClause returns the SQL condition and argument for f.To, or an empty
// condition when f.To filters nothing.
func (f Filter) toClause() (string, string) {
	if isEmail(f.To) {
		return "recipient = ?", strings.ToLower(f.To)
	}
	if d := Digits(f.To); d != "" {
		return "recipient_digits = ?", d
	}
	return "", ""
}

// stamp drops t's monotonic reading and sub-nanosecond state and puts it in
// UTC, so a value equals what a later read of the stored value gives.
func stamp(t time.Time) time.Time {
	return t.Round(0).UTC()
}

// execer runs a write on the store's connection or inside a transaction.
type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
	QueryRow(query string, args ...any) *sql.Row
}

func (s *Store) Add(m Message) (Message, error) {
	m, err := s.insertMessage(s.db, m)
	if err != nil {
		return Message{}, err
	}
	s.publish(Event{Type: "message", Message: &m})
	return m, nil
}

// insertMessage writes m and prunes the oldest messages beyond the store's
// max, without publishing.
func (s *Store) insertMessage(db execer, m Message) (Message, error) {
	// Timestamps are stored in nanoseconds; round-trip through the same
	// precision so the returned message equals what a later read gives.
	now := time.Unix(0, s.now().UnixNano()).UTC()
	m.CreatedAt, m.UpdatedAt = now, now
	var digits string
	m.To, digits = recipient(m.To)
	_, err := db.Exec(`INSERT INTO messages (`+columns+`, recipient_digits)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.ID, m.Provider, m.Direction, m.AccountSID, m.From, m.To, m.Body, m.Status, m.OTP,
		m.Encoding, m.Segments, m.StatusCallback, m.Channel, m.VerificationSID,
		now.UnixNano(), now.UnixNano(), digits)
	if err != nil {
		return Message{}, err
	}
	if s.max > 0 {
		if _, err := db.Exec(`DELETE FROM messages WHERE seq <= (SELECT MAX(seq) FROM messages) - ?`, s.max); err != nil {
			return Message{}, err
		}
	}
	return m, nil
}

type scanner interface{ Scan(...any) error }

func scan(row scanner) (Message, error) {
	var m Message
	var created, updated int64
	err := row.Scan(&m.ID, &m.Provider, &m.Direction, &m.AccountSID, &m.From, &m.To, &m.Body, &m.Status,
		&m.OTP, &m.Encoding, &m.Segments, &m.StatusCallback, &m.Channel, &m.VerificationSID, &created, &updated)
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
	if clause, to := f.toClause(); clause != "" {
		where, args = append(where, clause), append(args, to)
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
	if isEmail(f.To) {
		if strings.ToLower(m.To) != strings.ToLower(f.To) {
			return false
		}
	} else if to := Digits(f.To); to != "" && (isEmail(m.To) || Digits(m.To) != to) {
		return false
	}
	if f.Direction != "" && m.Direction != f.Direction {
		return false
	}
	return f.Since.IsZero() || m.CreatedAt.After(f.Since)
}

func (s *Store) SetStatus(id, status string) (Message, error) {
	res, err := s.db.Exec(`UPDATE messages SET status = ?, updated_at = ? WHERE id = ?`, status, s.now().UnixNano(), id)
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

// Clear deletes every message and every verification.
func (s *Store) Clear() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, table := range []string{"messages", "verifications"} {
		if _, err := tx.Exec(`DELETE FROM ` + table); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
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
