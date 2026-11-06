package store

import (
	"database/sql"
	"errors"
	"fmt"
)

// TrustCertificate pins the first certificate fingerprint seen for a host
// that has no CA-signed certificate, and rejects a different one later.
func (s *Store) TrustCertificate(host, fingerprint string) error {
	key := "certificate:" + host
	var pinned string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key=?`, key).Scan(&pinned)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = s.db.Exec(`INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO NOTHING`, key, fingerprint)
		return err
	}
	if err != nil {
		return err
	}
	if pinned != fingerprint {
		return fmt.Errorf("certificate of %s changed (SHA-256 %s, trusted %s); forget the trusted certificate in the account's Swyx settings if the server was replaced", host, fingerprint, pinned)
	}
	return nil
}

// ForgetCertificate removes a pinned fingerprint so the next one is trusted.
func (s *Store) ForgetCertificate(host string) error {
	_, err := s.db.Exec(`DELETE FROM settings WHERE key=?`, "certificate:"+host)
	return err
}
