package store

import (
	"context"
	"github.com/go-ldap/ldap/v3"
	"path/filepath"
	"testing"
)

func TestDirectoryRequiresSecureExplicitEndpoint(t *testing.T) {
	for _, endpoint := range []string{"http://example.test", "ldap://name:secret@example.test", "ldaps://example.test/base", "ldaps://"} {
		if _, err := LookupDirectory(context.Background(), DirectoryConfig{URL: endpoint, BaseDN: "dc=example,dc=test"}); err == nil {
			t.Fatalf("accepted endpoint %q", endpoint)
		}
	}
}

func TestDirectoryImportPreservesLocalEdits(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "contacts.db"), filepath.Join(t.TempDir(), "contacts.toml"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.SaveContact(Contact{Name: "My name", Address: "100", Notes: "Private note", Favorite: true}); err != nil {
		t.Fatal(err)
	}
	count, err := s.ImportDirectory([]Contact{{Name: "Directory name", Address: "100"}, {Name: "New colleague", Address: "101"}})
	if err != nil || count != 1 {
		t.Fatalf("import: %d, %v", count, err)
	}
	contacts, err := s.Contacts("100")
	if err != nil || len(contacts) != 1 {
		t.Fatalf("contacts: %v, %v", contacts, err)
	}
	if contacts[0].Name != "My name" || contacts[0].Notes != "Private note" || !contacts[0].Favorite || contacts[0].Source != "local" {
		t.Fatalf("local edits overwritten: %+v", contacts[0])
	}
}

func TestDirectoryKeepsNumbersWithinTheirEntry(t *testing.T) {
	first, skipped := directoryContact(ldap.NewEntry("uid=first,dc=example", map[string][]string{"displayName": {"Alex"}, "telephoneNumber": {"100"}, "mobile": {"+1234"}, "sipURI": {"sip:alex@example.test"}}))
	second, _ := directoryContact(ldap.NewEntry("uid=second,dc=example", map[string][]string{"displayName": {"Alex"}, "telephoneNumber": {"200"}}))
	if skipped != 0 || first.Address != "100" || len(first.Numbers) != 2 || first.Numbers[0].Label != "Mobile" {
		t.Fatalf("first entry: %+v, skipped %d", first, skipped)
	}
	if second.Address != "200" || len(second.Numbers) != 0 {
		t.Fatalf("second entry: %+v", second)
	}
	s, err := Open(filepath.Join(t.TempDir(), "contacts.db"), filepath.Join(t.TempDir(), "contacts.toml"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if count, err := s.ImportDirectory([]Contact{first, second}); err != nil || count != 2 {
		t.Fatalf("import: %d,%v", count, err)
	}
	contacts, err := s.Contacts("Alex")
	if err != nil || len(contacts) != 2 {
		t.Fatalf("same-name contacts merged: %+v,%v", contacts, err)
	}
	if len(contacts[0].Numbers) != 2 {
		t.Fatal("additional numbers were not persisted")
	}
}
