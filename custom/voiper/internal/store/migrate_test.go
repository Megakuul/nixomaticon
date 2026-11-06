package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpgradeMovesContactsToTOMLAndMarksOldMessagesRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	contactsPath := filepath.Join(t.TempDir(), "contacts.toml")
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE contacts(id INTEGER PRIMARY KEY,name TEXT NOT NULL,address TEXT NOT NULL UNIQUE,notes TEXT NOT NULL DEFAULT '',source TEXT NOT NULL DEFAULT 'local',favorite INTEGER NOT NULL DEFAULT 0);
CREATE TABLE history(id INTEGER PRIMARY KEY,account TEXT,remote TEXT,direction TEXT,status TEXT,started TEXT,ended TEXT);
CREATE TABLE messages(id INTEGER PRIMARY KEY,account TEXT,remote TEXT,body TEXT,direction TEXT,status TEXT,created TEXT);
INSERT INTO contacts(name,address,favorite) VALUES('Alice','100',1);
INSERT INTO messages(account,remote,body,direction,status,created) VALUES('work','100','hello','incoming','received','2026-10-01T12:00:00Z');
PRAGMA user_version=1;`)
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(path, contactsPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	saved, err := os.ReadFile(contactsPath)
	if err != nil || !strings.Contains(string(saved), `address = "100"`) {
		t.Fatalf("contacts file: %s %v", saved, err)
	}
	var remaining int
	if err = s.db.QueryRow("SELECT count(*) FROM contacts").Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("contacts left in database: %d %v", remaining, err)
	}
	contacts, err := s.Contacts("")
	if err != nil || len(contacts) != 1 || !contacts[0].Favorite {
		t.Fatalf("contacts lost: %+v %v", contacts, err)
	}
	if count, err := s.UnreadCount(); err != nil || count != 0 {
		t.Fatalf("old messages incorrectly unread: %d %v", count, err)
	}
	contacts[0].Numbers = []ContactNumber{{Label: "Mobile", Address: "+491234"}}
	if err = s.SaveContact(contacts[0]); err != nil {
		t.Fatal(err)
	}
	contacts, err = s.Contacts("491234")
	if err != nil || len(contacts) != 1 || len(contacts[0].Numbers) != 1 {
		t.Fatalf("additional number search: %+v %v", contacts, err)
	}
	if err = s.DeleteContact(contacts[0].ID); err != nil {
		t.Fatal(err)
	}
	s.Close()
	// Reopening reads the file again and must not repeat the migration.
	s, err = Open(path, contactsPath)
	if err != nil {
		t.Fatal(err)
	}
	if contacts, err = s.Contacts(""); err != nil || len(contacts) != 0 {
		t.Fatalf("deleted contact returned: %+v %v", contacts, err)
	}
}
func TestReadCursorDoesNotMarkNewArrivalsRead(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "messages.db"), filepath.Join(t.TempDir(), "contacts.toml"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	first, err := s.AddMessage(Message{Account: "work", Remote: "sip:100@example.org", Body: "first", Direction: "incoming", Status: "received"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.AddMessage(Message{Account: "work", Remote: "sip:100@example.org", Body: "second", Direction: "incoming", Status: "received"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.MarkConversationRead("work", "sip:100@example.org", first); err != nil {
		t.Fatal(err)
	}
	if count, err := s.UnreadCount(); err != nil || count != 1 {
		t.Fatalf("unread=%d err=%v", count, err)
	}
	conversations, err := s.Conversations("work", "")
	if err != nil || len(conversations) != 1 || conversations[0].Unread != 1 {
		t.Fatalf("conversation summary=%+v err=%v", conversations, err)
	}
}
