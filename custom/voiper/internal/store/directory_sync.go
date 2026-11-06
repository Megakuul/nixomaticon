package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"
)

// DirectoryProfile contains no secret. Passwords belong to the desktop wallet
// or the running session, never the contact database.
type DirectoryProfile struct {
	URL, BaseDN, BindDN, CAFile string
	Limit, IntervalMinutes      int
	Enabled, UseSecretService   bool
}

type DirectoryStatus struct {
	LastAttempt, LastSuccess time.Time
	Error                    string
	Entries, Preserved       int
	Partial                  bool
}

func (p DirectoryProfile) Validate() error {
	if !p.Enabled && p.URL == "" {
		return nil
	}
	u, err := url.Parse(p.URL)
	if err != nil || (u.Scheme != "ldap" && u.Scheme != "ldaps") || u.Hostname() == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("use an ldap:// or ldaps:// server URL without credentials, path, or query")
	}
	if len(p.URL) > 2048 || len(p.BaseDN) > 4096 || len(p.BindDN) > 4096 || len(p.CAFile) > 4096 {
		return errors.New("directory configuration is too long")
	}
	if strings.TrimSpace(p.BaseDN) == "" {
		return errors.New("directory base DN is required")
	}
	if _, err := ldap.ParseDN(p.BaseDN); err != nil {
		return errors.New("invalid directory base DN")
	}
	if p.Limit < 1 || p.Limit > 5000 {
		return errors.New("directory limit must be between 1 and 5000")
	}
	if p.IntervalMinutes < 5 || p.IntervalMinutes > 1440 {
		return errors.New("refresh interval must be between 5 and 1440 minutes")
	}
	return nil
}

