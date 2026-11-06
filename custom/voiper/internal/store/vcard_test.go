package store

import (
	"path/filepath"
	"testing"
)

func TestVCardImportIsAtomicAndPreservesNumbers(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "contacts.db"), filepath.Join(t.TempDir(), "contacts.toml"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	const card = "BEGIN:VCARD\r\nVERSION:4.0\r\nFN:Alice\r\nTEL:tel:+41123456\r\nIMPP:sip:alice@example.test\r\nEND:VCARD\r\n"
	if _, err := s.ImportVCard(card + "BEGIN:VCARD\r\nVERSION:4.0\r\n"); err == nil {
		t.Fatal("accepted incomplete second card")
	}
	contacts, err := s.Contacts("")
	if err != nil || len(contacts) != 0 {
		t.Fatalf("partial import: %v, %v", contacts, err)
	}
	if n, err := s.ImportVCard(card); err != nil || n != 1 {
		t.Fatalf("import: %d, %v", n, err)
	}
	if n, err := s.ImportVCard(card); err != nil || n != 0 {
		t.Fatalf("duplicate import: %d, %v", n, err)
	}
	text, err := s.ExportVCard()
	if err != nil {
		t.Fatal(err)
	}
	contacts, err = ParseVCard(text)
	if err != nil || len(contacts) != 1 {
		t.Fatalf("round trip: %v, %v", contacts, err)
	}
	addresses := map[string]bool{}
	for _, c := range contacts {
		addresses[c.Address] = true
		for _, n := range c.Numbers {
			addresses[n.Address] = true
		}
	}
	if !addresses["+41123456"] || !addresses["sip:alice@example.test"] {
		t.Fatalf("lost numbers: %v", contacts)
	}
}
