package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// Lifecycle limits: Twilio Verify's defaults, fixed in this emulation.
const (
	VerificationTTL  = 10 * time.Minute
	MaxSendAttempts  = 5
	MaxCheckAttempts = 5
)

// SendAttempt is one delivery of a verification's code. SID is prefixed "VL".
type SendAttempt struct {
	SID     string    `json:"sid"`
	Channel string    `json:"channel"`
	Time    time.Time `json:"time"`
}

// Verification is an emulated Twilio Verify verification. Status is
// "pending", "approved", "canceled", "max_attempts_reached" or "expired".
// The JSON is Caduceus's own; Twilio's shapes are rendered by the server.
type Verification struct {
	ID            string        `json:"id"`
	ServiceSID    string        `json:"service_sid"`
	AccountSID    string        `json:"account_sid"`
	To            string        `json:"to"`
	Channel       string        `json:"channel"`
	Code          string        `json:"code"`
	Status        string        `json:"status"`
	SendAttempts  []SendAttempt `json:"send_attempts"`
	CheckAttempts int           `json:"check_attempts"`
	CreatedAt     time.Time     `json:"created_at"`
	UpdatedAt     time.Time     `json:"updated_at"`
	ExpiresAt     time.Time     `json:"expires_at"`
}

const verificationColumns = `id, service_sid, account_sid, recipient, channel, code, status,
	send_attempts, check_attempts, created_at, updated_at, expires_at`

// attempts returns a copy of a's entries with stamped times, never nil, so
// the value returned by a write equals what a later read gives.
func attempts(a []SendAttempt) []SendAttempt {
	out := make([]SendAttempt, len(a))
	for i, at := range a {
		at.Time = stamp(at.Time)
		out[i] = at
	}
	return out
}

func scanVerification(row scanner) (Verification, error) {
	var v Verification
	var sent string
	var created, updated, expires int64
	if err := row.Scan(&v.ID, &v.ServiceSID, &v.AccountSID, &v.To, &v.Channel, &v.Code, &v.Status,
		&sent, &v.CheckAttempts, &created, &updated, &expires); err != nil {
		return Verification{}, err
	}
	if err := json.Unmarshal([]byte(sent), &v.SendAttempts); err != nil {
		return Verification{}, err
	}
	v.SendAttempts = attempts(v.SendAttempts)
	v.CreatedAt = time.Unix(0, created).UTC()
	v.UpdatedAt = time.Unix(0, updated).UTC()
	v.ExpiresAt = time.Unix(0, expires).UTC()
	return v, nil
}

func (s *Store) publishVerification(v Verification) {
	v.SendAttempts = attempts(v.SendAttempts)
	s.publish(Event{Type: "verification", Verification: &v})
}

// AddVerification stores a new verification. It sets CreatedAt and UpdatedAt
// to now and ExpiresAt to CreatedAt plus VerificationTTL, ignoring the values
// passed in, and prunes the oldest verifications beyond the store's max.
func (s *Store) AddVerification(v Verification) (Verification, error) {
	now := stamp(s.now())
	v.CreatedAt, v.UpdatedAt, v.ExpiresAt = now, now, now.Add(VerificationTTL)
	var digits string
	v.To, digits = recipient(v.To)
	v.SendAttempts = attempts(v.SendAttempts)
	sent, err := json.Marshal(v.SendAttempts)
	if err != nil {
		return Verification{}, err
	}
	_, err = s.db.Exec(`INSERT INTO verifications (`+verificationColumns+`, recipient_digits)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		v.ID, v.ServiceSID, v.AccountSID, v.To, v.Channel, v.Code, v.Status, string(sent),
		v.CheckAttempts, now.UnixNano(), now.UnixNano(), v.ExpiresAt.UnixNano(), digits)
	if err != nil {
		return Verification{}, err
	}
	if s.max > 0 {
		if _, err := s.db.Exec(`DELETE FROM verifications WHERE seq <= (SELECT MAX(seq) FROM verifications) - ?`, s.max); err != nil {
			return Verification{}, err
		}
	}
	s.publishVerification(v)
	return v, nil
}

// UpdateVerification writes v's Channel, Code, Status, SendAttempts,
// CheckAttempts and ExpiresAt, sets UpdatedAt to now, and returns the
// verification as stored. Expiry is not applied to the returned value.
func (s *Store) UpdateVerification(v Verification) (Verification, error) {
	sent, err := json.Marshal(attempts(v.SendAttempts))
	if err != nil {
		return Verification{}, err
	}
	res, err := s.db.Exec(`UPDATE verifications SET channel = ?, code = ?, status = ?, send_attempts = ?,
		check_attempts = ?, expires_at = ?, updated_at = ? WHERE id = ?`,
		v.Channel, v.Code, v.Status, string(sent), v.CheckAttempts, stamp(v.ExpiresAt).UnixNano(),
		stamp(s.now()).UnixNano(), v.ID)
	if err != nil {
		return Verification{}, err
	}
	if n, err := res.RowsAffected(); err != nil {
		return Verification{}, err
	} else if n == 0 {
		return Verification{}, ErrNotFound
	}
	got, err := s.getVerification(v.ID)
	if err != nil {
		return Verification{}, err
	}
	s.publishVerification(got)
	return got, nil
}

// getVerification reads a verification as stored, without applying expiry.
func (s *Store) getVerification(id string) (Verification, error) {
	v, err := scanVerification(s.db.QueryRow(`SELECT `+verificationColumns+` FROM verifications WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Verification{}, ErrNotFound
	}
	return v, err
}