func (s *Store) DirectoryProfile() (DirectoryProfile, error) {
	p := DirectoryProfile{Limit: 1000, IntervalMinutes: 60}
	var raw string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key='directory-profile'`).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	err = json.Unmarshal([]byte(raw), &p)
	return p, err
}

func (s *Store) SaveDirectoryProfile(p DirectoryProfile) error {
	if err := p.Validate(); err != nil {
		return err
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var previous string
	err = tx.QueryRow(`SELECT value FROM settings WHERE key='directory-profile'`).Scan(&previous)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var old DirectoryProfile
	if previous != "" {
		if err = json.Unmarshal([]byte(previous), &old); err != nil {
			return err
		}
	}
	if old.URL != p.URL || old.BaseDN != p.BaseDN || old.BindDN != p.BindDN {
		// Changing the directory keeps its old contacts as an imported snapshot.
		// The new server can never delete contacts belonging to the old server.
		if _, err = tx.Exec(`DELETE FROM directory_entries; DELETE FROM settings WHERE key='directory-status'`); err != nil {
			return err
		}
		err = s.updateContacts(func(contacts []Contact) ([]Contact, error) {
			for i := range contacts {
				if contacts[i].Source == "directory-sync" {
					contacts[i].Source = "directory"
				}
				contacts[i].DirectoryID = ""
			}
			return contacts, nil
		})
		if err != nil {
			return err
		}
	}
	if _, err = tx.Exec(`INSERT INTO settings(key,value) VALUES('directory-profile',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, string(raw)); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) DirectoryStatus() (DirectoryStatus, error) {
	var status DirectoryStatus
	var raw string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key='directory-status'`).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return status, nil
	}
	if err != nil {
		return status, err
	}
	err = json.Unmarshal([]byte(raw), &status)
	return status, err
}

// ApplyDirectory commits only a result for the still-configured profile. A
// partial scan may update entries but cannot prove that absent entries vanished.
// Edited contacts have source=local, so later refreshes never overwrite them.
func (s *Store) ApplyDirectory(profile DirectoryProfile, result DirectoryResult, lookupErr error) error {
	if len(result.Contacts) > 5000 {
		return errors.New("directory result exceeds 5000 contacts")
	}
	seen := make(map[string]bool, len(result.Contacts))
	if lookupErr == nil {
		for _, c := range result.Contacts {
			if c.DirectoryID == "" || len(c.DirectoryID) > 8192 || seen[c.DirectoryID] {
				return errors.New("directory returned a missing or duplicate identity")
			}
			seen[c.DirectoryID] = true
			if err := validContact(c); err != nil {
				return err
			}
		}
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var raw string
	if err = tx.QueryRow(`SELECT value FROM settings WHERE key='directory-profile'`).Scan(&raw); err != nil {
		return err
	}
	var current DirectoryProfile
	if err = json.Unmarshal([]byte(raw), &current); err != nil {
		return err
	}
	if current != profile {
		return errors.New("directory configuration changed during refresh")
	}
	status := DirectoryStatus{}
	err = tx.QueryRow(`SELECT value FROM settings WHERE key='directory-status'`).Scan(&raw)
	if err == nil {
		if err = json.Unmarshal([]byte(raw), &status); err != nil {
			return err
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	now := time.Now()
	status.LastAttempt = now
	status.Error = ""
	if lookupErr != nil {
		// Error details can include a bind identity or server diagnostic. Persist a
		// useful category only; credentials and directory replies never enter logs.
		status.Error = "Directory refresh failed. Check the endpoint, credentials and connection; cached contacts remain available."
	} else {
		status.LastSuccess = now
		status.Entries = len(result.Contacts)
		status.Partial = result.Truncated || result.Skipped > 0
		status.Preserved = 0
		// Contacts are written before the entry cache commits. If the commit fails,
		// the next refresh finds the written contacts by address and preserves them.
		err = s.updateContacts(func(contacts []Contact) ([]Contact, error) {
			contacts, preserved, err := applyDirectoryContacts(tx, contacts, result.Contacts, seen, !status.Partial, now)
			status.Preserved = preserved
			return contacts, err
		})
		if err != nil {
			return err
		}
	}
	encoded, err := json.Marshal(status)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO settings(key,value) VALUES('directory-status',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, string(encoded)); err != nil {
		return err
	}
	return tx.Commit()
}

// applyDirectoryContacts merges one refresh into contacts and the entry cache.
// Entries the user deleted (hidden) or edited (no longer directory-sync) are
// preserved, as is any contact whose number a directory entry would take over.
func applyDirectoryContacts(tx *sql.Tx, contacts []Contact, entries []Contact, seen map[string]bool, complete bool, now time.Time) ([]Contact, int, error) {
	preserved := 0
	for _, c := range entries {
		var hidden bool
		err := tx.QueryRow(`SELECT hidden FROM directory_entries WHERE remote_id=?`, c.DirectoryID).Scan(&hidden)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, 0, err
		}
		payload, err := json.Marshal(c)
		if err != nil {
			return nil, 0, err
		}
		index := slices.IndexFunc(contacts, func(other Contact) bool { return other.DirectoryID == c.DirectoryID })
		conflict := slices.ContainsFunc(contacts, func(other Contact) bool {
			return other.Address == c.Address && other.DirectoryID != c.DirectoryID
		})
		switch {
		case hidden || (index >= 0 && contacts[index].Source != "directory-sync") || conflict:
			preserved++
		case index >= 0:
			existing := &contacts[index]
			existing.Name, existing.Address, existing.Numbers, existing.Updated = c.Name, c.Address, trimNumbers(c.Numbers), now
		default:
			contacts = append(contacts, Contact{ID: nextContactID(contacts), Name: c.Name, Address: c.Address, Numbers: trimNumbers(c.Numbers), Source: "directory-sync", DirectoryID: c.DirectoryID, Updated: now})
		}
		if _, err = tx.Exec(`INSERT INTO directory_entries(remote_id,payload,hidden) VALUES(?,?,?) ON CONFLICT(remote_id) DO UPDATE SET payload=excluded.payload`, c.DirectoryID, string(payload), hidden); err != nil {
			return nil, 0, err
		}
	}
	if !complete {
		return contacts, preserved, nil
	}
	// Only a complete scan proves that an entry left the directory.
	rows, err := tx.Query(`SELECT remote_id FROM directory_entries WHERE hidden=0`)
	if err != nil {
		return nil, 0, err
	}
	var missing []string
	for rows.Next() {
		var remote string
		if err = rows.Scan(&remote); err != nil {
			rows.Close()
			return nil, 0, err
		}
		if !seen[remote] {
			missing = append(missing, remote)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, 0, err
	}
	for _, remote := range missing {
		if _, err = tx.Exec(`DELETE FROM directory_entries WHERE remote_id=?`, remote); err != nil {
			return nil, 0, err
		}
		contacts = slices.DeleteFunc(contacts, func(c Contact) bool { return c.DirectoryID == remote && c.Source == "directory-sync" })
	}
	return contacts, preserved, nil
}

// RestoreDirectoryContact discards local name/number changes in favor of the
// latest cached remote entry. Personal notes, favorites and account stay local.
func (s *Store) RestoreDirectoryContact(id int64) error {
	// Read the cached entry before taking contactsMu; see Store for lock order.
	s.contactsMu.Lock()
	index := slices.IndexFunc(s.contacts, func(c Contact) bool { return c.ID == id })
	directoryID := ""
	if index >= 0 {
		directoryID = s.contacts[index].DirectoryID
	}
	s.contactsMu.Unlock()
	var raw string
	err := s.db.QueryRow(`SELECT payload FROM directory_entries WHERE remote_id=? AND hidden=0`, directoryID).Scan(&raw)
	if directoryID == "" || errors.Is(err, sql.ErrNoRows) {
		return errors.New("this contact no longer has a directory entry")
	}
	if err != nil {
		return err
	}
	var remote Contact
	if err = json.Unmarshal([]byte(raw), &remote); err != nil {
		return err
	}
	if err = validContact(remote); err != nil {
		return err
	}
	return s.updateContacts(func(contacts []Contact) ([]Contact, error) {
		index := slices.IndexFunc(contacts, func(c Contact) bool { return c.ID == id && c.DirectoryID == directoryID })
		if index < 0 {
			return nil, errors.New("contact changed while restoring; try again")
		}
		if slices.ContainsFunc(contacts, func(c Contact) bool { return c.Address == remote.Address && c.ID != id }) {
			return nil, errors.New("another contact already uses the directory number")
		}
		restored := &contacts[index]
		restored.Name, restored.Address, restored.Numbers = remote.Name, remote.Address, trimNumbers(remote.Numbers)
		restored.Source, restored.Updated = "directory-sync", time.Now()
		return contacts, nil
	})
}
