package store

import (
	"database/sql"
	"fmt"
	"time"
)

func migrate(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var version int
	if err = tx.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version > 3 {
		return fmt.Errorf("database schema %d requires a newer Voiper", version)
	}
	if version == 0 {
		_, err = tx.Exec(`CREATE TABLE contacts (id INTEGER PRIMARY KEY, name TEXT NOT NULL, address TEXT NOT NULL UNIQUE, notes TEXT NOT NULL DEFAULT '', source TEXT NOT NULL DEFAULT 'local', favorite INTEGER NOT NULL DEFAULT 0);
CREATE TABLE history (id INTEGER PRIMARY KEY, account TEXT NOT NULL, remote TEXT NOT NULL, direction TEXT NOT NULL, status TEXT NOT NULL, started TEXT NOT NULL, ended TEXT NOT NULL);
CREATE TABLE messages (id INTEGER PRIMARY KEY, account TEXT NOT NULL, remote TEXT NOT NULL, body TEXT NOT NULL, direction TEXT NOT NULL, status TEXT NOT NULL, created TEXT NOT NULL);`)
		if err != nil {
			return err
		}
	}
	if version < 2 {
		_, err = tx.Exec(`ALTER TABLE contacts ADD COLUMN account TEXT NOT NULL DEFAULT '';
ALTER TABLE contacts ADD COLUMN updated TEXT NOT NULL DEFAULT '';
CREATE TABLE contact_numbers (contact_id INTEGER NOT NULL REFERENCES contacts(id) ON DELETE CASCADE, label TEXT NOT NULL, address TEXT NOT NULL, position INTEGER NOT NULL, PRIMARY KEY(contact_id,address));
ALTER TABLE messages ADD COLUMN read INTEGER NOT NULL DEFAULT 1;
CREATE INDEX messages_conversation ON messages(account,remote,id);
CREATE INDEX messages_unread ON messages(read,account);
CREATE INDEX history_account ON history(account,id);
CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);
PRAGMA user_version=2;`)
		if err != nil {
			return err
		}
	}
	if version < 3 {
		_, err = tx.Exec(`CREATE TABLE directory_entries (
remote_id TEXT PRIMARY KEY, contact_id INTEGER REFERENCES contacts(id) ON DELETE SET NULL,
payload TEXT NOT NULL, hidden INTEGER NOT NULL DEFAULT 0);
PRAGMA user_version=3;`)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

// legacyContacts reads contacts stored in SQLite before contacts.toml existed.
func legacyContacts(db *sql.DB) ([]Contact, error) {
	rows, err := db.Query(`SELECT id,name,address,notes,source,favorite,account,updated,
COALESCE((SELECT remote_id FROM directory_entries WHERE contact_id=contacts.id LIMIT 1),'') FROM contacts ORDER BY id`)
	if err != nil {
		return nil, err
	}
	contacts := []Contact{}
	positions := map[int64]int{}
	for rows.Next() {
		var c Contact
		var updated string
		if err = rows.Scan(&c.ID, &c.Name, &c.Address, &c.Notes, &c.Source, &c.Favorite, &c.Account, &updated, &c.DirectoryID); err != nil {
			rows.Close()
			return nil, err
		}
		c.Updated, _ = time.Parse(time.RFC3339Nano, updated)
		c.Numbers = []ContactNumber{}
		positions[c.ID] = len(contacts)
		contacts = append(contacts, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	rows, err = db.Query(`SELECT contact_id,label,address FROM contact_numbers ORDER BY contact_id,position`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var n ContactNumber
		if err = rows.Scan(&id, &n.Label, &n.Address); err != nil {
			return nil, err
		}
		if i, ok := positions[id]; ok {
			contacts[i].Numbers = append(contacts[i].Numbers, n)
		}
	}
	return contacts, rows.Err()
}
