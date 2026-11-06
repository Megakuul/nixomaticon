package store

import (
	"path/filepath"
	"testing"
)

func TestImportIsAtomicAndPreservesContacts(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "contacts.db"), filepath.Join(t.TempDir(), "contacts.toml"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.SaveContact(Contact{Name: "Personal name", Address: "101", Favorite: true}); err != nil {
		t.Fatal(err)
	}
	n, err := s.ImportCSV("name,address,notes\nDirectory name,101,new\nAlice,102,\n")
	if err != nil || n != 1 {
		t.Fatalf("import: %d %v", n, err)
	}
	all, err := s.Contacts("")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 || all[0].Name != "Personal name" || !all[0].Favorite {
		t.Fatalf("existing contact overwritten: %#v", all)
	}
	if _, err = s.ImportCSV("name,address\nValid,103\nInvalid,\n"); err == nil {
		t.Fatal("accepted invalid record")
	}
	all, _ = s.Contacts("")
	if len(all) != 2 {
		t.Fatal("partial import committed")
	}
	exported, err := s.ExportCSV()
	if err != nil {
		t.Fatal(err)
	}
	if exported == "" {
		t.Fatal("empty export")
	}
}

func TestCSVRoundTripKeepsMultipleNumbersTogether(t *testing.T) {
	source, err := Open(filepath.Join(t.TempDir(), "source.db"), filepath.Join(t.TempDir(), "contacts.toml"))
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	for _, contact := range []Contact{
		{Name: "Alex", Address: "101", Numbers: []ContactNumber{{Label: "mobile", Address: "+4912345"}}},
		{Name: "Alex", Address: "102"},
	} {
		if err = source.SaveContact(contact); err != nil {
			t.Fatal(err)
		}
	}
	text, err := source.ExportCSV()
	if err != nil {
		t.Fatal(err)
	}
	destination, err := Open(filepath.Join(t.TempDir(), "destination.db"), filepath.Join(t.TempDir(), "contacts.toml"))
	if err != nil {
		t.Fatal(err)
	}
	defer destination.Close()
	if count, err := destination.ImportCSV(text); err != nil || count != 2 {
		t.Fatalf("imported %d contacts: %v", count, err)
	}
	contacts, err := destination.Contacts("")
	if err != nil || len(contacts) != 2 {
		t.Fatalf("contacts: %+v, %v", contacts, err)
	}
	if contacts[0].Address != "101" || len(contacts[0].Numbers) != 1 || contacts[0].Numbers[0] != (ContactNumber{Label: "mobile", Address: "+4912345"}) || contacts[1].Address != "102" || len(contacts[1].Numbers) != 0 {
		t.Fatalf("number grouping changed: %+v", contacts)
	}
}
