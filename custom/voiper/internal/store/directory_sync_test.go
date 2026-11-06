package store

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestDirectorySyncPreservesLocalChangesAndHandlesPartialScans(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "contacts.db"), filepath.Join(t.TempDir(), "contacts.toml"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p := DirectoryProfile{URL: "ldaps://directory.example.org", BaseDN: "dc=example,dc=org", Limit: 1000, IntervalMinutes: 60, Enabled: true}
	if err = s.SaveDirectoryProfile(p); err != nil {
		t.Fatal(err)
	}
	first := Contact{DirectoryID: "uuid:first", Name: "Alice", Address: "100", Numbers: []ContactNumber{{Label: "Mobile", Address: "+49100"}}}
	second := Contact{DirectoryID: "uuid:second", Name: "Bob", Address: "200"}
	third := Contact{DirectoryID: "uuid:third", Name: "Charlie", Address: "300"}
	apply := func(result DirectoryResult, failure error) {
		t.Helper()
		if err = s.ApplyDirectory(p, result, failure); err != nil {
			t.Fatal(err)
		}
	}
	apply(DirectoryResult{Contacts: []Contact{first, second, third}}, nil)
	rows, err := s.Contacts("")
	if err != nil || len(rows) != 3 {
		t.Fatalf("initial contacts: %+v %v", rows, err)
	}
	aliceID := rows[0].ID
	rows[1].Name = "My Bob"
	rows[1].Notes = "Personal"
	rows[1].Favorite = true
	if err = s.SaveContact(rows[1]); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteContact(rows[2].ID); err != nil {
		t.Fatal(err)
	}
	first.Name = "Alice renamed"
	first.Address = "101"
	apply(DirectoryResult{Contacts: []Contact{first, second, third}}, nil)
	rows, err = s.Contacts("")
	if err != nil || len(rows) != 2 {
		t.Fatalf("local deletion resurrected: %+v %v", rows, err)
	}
	alice, err := s.Contacts("101")
	if err != nil || len(alice) != 1 || alice[0].ID != aliceID || alice[0].Name != "Alice renamed" || len(alice[0].Numbers) != 1 {
		t.Fatalf("remote rename/number update: %+v %v", alice, err)
	}
	bob, err := s.Contacts("200")
	if err != nil || len(bob) != 1 || bob[0].Name != "My Bob" || bob[0].Notes != "Personal" || !bob[0].Favorite || bob[0].Source != "local" {
		t.Fatalf("local edits lost: %+v %v", bob, err)
	}
	apply(DirectoryResult{Truncated: true}, nil)
	rows, _ = s.Contacts("")
	if len(rows) != 2 {
		t.Fatal("partial scan removed contacts")
	}
	apply(DirectoryResult{Skipped: 1}, nil)
	rows, _ = s.Contacts("")
	if len(rows) != 2 {
		t.Fatal("invalid remote entry removed contacts")
	}
	apply(DirectoryResult{}, errors.New("server offline"))
	rows, _ = s.Contacts("")
	if len(rows) != 2 {
		t.Fatal("failed scan removed contacts")
	}
	status, err := s.DirectoryStatus()
	if err != nil || status.LastSuccess.IsZero() || status.Error == "" {
		t.Fatalf("status: %+v %v", status, err)
	}
	apply(DirectoryResult{}, nil)
	rows, _ = s.Contacts("")
	if len(rows) != 1 || rows[0].Name != "My Bob" {
		t.Fatalf("complete scan did not preserve local contact: %+v", rows)
	}
}

