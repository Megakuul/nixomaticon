package store

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestContactImportPreviewAndCommitPreserveEdits(t *testing.T) {
	for format, text := range map[string]string{
		"csv":   "name,address,notes,group,label\nImported Alice,101,replace,a,\nImported Alice,102,replace,a,work\nBob,201,,b,\nDuplicate Bob,201,,,\n",
		"vcard": "BEGIN:VCARD\nVERSION:4.0\nFN:Imported Alice\nNOTE:replace\nTEL:101\nTEL:102\nEND:VCARD\nBEGIN:VCARD\nVERSION:4.0\nFN:Bob\nTEL:201\nEND:VCARD\nBEGIN:VCARD\nVERSION:4.0\nFN:Duplicate Bob\nTEL:201\nEND:VCARD\n",
	} {
		t.Run(format, func(t *testing.T) {
			s, err := Open(filepath.Join(t.TempDir(), "contacts.db"), filepath.Join(t.TempDir(), "contacts.toml"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if err = s.SaveContact(Contact{Name: "Local Alice", Address: "101", Notes: "Private notes", Favorite: true, Account: "work", Numbers: []ContactNumber{{Label: "personal", Address: "103"}}}); err != nil {
				t.Fatal(err)
			}
			preserve, err := s.PreviewContactImport(format, text, false)
			if err != nil || preserve.Added != 1 || preserve.Skipped != 2 || preserve.Updated != 0 {
				t.Fatalf("preserve: %+v, %v", preserve, err)
			}
			preview, err := s.PreviewContactImport(format, text, true)
			if err != nil || preview.Added != 1 || preview.Updated != 1 || preview.Skipped != 1 || preview.Invalid != 0 || preview.Committed {
				t.Fatalf("preview: %+v, %v", preview, err)
			}
			before, _ := s.Contacts("")
			if len(before) != 1 || len(before[0].Numbers) != 1 {
				t.Fatal("preview changed contacts")
			}
			result, err := s.ImportContactFile(format, text, true)
			if err != nil || !result.Committed || result.Added != preview.Added || result.Updated != preview.Updated || result.Skipped != preview.Skipped {
				t.Fatalf("commit differs: %+v, %v", result, err)
			}
			contacts, err := s.Contacts("")
			if err != nil || len(contacts) != 2 {
				t.Fatalf("contacts: %+v, %v", contacts, err)
			}
			alice := contacts[0]
			if alice.Name != "Local Alice" || alice.Notes != "Private notes" || alice.Account != "work" || !alice.Favorite || alice.Source != "local" || len(alice.Numbers) != 2 || alice.Numbers[0] != (ContactNumber{Label: "personal", Address: "103"}) || alice.Numbers[1].Address != "102" {
				t.Fatalf("local edits changed: %+v", alice)
			}
		})
	}
}

func TestContactNumberMergeLimitBlocksWholeImport(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "contacts.db"), filepath.Join(t.TempDir(), "contacts.toml"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	contact := Contact{Name: "Alice", Address: "101"}
	for i := 0; i < 20; i++ {
		contact.Numbers = append(contact.Numbers, ContactNumber{Address: fmt.Sprint(200 + i)})
	}
	if err = s.SaveContact(contact); err != nil {
		t.Fatal(err)
	}
	const text = "name,address,group\nImported Alice,101,a\nImported Alice,999,a\nBob,102,b\n"
	result, err := s.ImportContactFile("csv", text, true)
	if err != nil || result.Invalid != 1 || result.Committed {
		t.Fatalf("limit: %+v, %v", result, err)
	}
	contacts, _ := s.Contacts("")
	if len(contacts) != 1 || len(contacts[0].Numbers) != 20 {
		t.Fatal("overflow import changed contacts")
	}
}

func TestInvalidContactImportDoesNotPartiallyCommit(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "contacts.db"), filepath.Join(t.TempDir(), "contacts.toml"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	text := "name,address\nAlice,101\nMissing,\nOther missing,\n"
	preview, err := s.PreviewContactImport("csv", text, false)
	if err != nil || preview.Invalid != 2 || preview.Added != 1 {
		t.Fatalf("preview: %+v, %v", preview, err)
	}
	result, err := s.ImportContactFile("csv", text, false)
	if err != nil || result.Committed || result.Invalid != 2 {
		t.Fatalf("invalid commit: %+v, %v", result, err)
	}
	contacts, _ := s.Contacts("")
	if len(contacts) != 0 {
		t.Fatal("invalid import partly committed")
	}
	if _, err = s.ImportCSV(text); err == nil {
		t.Fatal("legacy import accepted same invalid input")
	}
}

func TestContactImportRechecksDuplicatesAndRollsBackFailure(t *testing.T) {
	contactsDir := t.TempDir()
	s, err := Open(filepath.Join(t.TempDir(), "contacts.db"), filepath.Join(contactsDir, "contacts.toml"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	const text = "name,address\nAlice,101\nBob,102\n"
	preview, err := s.PreviewContactImport("csv", text, false)
	if err != nil || preview.Added != 2 {
		t.Fatalf("preview: %+v, %v", preview, err)
	}
	if err = s.SaveContact(Contact{Name: "Edited Alice", Address: "101"}); err != nil {
		t.Fatal(err)
	}
	result, err := s.ImportContactFile("csv", text, false)
	if err != nil || result.Added != 1 || result.Skipped != 1 || !result.Committed {
		t.Fatalf("stale preview was not rechecked: %+v, %v", result, err)
	}
	// An unwritable directory makes saving the contacts file fail.
	if err = os.Chmod(contactsDir, 0500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(contactsDir, 0700)
	result, err = s.ImportContactFile("csv", "name,address\nNew,103\nRejected,104\n", false)
	if err == nil || result.Committed {
		t.Fatal("storage failure was not reported")
	}
	contacts, _ := s.Contacts("")
	if len(contacts) != 2 {
		t.Fatal("storage failure partially committed")
	}
}

func TestSwyxPhonebookImportUsesPresenceNumber(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "contacts.db"), filepath.Join(t.TempDir(), "contacts.toml"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	const export = `<contacts><Contact id="312">
<Name><Display>Martin Muster</Display></Name><Info><Company>Finance</Company></Info>
<Phone><Type>Home</Type><Type>Cell</Type><Phone>0079 123 45 67</Phone></Phone>
<Phone><Type>Work</Type><Type>IPPhone</Type><Phone>312</Phone><Presence>312</Presence></Phone>
</Contact></contacts>`
	result, err := s.ImportContactFile("xml", export, false)
	if err != nil || result.Added != 1 || !result.Committed {
		t.Fatalf("import: %+v %v", result, err)
	}
	contacts, err := s.Contacts("Finance")
	if err != nil || len(contacts) != 1 {
		t.Fatalf("company search: %+v %v", contacts, err)
	}
	c := contacts[0]
	if c.Address != "312" || c.Company != "Finance" || len(c.Numbers) != 1 || c.Numbers[0] != (ContactNumber{Label: "Mobile", Address: "0079 123 45 67"}) {
		t.Fatalf("converted contact: %+v", c)
	}
}
