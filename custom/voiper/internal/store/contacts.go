package store

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

const contactsHeader = `# Voiper contacts. Voiper rewrites this file when contacts change, so edit it
# while Voiper is closed. Each contact needs a unique primary address; entries
# without an id receive one on the next start.

`

type contactsFile struct {
	Contacts []Contact `toml:"contacts"`
}

// loadContacts reads the contacts file. A file that cannot be parsed is an
// error rather than an empty phonebook, so a hand edit is never overwritten.
func loadContacts(path string) ([]Contact, bool, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return []Contact{}, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var file contactsFile
	if _, err = toml.Decode(string(raw), &file); err != nil {
		return nil, true, fmt.Errorf("read %s: %w", path, err)
	}
	ids := map[int64]bool{}
	addresses := map[string]bool{}
	var next int64
	for _, c := range file.Contacts {
		next = max(next, c.ID)
	}
	for i := range file.Contacts {
		c := &file.Contacts[i]
		c.Address = strings.TrimSpace(c.Address)
		if c.Name = strings.TrimSpace(c.Name); c.Name == "" {
			c.Name = c.Address
		}
		if c.Source == "" {
			c.Source = "local"
		}
		if c.Numbers == nil {
			c.Numbers = []ContactNumber{}
		}
		if c.ID <= 0 || ids[c.ID] {
			next++
			c.ID = next
		}
		ids[c.ID] = true
		if err := validContact(*c); err != nil {
			return nil, true, fmt.Errorf("%s: contact %q: %w", path, c.Name, err)
		}
		if addresses[c.Address] {
			return nil, true, fmt.Errorf("%s: address %q is used by more than one contact", path, c.Address)
		}
		addresses[c.Address] = true
	}
	return file.Contacts, true, nil
}

// writeContacts replaces the file atomically so a crash leaves either the old
// or the new phonebook. The caller holds contactsMu.
func (s *Store) writeContacts(contacts []Contact) error {
	sorted := slices.Clone(contacts)
	slices.SortStableFunc(sorted, func(a, b Contact) int {
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	for i := range sorted {
		sorted[i].Updated = sorted[i].Updated.Truncate(time.Second)
	}
	var out bytes.Buffer
	out.WriteString(contactsHeader)
	encoder := toml.NewEncoder(&out)
	encoder.Indent = ""
	if err := encoder.Encode(contactsFile{Contacts: sorted}); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.contactsPath), 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(s.contactsPath), ".contacts-*.toml")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(out.Bytes()); err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(file.Name(), s.contactsPath)
}

// updateContacts applies change to a copy and keeps it only once it is saved.
func (s *Store) updateContacts(change func(contacts []Contact) ([]Contact, error)) error {
	s.contactsMu.Lock()
	defer s.contactsMu.Unlock()
	next, err := change(cloneContacts(s.contacts))
	if err != nil {
		return err
	}
	if err = s.writeContacts(next); err != nil {
		return err
	}
	s.contacts = next
	return nil
}

func cloneContacts(contacts []Contact) []Contact {
	cloned := slices.Clone(contacts)
	for i := range cloned {
		cloned[i].Numbers = slices.Clone(cloned[i].Numbers)
	}
	return cloned
}

func nextContactID(contacts []Contact) int64 {
	var id int64
	for _, c := range contacts {
		id = max(id, c.ID)
	}
	return id + 1
}

func (s *Store) Contacts(query string) ([]Contact, error) { return s.ContactsPage(query, 0, 500) }
func (s *Store) ContactsPage(query string, offset, limit int) ([]Contact, error) {
	if offset < 0 {
		return nil, errors.New("invalid contact offset")
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	query = strings.ToLower(query)
	matches := func(c Contact) bool {
		if strings.Contains(strings.ToLower(c.Name), query) || strings.Contains(strings.ToLower(c.Company), query) || strings.Contains(strings.ToLower(c.Address), query) {
			return true
		}
		return slices.ContainsFunc(c.Numbers, func(n ContactNumber) bool { return strings.Contains(strings.ToLower(n.Address), query) })
	}
	s.contactsMu.Lock()
	found := []Contact{}
	for _, c := range s.contacts {
		if matches(c) {
			found = append(found, c)
		}
	}
	found = cloneContacts(found)
	s.contactsMu.Unlock()
	slices.SortStableFunc(found, func(a, b Contact) int {
		if a.Favorite != b.Favorite {
			if a.Favorite {
				return -1
			}
			return 1
		}
		if order := strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)); order != 0 {
			return order
		}
		return cmp.Compare(a.ID, b.ID)
	})
	if offset >= len(found) {
		return []Contact{}, nil
	}
	return found[offset:min(len(found), offset+limit)], nil
}