// applyExpiry stores a pending verification whose ExpiresAt is not after now
// as expired, and returns the verification's current state. The write is
// conditional on the stored row still being pending and past its expiry, so it
// never overwrites a write made after v was read; the event is published only
// when that write changed the row. It returns ErrNotFound when the row was
// deleted after v was read.
func (s *Store) applyExpiry(v Verification) (Verification, error) {
	now := stamp(s.now())
	if v.Status != "pending" || now.Before(v.ExpiresAt) {
		return v, nil
	}
	res, err := s.db.Exec(`UPDATE verifications SET status = 'expired', updated_at = ?
		WHERE id = ? AND status = 'pending' AND expires_at <= ?`, now.UnixNano(), v.ID, now.UnixNano())
	if err != nil {
		return Verification{}, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return Verification{}, err
	}
	cur, err := s.getVerification(v.ID)
	if err != nil {
		return Verification{}, err
	}
	if n > 0 {
		s.publishVerification(cur)
	}
	return cur, nil
}

// GetVerification returns the verification with that id, applying expiry, or
// ErrNotFound.
func (s *Store) GetVerification(id string) (Verification, error) {
	v, err := s.getVerification(id)
	if err != nil {
		return Verification{}, err
	}
	return s.applyExpiry(v)
}

// queryVerifications reads every matching row before returning, so the
// caller can write (expire) on the store's single connection afterwards.
func (s *Store) queryVerifications(query string, args ...any) ([]Verification, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Verification{}
	for rows.Next() {
		v, err := scanVerification(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ListVerifications returns verifications newest first, applying expiry. It
// honours f.To and f.Limit; the other Filter fields are ignored.
func (s *Store) ListVerifications(f Filter) ([]Verification, error) {
	query, args := `SELECT `+verificationColumns+` FROM verifications`, []any{}
	if clause, to := f.toClause(); clause != "" {
		query, args = query+` WHERE `+clause, append(args, to)
	}
	query += ` ORDER BY seq DESC`
	if f.Limit > 0 {
		query, args = query+` LIMIT ?`, append(args, f.Limit)
	}
	vs, err := s.queryVerifications(query, args...)
	if err != nil {
		return nil, err
	}
	out := vs[:0]
	for _, v := range vs {
		cur, err := s.applyExpiry(v)
		if errors.Is(err, ErrNotFound) {
			continue // deleted since the query: absent now, so not listed
		}
		if err != nil {
			return nil, err
		}
		out = append(out, cur)
	}
	return out, nil
}

// PendingVerification returns the newest verification on that service to that
// recipient that is still pending after expiry is applied, else ErrNotFound.
// The recipient is matched like Filter.To.
func (s *Store) PendingVerification(serviceSID, to string) (Verification, error) {
	clause, arg := Filter{To: to}.toClause()
	if clause == "" {
		return Verification{}, ErrNotFound
	}
	vs, err := s.queryVerifications(`SELECT `+verificationColumns+` FROM verifications
		WHERE service_sid = ? AND status = 'pending' AND `+clause+` ORDER BY seq DESC`, serviceSID, arg)
	if err != nil {
		return Verification{}, err
	}
	for _, v := range vs {
		cur, err := s.applyExpiry(v)
		if errors.Is(err, ErrNotFound) {
			continue // deleted since the query: try the next candidate
		}
		if err != nil {
			return Verification{}, err
		}
		if cur.Status == "pending" {
			return cur, nil
		}
	}
	return Verification{}, ErrNotFound
}
