package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func add(t *testing.T, s *Store, to, body string) Message {
	t.Helper()
	m, err := s.Add(Message{ID: NewSID("SM"), Direction: Outbound, To: to, Body: body, Status: "queued"})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestPersistsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "caduceus.db")
	s, err := Open(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := add(t, s, "+15551234567", "hello")
	s.Close()

	s, err = Open(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.Get(want.ID)
	if err != nil || got != want {
		t.Fatalf("got %+v, %v; want %+v", got, err, want)
	}
}

func TestTemporaryDatabaseIsDeletedOnClose(t *testing.T) {
	s, err := Open("", 0)
	if err != nil {
		t.Fatal(err)
	}
	path := s.temp
	add(t, s, "+1555", "hi")
	s.Close()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary database still exists: %v", err)
	}
}

func TestPrunesOldestBeyondMax(t *testing.T) {
	s, err := Open("", 3)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for i := 1; i <= 5; i++ {
		add(t, s, "+1555", fmt.Sprint("message ", i))
	}
	got, err := s.List(Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Body != "message 5" || got[2].Body != "message 3" {
		t.Fatalf("kept %+v", got)
	}
}

func TestListFilters(t *testing.T) {
	s, err := Open("", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	add(t, s, "+1 (555) 123-4567", "Your code is 1234")
	add(t, s, "+15559999999", "100% off_today")

	for name, c := range map[string]struct {
		f    Filter
		want int
	}{
		"by number":        {Filter{To: "15551234567"}, 1},
		"by text":          {Filter{Query: "CODE"}, 1},
		"literal percent":  {Filter{Query: "100%"}, 1},
		"percent no match": {Filter{Query: "%zzz"}, 0},
		"everything":       {Filter{}, 2},
		"limit":            {Filter{Limit: 1}, 1},
	} {
		got, err := s.List(c.f)
		if err != nil || len(got) != c.want {
			t.Errorf("%s: got %d messages (%v), want %d", name, len(got), err, c.want)
		}
	}
}

func TestSetStatusOnMissingMessage(t *testing.T) {
	s, err := Open("", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.SetStatus("SMnope", "sent"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func newVerification(t *testing.T, s *Store, service, to string) Verification {
	t.Helper()
	v, err := s.AddVerification(Verification{
		ID:         NewSID("VE"),
		ServiceSID: service,
		AccountSID: "AC00000000000000000000000000000000",
		To:         to,
		Channel:    "sms",
		Code:       "123456",
		Status:     "pending",
		SendAttempts: []SendAttempt{
			{SID: NewSID("VL"), Channel: "sms", Time: time.Now()},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func setStatus(t *testing.T, s *Store, v Verification, status string) Verification {
	t.Helper()
	from := v.Status
	v.Status = status
	v, err := s.UpdateVerification(v, from)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// nextVerificationEvent returns the next verification event, failing when none
// arrives promptly.
func nextVerificationEvent(t *testing.T, events <-chan Event) Verification {
	t.Helper()
	for {
		select {
		case e := <-events:
			if e.Type != "verification" {
				continue
			}
			if e.Verification == nil {
				t.Fatal("verification event without a verification")
			}
			return *e.Verification
		case <-time.After(2 * time.Second):
			t.Fatal("no verification event")
		}
	}
}

func noEvent(t *testing.T, events <-chan Event) {
	t.Helper()
	select {
	case e := <-events:
		t.Fatalf("unexpected event %+v", e)
	default:
	}
}

func TestVerificationRoundTripsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "caduceus.db")
	s, err := Open(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	added := newVerification(t, s, "VAone", "+15551234567")
	// AddVerification's return is built from its input, not read back, so
	// comparing a read with it checks every column the add wrote.
	if got, err := s.GetVerification(added.ID); err != nil || !reflect.DeepEqual(got, added) {
		t.Fatalf("read after add: got %+v, %v; want %+v", got, err, added)
	}
	want := added
	want.CheckAttempts = 2
	want.SendAttempts = append(want.SendAttempts, SendAttempt{SID: NewSID("VL"), Channel: "call", Time: time.Unix(1_800_000_000, 5).UTC()})
	want.Channel = "call"
	want.Code = "654321"
	updated, err := s.UpdateVerification(want, "pending")
	if err != nil {
		t.Fatal(err)
	}
	want.UpdatedAt = updated.UpdatedAt
	if !reflect.DeepEqual(updated, want) {
		t.Fatalf("read after update: got %+v; want %+v", updated, want)
	}
	if want.ExpiresAt.Sub(want.CreatedAt) != VerificationTTL || len(want.SendAttempts) != 2 {
		t.Fatalf("added %+v", want)
	}
	if want.AccountSID != "AC00000000000000000000000000000000" || want.To != "+15551234567" || want.Status != "pending" {
		t.Fatalf("input fields lost: %+v", want)
	}
	s.Close()

	s, err = Open(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.GetVerification(want.ID)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("GetVerification: got %+v, %v; want %+v", got, err, want)
	}
	got, err = s.PendingVerification("VAone", "+15551234567")
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("PendingVerification: got %+v, %v; want %+v", got, err, want)
	}
}

func TestPendingVerificationScoping(t *testing.T) {
	s, err := Open("", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	v := newVerification(t, s, "VAone", "+15551234567")
	if got, err := s.PendingVerification("VAone", "+1 (555) 123-4567"); err != nil || got.ID != v.ID {
		t.Fatalf("same service: got %+v, %v", got, err)
	}
	if _, err := s.PendingVerification("VAother", "+15551234567"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other service: err = %v", err)
	}
	setStatus(t, s, v, "canceled")
	for _, status := range []string{"approved", "canceled", "max_attempts_reached"} {
		v := setStatus(t, s, newVerification(t, s, "VAone", "+15551234567"), status)
		if got, err := s.PendingVerification("VAone", "+15551234567"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("%s: got %+v, %v", status, got, err)
		}
		if got, err := s.GetVerification(v.ID); err != nil || got.Status != status {
			t.Fatalf("%s: GetVerification got %+v, %v", status, got, err)
		}
	}
	if _, err := s.GetVerification("VEnope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: err = %v", err)
	}
	if _, err := s.UpdateVerification(Verification{ID: "VEnope"}, "pending"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update missing: err = %v", err)
	}
}

func TestPendingVerificationExpiresOnRead(t *testing.T) {
	s, err := Open("", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Unix(1_800_000_000, 0).UTC()
	s.now = func() time.Time { return now }

	atNow := newVerification(t, s, "VAone", "+15551234567")
	atNow.ExpiresAt = now // equal to now: expired
	atNow, err = s.UpdateVerification(atNow, "pending")
	if err != nil {
		t.Fatal(err)
	}
	before := newVerification(t, s, "VAone", "+15550000000")
	before.ExpiresAt = now.Add(-time.Second)
	before, err = s.UpdateVerification(before, "pending")
	if err != nil {
		t.Fatal(err)
	}
	approved := setStatus(t, s, newVerification(t, s, "VAone", "+15559999999"), "approved")
	approved.ExpiresAt = now.Add(-time.Hour)
	approved, err = s.UpdateVerification(approved, "approved")
	if err != nil {
		t.Fatal(err)
	}
	live := newVerification(t, s, "VAone", "+15558888888")
	live.ExpiresAt = now.Add(time.Nanosecond)
	live, err = s.UpdateVerification(live, "pending")
	if err != nil {
		t.Fatal(err)
	}

	events, cancel := s.Subscribe()
	defer cancel()

	got, err := s.GetVerification(atNow.ID)
	if err != nil || got.Status != "expired" {
		t.Fatalf("GetVerification: got %+v, %v", got, err)
	}
	if e := nextVerificationEvent(t, events); e.ID != atNow.ID || e.Status != "expired" {
		t.Fatalf("event %+v", e)
	}
	// Reading it again changes nothing, so publishes nothing.
	if got, err := s.GetVerification(atNow.ID); err != nil || got.Status != "expired" {
		t.Fatalf("second read: got %+v, %v", got, err)
	}
	noEvent(t, events)

	list, err := s.ListVerifications(Filter{})
	if err != nil {
		t.Fatal(err)
	}
	statuses := map[string]string{}
	for _, v := range list {
		statuses[v.ID] = v.Status
	}
	if statuses[before.ID] != "expired" || statuses[approved.ID] != "approved" || statuses[live.ID] != "pending" {
		t.Fatalf("ListVerifications statuses %v", statuses)
	}
	if e := nextVerificationEvent(t, events); e.ID != before.ID || e.Status != "expired" {
		t.Fatalf("event %+v", e)
	}
	noEvent(t, events)

	// Stored, not only reported.
	var stored string
	if err := s.db.QueryRow(`SELECT status FROM verifications WHERE id = ?`, before.ID).Scan(&stored); err != nil || stored != "expired" {
		t.Fatalf("stored status %q, %v", stored, err)
	}
	if got, err := s.GetVerification(approved.ID); err != nil || got.Status != "approved" {
		t.Fatalf("approved: got %+v, %v", got, err)
	}

	expiring := newVerification(t, s, "VAone", "+15557777777")
	expiring.ExpiresAt = now.Add(-time.Minute)
	if _, err := s.UpdateVerification(expiring, "pending"); err != nil {
		t.Fatal(err)
	}
	nextVerificationEvent(t, events) // add
	nextVerificationEvent(t, events) // update
	if got, err := s.PendingVerification("VAone", "+15557777777"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("PendingVerification: got %+v, %v", got, err)
	}
	if e := nextVerificationEvent(t, events); e.ID != expiring.ID || e.Status != "expired" {
		t.Fatalf("event %+v", e)
	}
	if got, err := s.PendingVerification("VAone", "+15558888888"); err != nil || got.ID != live.ID {
		t.Fatalf("live: got %+v, %v", got, err)
	}
}

// A read that saw a pending verification must not overwrite a result written
// after that read: the expiry write is conditional.
func TestExpiryDoesNotOverwriteConcurrentWrite(t *testing.T) {
	s, err := Open("", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Unix(1_800_000_000, 0).UTC()
	s.now = func() time.Time { return now }

	v := newVerification(t, s, "VAone", "+15551234567")
	v.ExpiresAt = now.Add(-time.Second)
	stale, err := s.UpdateVerification(v, "pending")
	if err != nil {
		t.Fatal(err)
	}
	current := stale
	current.Status, current.CheckAttempts = "approved", 1
	if _, err := s.UpdateVerification(current, "pending"); err != nil {
		t.Fatal(err)
	}

	events, cancel := s.Subscribe()
	defer cancel()
	got, err := s.applyExpiry(stale)
	if err != nil || got.Status != "approved" || got.CheckAttempts != 1 {
		t.Fatalf("got %+v, %v", got, err)
	}
	noEvent(t, events)
}

// A write conditioned on the status its caller read does not overwrite a
// status stored since: against an expired row, an update conditioned on
// pending and a resend of the pending verification return ErrStale, change
// nothing and publish nothing.
func TestVerificationWriteIsConditionalOnReadStatus(t *testing.T) {
	s, err := Open("", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Unix(1_800_000_000, 0).UTC()
	s.SetClock(func() time.Time { return now })

	read := newVerification(t, s, "VAone", "+15551234567")
	now = read.ExpiresAt
	expired, err := s.GetVerification(read.ID)
	if err != nil || expired.Status != "expired" {
		t.Fatalf("expired: got %+v, %v", expired, err)
	}
	now = now.Add(time.Second)

	events, cancel := s.Subscribe()
	defer cancel()
	approved := read
	approved.Status, approved.CheckAttempts = "approved", 1
	if got, err := s.UpdateVerification(approved, "pending"); !errors.Is(err, ErrStale) {
		t.Fatalf("update: got %+v, %v; want ErrStale", got, err)
	}
	resend := read
	resend.SendAttempts = append(resend.SendAttempts, SendAttempt{SID: NewSID("VL"), Channel: "sms", Time: now})
	m := Message{ID: NewSID("SM"), Direction: Outbound, To: read.To, Body: read.Code, OTP: read.Code, Status: "queued", VerificationSID: read.ID}
	if _, _, err := s.SendVerification(resend, false, m); !errors.Is(err, ErrStale) {
		t.Fatalf("resend: err = %v; want ErrStale", err)
	}
	noEvent(t, events)

	if stored, err := s.getVerification(read.ID); err != nil || !reflect.DeepEqual(stored, expired) {
		t.Fatalf("row changed: got %+v, %v; want %+v", stored, err, expired)
	}
	if msgs, err := s.List(Filter{}); err != nil || len(msgs) != 0 {
		t.Fatalf("stale resend stored messages %+v, %v", msgs, err)
	}
	if _, err := s.UpdateVerification(Verification{ID: "VEnope"}, "pending"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update missing: err = %v; want ErrNotFound", err)
	}
}

// deleteOnFirstNow makes the store's clock delete the verification with that
// id the first time it is read, standing in for a Clear or a prune that runs
// between a read's query and its expiry write.
func deleteOnFirstNow(t *testing.T, s *Store, now time.Time, id string) {
	t.Helper()
	deleted := false
	s.now = func() time.Time {
		if !deleted {
			deleted = true
			if _, err := s.db.Exec(`DELETE FROM verifications WHERE id = ?`, id); err != nil {
				t.Errorf("delete: %v", err)
			}
		}
		return now
	}
}

// A row deleted after a read's query but before its expiry write is absent:
// the read leaves it out instead of failing.
func TestReadsSkipVerificationDeletedBeforeExpiry(t *testing.T) {
	s, err := Open("", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Unix(1_800_000_000, 0).UTC()
	s.now = func() time.Time { return now }

	older := newVerification(t, s, "VAone", "+15551234567")
	newer := newVerification(t, s, "VAone", "+15551234567")
	newer.ExpiresAt = now.Add(-time.Second)
	if _, err := s.UpdateVerification(newer, "pending"); err != nil {
		t.Fatal(err)
	}

	deleteOnFirstNow(t, s, now, newer.ID)
	list, err := s.ListVerifications(Filter{})
	if err != nil || len(list) != 1 || list[0].ID != older.ID {
		t.Fatalf("ListVerifications: got %+v, %v; want only %s", list, err, older.ID)
	}

	gone := newVerification(t, s, "VAone", "+15551234567")
	gone.ExpiresAt = now.Add(-time.Second)
	if _, err := s.UpdateVerification(gone, "pending"); err != nil {
		t.Fatal(err)
	}
	deleteOnFirstNow(t, s, now, gone.ID)
	got, err := s.PendingVerification("VAone", "+15551234567")
	if err != nil || got.ID != older.ID {
		t.Fatalf("PendingVerification: got %+v, %v; want %s", got, err, older.ID)
	}
}

func TestClearAndPruneVerifications(t *testing.T) {
	s, err := Open("", 2)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	first := newVerification(t, s, "VAone", "+15550000001")
	second := newVerification(t, s, "VAone", "+15550000002")
	third := newVerification(t, s, "VAone", "+15550000003")
	got, err := s.ListVerifications(Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != third.ID || got[1].ID != second.ID {
		t.Fatalf("kept %+v", got)
	}
	if _, err := s.GetVerification(first.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("oldest not pruned: %v", err)
	}

	add(t, s, "+1555", "hello")
	if err := s.Clear(); err != nil {
		t.Fatal(err)
	}
	vs, err := s.ListVerifications(Filter{})
	if err != nil || len(vs) != 0 {
		t.Fatalf("verifications after Clear: %+v, %v", vs, err)
	}
	ms, err := s.List(Filter{})
	if err != nil || len(ms) != 0 {
		t.Fatalf("messages after Clear: %+v, %v", ms, err)
	}
}

func TestVerificationWritesPublishEvents(t *testing.T) {
	s, err := Open("", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	events, cancel := s.Subscribe()
	defer cancel()

	v := newVerification(t, s, "VAone", "+15551234567")
	if e := nextVerificationEvent(t, events); !reflect.DeepEqual(e, v) {
		t.Fatalf("add event %+v, want %+v", e, v)
	}
	v.CheckAttempts = 1
	v, err = s.UpdateVerification(v, "pending")
	if err != nil {
		t.Fatal(err)
	}
	if e := nextVerificationEvent(t, events); !reflect.DeepEqual(e, v) || e.CheckAttempts != 1 {
		t.Fatalf("update event %+v, want %+v", e, v)
	}
}

func TestEmailRecipientMatching(t *testing.T) {
	s, err := Open("", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ada := add(t, s, "ada@example.com", "code 1")
	ada1 := add(t, s, "Ada1@Example.com", "code 2")
	phone := add(t, s, "+15551234567", "code 3")
	if ada1.To != "ada1@example.com" {
		t.Fatalf("email recipient stored as %q", ada1.To)
	}

	got, err := s.List(Filter{To: "Ada@Example.com"})
	if err != nil || len(got) != 1 || got[0].ID != ada.ID {
		t.Fatalf("by email: %+v, %v", got, err)
	}
	got, err = s.List(Filter{To: "1"})
	if err != nil || len(got) != 0 {
		t.Fatalf("by digit 1: %+v, %v", got, err)
	}
	got, err = s.List(Filter{To: "15551234567"})
	if err != nil || len(got) != 1 || got[0].ID != phone.ID {
		t.Fatalf("by phone: %+v, %v", got, err)
	}

	if !(Filter{To: "ADA@example.COM"}).Matches(ada) || (Filter{To: "ada@example.com"}).Matches(ada1) {
		t.Fatal("Matches by email")
	}
	if (Filter{To: "1"}).Matches(ada1) || !(Filter{To: "+1 555 123 4567"}).Matches(phone) {
		t.Fatal("Matches by phone")
	}

	email := newVerification(t, s, "VAone", "ADA1@example.com")
	newVerification(t, s, "VAone", "+15551234567")
	vs, err := s.ListVerifications(Filter{To: "ada1@EXAMPLE.com"})
	if err != nil || len(vs) != 1 || vs[0].ID != email.ID {
		t.Fatalf("verifications by email: %+v, %v", vs, err)
	}
	vs, err = s.ListVerifications(Filter{To: "1"})
	if err != nil || len(vs) != 0 {
		t.Fatalf("verifications by digit 1: %+v, %v", vs, err)
	}
}

func TestPendingVerificationByEmail(t *testing.T) {
	s, err := Open("", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	v := newVerification(t, s, "VAone", "ada@example.com")
	got, err := s.PendingVerification("VAone", "Ada@Example.com")
	if err != nil || got.ID != v.ID || got.To != "ada@example.com" {
		t.Fatalf("got %+v, %v", got, err)
	}
	if _, err := s.PendingVerification("VAone", "nobody"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("recipient with neither @ nor digits: err = %v", err)
	}
}

func TestMessageChannelFields(t *testing.T) {
	s, err := Open("", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	m, err := s.Add(Message{ID: NewSID("SM"), Direction: Outbound, To: "ada@example.com",
		Body: "code", Status: "sent", Channel: "email", VerificationSID: "VE0123"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(m.ID)
	if err != nil || got.Channel != "email" || got.VerificationSID != "VE0123" {
		t.Fatalf("Get: %+v, %v", got, err)
	}
	list, err := s.List(Filter{})
	if err != nil || len(list) != 1 || list[0] != got {
		t.Fatalf("List: %+v, %v", list, err)
	}
}

// preF1Schema is the messages schema as it was before F1.1.
const preF1Schema = `
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

func TestMigratesPreF1Database(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	old, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(preF1Schema); err != nil {
		t.Fatal(err)
	}
	for _, to := range []string{"+15551234567", "Ada1@Example.com"} {
		// The pre-F1 store indexed every recipient by its digits.
		if _, err := old.Exec(`INSERT INTO messages (id, provider, direction, account_sid, sender, recipient,
			recipient_digits, body, status, otp, encoding, segments, status_callback, created_at, updated_at)
			VALUES (?, 'twilio', 'outbound', '', '+1000', ?, ?, 'old', 'sent', '', 'GSM-7', 1, '', 1, 1)`,
			NewSID("SM"), to, Digits(to)); err != nil {
			t.Fatal(err)
		}
	}
	old.Close()

	s, err := Open(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	m, err := s.Add(Message{ID: NewSID("SM"), Direction: Outbound, To: "+15550000000", Body: "new", Channel: "sms"})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.Get(m.ID); err != nil || got.Channel != "sms" {
		t.Fatalf("new message: %+v, %v", got, err)
	}
	all, err := s.List(Filter{})
	if err != nil || len(all) != 3 {
		t.Fatalf("all messages: %+v, %v", all, err)
	}
	got, err := s.List(Filter{To: "ada1@example.com"})
	if err != nil || len(got) != 1 || got[0].To != "ada1@example.com" {
		t.Fatalf("old email row by address: %+v, %v", got, err)
	}
	got, err = s.List(Filter{To: "1"})
	if err != nil || len(got) != 0 {
		t.Fatalf("old email row by digit 1: %+v, %v", got, err)
	}
	if _, err := s.AddVerification(Verification{ID: NewSID("VE"), ServiceSID: "VAone", To: "+1555", Status: "pending"}); err != nil {
		t.Fatal(err)
	}
	s.Close()

	s, err = Open(path, 0)
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	defer s.Close()
	if got, err := s.List(Filter{}); err != nil || len(got) != 3 {
		t.Fatalf("after reopen: %+v, %v", got, err)
	}
}

// SendVerification writes the verification and its message in one
// transaction: when the message cannot be stored, neither is, and no event is
// published.
func TestSendVerificationIsAtomic(t *testing.T) {
	s, err := Open("", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	msg := func(v Verification) Message {
		return Message{ID: NewSID("SM"), Direction: Outbound, To: v.To, Body: v.Code, OTP: v.Code, Status: "queued", VerificationSID: v.ID}
	}
	fresh := Verification{ID: NewSID("VE"), ServiceSID: "VAone", To: "+15551234567", Channel: "sms", Code: "123456", Status: "pending",
		SendAttempts: []SendAttempt{{SID: NewSID("VL"), Channel: "sms", Time: time.Now()}}}

	v, m, err := s.SendVerification(fresh, true, msg(fresh))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.GetVerification(v.ID); err != nil || !reflect.DeepEqual(got, v) {
		t.Fatalf("stored %+v, %v; want %+v", got, err, v)
	}
	if got, err := s.Get(m.ID); err != nil || got.VerificationSID != v.ID {
		t.Fatalf("message %+v, %v", got, err)
	}

	events, cancel := s.Subscribe()
	defer cancel()
	if _, err := s.db.Exec(`CREATE TRIGGER fail_messages BEFORE INSERT ON messages BEGIN SELECT RAISE(FAIL, 'disk full'); END`); err != nil {
		t.Fatal(err)
	}
	resend := v
	resend.Channel = "whatsapp"
	resend.SendAttempts = append(resend.SendAttempts, SendAttempt{SID: NewSID("VL"), Channel: "whatsapp", Time: time.Now()})
	if _, _, err := s.SendVerification(resend, false, msg(resend)); err == nil {
		t.Fatal("resend succeeded with message inserts failing")
	}
	if got, err := s.GetVerification(v.ID); err != nil || !reflect.DeepEqual(got, v) {
		t.Fatalf("after a failed resend %+v, %v; want %+v", got, err, v)
	}
	other := fresh
	other.ID, other.To = NewSID("VE"), "+15557654321"
	if _, _, err := s.SendVerification(other, true, msg(other)); err == nil {
		t.Fatal("new send succeeded with message inserts failing")
	}
	if _, err := s.GetVerification(other.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("failed new send stored a verification: %v", err)
	}
	noEvent(t, events)
}