func validContact(c Contact) error {
	if strings.TrimSpace(c.Address) == "" || len(c.Address) > 1024 || strings.ContainsAny(c.Address, "\r\n\x00") {
		return errors.New("enter a valid number or SIP address")
	}
	if len(c.Name) > 512 || len(c.Company) > 512 || len(c.Notes) > 8192 || len(c.Account) > 256 {
		return errors.New("contact text is too long")
	}
	if len(c.Numbers) > 20 {
		return errors.New("a contact supports at most 20 additional numbers")
	}
	seen := map[string]bool{strings.TrimSpace(c.Address): true}
	for _, n := range c.Numbers {
		address := strings.TrimSpace(n.Address)
		if address == "" || len(address) > 1024 || strings.ContainsAny(address, "\r\n\x00") || len(n.Label) > 80 {
			return errors.New("enter a valid additional number and label")
		}
		if seen[address] {
			return fmt.Errorf("duplicate contact number %q", address)
		}
		seen[address] = true
	}
	return nil
}

func trimNumbers(numbers []ContactNumber) []ContactNumber {
	trimmed := make([]ContactNumber, 0, len(numbers))
	for _, n := range numbers {
		trimmed = append(trimmed, ContactNumber{Label: strings.TrimSpace(n.Label), Address: strings.TrimSpace(n.Address)})
	}
	return trimmed
}

// SaveContact updates the contact with c.ID, or the contact already using the
// address when c.ID is zero. Editing a synchronized contact makes it local.
func (s *Store) SaveContact(c Contact) error {
	c.Address = strings.TrimSpace(c.Address)
	c.Name = strings.TrimSpace(c.Name)
	c.Company = strings.TrimSpace(c.Company)
	if c.Name == "" {
		c.Name = c.Address
	}
	if err := validContact(c); err != nil {
		return err
	}
	c.Numbers = trimNumbers(c.Numbers)
	return s.updateContacts(func(contacts []Contact) ([]Contact, error) {
		index := -1
		if c.ID != 0 {
			if index = slices.IndexFunc(contacts, func(other Contact) bool { return other.ID == c.ID }); index < 0 {
				return nil, errors.New("contact no longer exists")
			}
		} else {
			index = slices.IndexFunc(contacts, func(other Contact) bool { return other.Address == c.Address })
		}
		if other := slices.IndexFunc(contacts, func(other Contact) bool { return other.Address == c.Address }); other >= 0 && other != index {
			return nil, errors.New("another contact already uses this number")
		}
		if index < 0 {
			if c.Source == "" {
				c.Source = "local"
			}
			c.ID = nextContactID(contacts)
			c.DirectoryID = ""
			c.Updated = time.Now()
			return append(contacts, c), nil
		}
		existing := &contacts[index]
		existing.Name, existing.Company, existing.Address, existing.Numbers = c.Name, c.Company, c.Address, c.Numbers
		existing.Notes, existing.Favorite, existing.Account = c.Notes, c.Favorite, c.Account
		existing.Updated = time.Now()
		if existing.Source == "directory-sync" {
			existing.Source = "local"
		}
		return contacts, nil
	})
}

func (s *Store) DeleteContact(id int64) error {
	var directoryID string
	err := s.updateContacts(func(contacts []Contact) ([]Contact, error) {
		index := slices.IndexFunc(contacts, func(c Contact) bool { return c.ID == id })
		if index < 0 {
			return contacts, nil
		}
		directoryID = contacts[index].DirectoryID
		return slices.Delete(contacts, index, index+1), nil
	})
	if err != nil || directoryID == "" {
		return err
	}
	// Later directory refreshes must not bring back a contact the user deleted.
	_, err = s.db.Exec(`UPDATE directory_entries SET hidden=1 WHERE remote_id=?`, directoryID)
	return err
}