func TestDirectoryConfigurationSwitchRejectsStaleResults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "contacts.db")
	contactsPath := filepath.Join(t.TempDir(), "contacts.toml")
	s, err := Open(path, contactsPath)
	if err != nil {
		t.Fatal(err)
	}
	p := DirectoryProfile{URL: "ldaps://old.example.org", BaseDN: "dc=old", Limit: 1000, IntervalMinutes: 60, Enabled: true}
	if err = s.SaveDirectoryProfile(p); err != nil {
		t.Fatal(err)
	}
	result := DirectoryResult{Contacts: []Contact{{DirectoryID: "uid=one", Name: "Old company", Address: "100"}}}
	if err = s.ApplyDirectory(p, result, nil); err != nil {
		t.Fatal(err)
	}
	next := p
	next.URL = "ldaps://new.example.org"
	if err = s.SaveDirectoryProfile(next); err != nil {
		t.Fatal(err)
	}
	if err = s.ApplyDirectory(p, DirectoryResult{}, nil); err == nil {
		t.Fatal("old server result accepted after configuration changed")
	}
	result.Contacts[0].Name = "New company"
	if err = s.ApplyDirectory(next, result, nil); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(path, contactsPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rows, err := s.Contacts("")
	if err != nil || len(rows) != 1 || rows[0].Name != "Old company" || rows[0].Source != "directory" {
		t.Fatalf("cached snapshot lost: %+v %v", rows, err)
	}
	saved, err := s.DirectoryProfile()
	if err != nil || saved != next {
		t.Fatalf("saved profile: %+v %v", saved, err)
	}
	result.Contacts[0].Address = "200"
	if err = s.ApplyDirectory(next, result, nil); err != nil {
		t.Fatal(err)
	}
	rows, _ = s.Contacts("")
	if len(rows) != 2 {
		t.Fatalf("resolved address conflict did not add new entry: %+v", rows)
	}
}

func TestDirectoryInvalidResultIsAtomic(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "contacts.db"), filepath.Join(t.TempDir(), "contacts.toml"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p := DirectoryProfile{URL: "ldaps://directory.example.org", BaseDN: "dc=example", Limit: 1000, IntervalMinutes: 60, Enabled: true}
	if err = s.SaveDirectoryProfile(p); err != nil {
		t.Fatal(err)
	}
	result := DirectoryResult{Contacts: []Contact{{DirectoryID: "uid=one", Name: "One", Address: "100"}, {DirectoryID: "uid=one", Name: "Duplicate", Address: "200"}}}
	if err = s.ApplyDirectory(p, result, nil); err == nil {
		t.Fatal("duplicate identity accepted")
	}
	rows, err := s.Contacts("")
	if err != nil || len(rows) != 0 {
		t.Fatalf("partial import: %+v %v", rows, err)
	}
}

func TestRestoreDirectoryVersionAndKeepDeletionHidden(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "contacts.db"), filepath.Join(t.TempDir(), "contacts.toml"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p := DirectoryProfile{URL: "ldaps://directory.example.org", BaseDN: "dc=example", Limit: 1000, IntervalMinutes: 60, Enabled: true}
	if err = s.SaveDirectoryProfile(p); err != nil {
		t.Fatal(err)
	}
	result := DirectoryResult{Contacts: []Contact{{DirectoryID: "uuid:one", Name: "Alice", Address: "100"}}}
	if err = s.ApplyDirectory(p, result, nil); err != nil {
		t.Fatal(err)
	}
	contacts, err := s.Contacts("")
	if err != nil || len(contacts) != 1 {
		t.Fatalf("contacts: %+v %v", contacts, err)
	}
	contact := contacts[0]
	contact.Name = "My Alice"
	contact.Notes = "Personal"
	contact.Favorite = true
	if contact.DirectoryID != "uuid:one" {
		t.Fatal("directory association missing")
	}
	if err = s.SaveContact(contact); err != nil {
		t.Fatal(err)
	}
	result.Contacts[0].Name = "Alice updated"
	result.Contacts[0].Address = "101"
	if err = s.ApplyDirectory(p, result, nil); err != nil {
		t.Fatal(err)
	}
	if err = s.RestoreDirectoryContact(contact.ID); err != nil {
		t.Fatal(err)
	}
	contacts, err = s.Contacts("")
	if err != nil || len(contacts) != 1 || contacts[0].Name != "Alice updated" || contacts[0].Address != "101" || contacts[0].Notes != "Personal" || !contacts[0].Favorite || contacts[0].Source != "directory-sync" {
		t.Fatalf("restore: %+v %v", contacts, err)
	}
	if err = s.DeleteContact(contact.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.ApplyDirectory(p, DirectoryResult{}, nil); err != nil {
		t.Fatal(err)
	}
	if err = s.ApplyDirectory(p, result, nil); err != nil {
		t.Fatal(err)
	}
	contacts, err = s.Contacts("")
	if err != nil || len(contacts) != 0 {
		t.Fatalf("remote reappearance revived hidden contact: %+v %v", contacts, err)
	}
}
