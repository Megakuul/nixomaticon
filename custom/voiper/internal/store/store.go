package store

import (
	"database/sql"
	"encoding/csv"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// Contact is stored in contacts.toml. Address is the primary number; Source
// records where it came from (local, import, directory or directory-sync).
type Contact struct {
	ID          int64           `toml:"id"`
	Name        string          `toml:"name"`
	Company     string          `toml:"company,omitempty"`
	Address     string          `toml:"address"`
	Numbers     []ContactNumber `toml:"numbers,omitempty"`
	Notes       string          `toml:"notes,omitempty"`
	Favorite    bool            `toml:"favorite,omitempty"`
	Account     string          `toml:"account,omitempty"`
	Source      string          `toml:"source,omitempty"`
	DirectoryID string          `toml:"directory_id,omitempty"`
	Updated     time.Time       `toml:"updated,omitempty"`
}
type ContactNumber struct {
	Label   string `toml:"label,omitempty"`
	Address string `toml:"address"`
}
type History struct {
	ID                                 int64
	Account, Remote, Direction, Status string
	Started, Ended                     time.Time
}
type Message struct {
	ID                                       int64
	Account, Remote, Body, Direction, Status string
	Created                                  time.Time
	Read                                     bool
}

// Store keeps history, messages and directory state in SQLite and contacts in
// a TOML file. contactsMu guards contacts and serializes writes to the file.
// SQLite has a single connection: code that needs both must hold the database
// connection (a transaction) before contactsMu, never the other way round.
type Store struct {
	db           *sql.DB
	contactsPath string
	contactsMu   sync.Mutex
	contacts     []Contact
}

func Open(path, contactsPath string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	f.Close()
	if err = os.Chmod(path, 0600); err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: path}
	db, err := sql.Open("sqlite3", u.String()+"?_busy_timeout=3000&_journal_mode=WAL&_foreign_keys=on")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err = migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	s := &Store{db: db, contactsPath: contactsPath}
	if err = s.openContacts(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// openContacts loads contacts.toml, moving contacts from databases written by
// earlier versions into it the first time.
func (s *Store) openContacts() error {
	contacts, exists, err := loadContacts(s.contactsPath)
	if err != nil {
		return err
	}
	if !exists {
		if contacts, err = legacyContacts(s.db); err != nil {
			return err
		}
		if len(contacts) > 0 {
			if err = s.writeContacts(contacts); err != nil {
				return err
			}
			if _, err = s.db.Exec(`DELETE FROM contacts`); err != nil {
				return err
			}
		}
	}
	s.contacts = contacts
	return nil
}
func (s *Store) Close() error { return s.db.Close() }
func (s *Store) AddHistory(h History) error {
	_, err := s.db.Exec(`INSERT INTO history(account,remote,direction,status,started,ended) VALUES(?,?,?,?,?,?)`, h.Account, h.Remote, h.Direction, h.Status, h.Started.Format(time.RFC3339Nano), h.Ended.Format(time.RFC3339Nano))
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`DELETE FROM history WHERE id NOT IN (SELECT id FROM history ORDER BY id DESC LIMIT 2000)`)
	return err
}
func (s *Store) History() ([]History, error) { return s.HistoryPage("", "", 0, 200) }
func (s *Store) ClearHistory() error         { _, err := s.db.Exec(`DELETE FROM history`); return err }

// ImportCSV validates the complete file before changing any contacts.
func (s *Store) ImportCSV(text string) (int, error) {
	contacts, err := ParseCSV(text)
	if err != nil {
		return 0, err
	}
	return s.ImportContacts(contacts)
}

func (s *Store) ExportCSV() (string, error) {
	var out strings.Builder
	writer := csv.NewWriter(&out)
	writer.Write([]string{"name", "address", "notes", "group", "label"})
	for offset := 0; ; offset += 500 {
		contacts, err := s.ContactsPage("", offset, 500)
		if err != nil {
			return "", err
		}
		for _, c := range contacts {
			group := fmt.Sprint(c.ID)
			writer.Write([]string{c.Name, c.Address, c.Notes, group, "primary"})
			for _, n := range c.Numbers {
				writer.Write([]string{c.Name, n.Address, c.Notes, group, n.Label})
			}
		}
		if len(contacts) < 500 {
			break
		}
	}
	writer.Flush()
	return out.String(), writer.Error()
}
