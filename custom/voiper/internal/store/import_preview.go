package store

import (
	"errors"
	"fmt"
	"slices"
	"time"
)

type ContactImportPreview struct {
	Entries                          []ContactImportEntry
	Added, Updated, Skipped, Invalid int
	Committed                        bool
}

func (s *Store) PreviewContactImport(format, text string, mergeNumbers bool) (ContactImportPreview, error) {
	return s.importContactFile(format, text, mergeNumbers, false)
}

func (s *Store) ImportContactFile(format, text string, mergeNumbers bool) (ContactImportPreview, error) {
	return s.importContactFile(format, text, mergeNumbers, true)
}

func (s *Store) importContactFile(format, text string, mergeNumbers, commit bool) (ContactImportPreview, error) {
	entries, err := parseContactFile(format, text)
	if err != nil {
		return ContactImportPreview{}, err
	}
	preview := ContactImportPreview{Entries: entries}
	// The preview and the commit are computed under one lock, so the committed
	// result is exactly the one counted here.
	err = s.updateContacts(func(contacts []Contact) ([]Contact, error) {
		seen := map[string]bool{}
		updates := map[int][]ContactNumber{}
		for i := range preview.Entries {
			entry := &preview.Entries[i]
			if entry.Action == "invalid" {
				preview.Invalid++
				continue
			}
			address := entry.Contact.Address
			if seen[address] {
				entry.Action, entry.Detail = "skip", "duplicate primary address in this file; first record takes precedence"
				preview.Skipped++
				continue
			}
			seen[address] = true
			index := slices.IndexFunc(contacts, func(c Contact) bool { return c.Address == address })
			if index < 0 {
				entry.Action, entry.Detail = "add", "new primary address"
				preview.Added++
				continue
			}
			existing := contacts[index]
			entry.Contact.ID = existing.ID
			entry.Action, entry.Detail = "skip", "existing contact preserved"
			if mergeNumbers && len(entry.Contact.Numbers) > 0 {
				numbers := map[string]bool{address: true}
				for _, n := range existing.Numbers {
					numbers[n.Address] = true
				}
				for _, number := range trimNumbers(entry.Contact.Numbers) {
					if !numbers[number.Address] {
						updates[i] = append(updates[i], number)
						numbers[number.Address] = true
					}
				}
				if len(numbers) > 21 {
					entry.Action, entry.Detail = "invalid", "merging would exceed 20 additional numbers; keep existing contacts or edit this record"
					preview.Invalid++
					continue
				}
				if len(updates[i]) > 0 {
					entry.Action, entry.Detail = "update", fmt.Sprintf("add %d numbers; existing name, notes, labels and preferences preserved", len(updates[i]))
					preview.Updated++
					continue
				}
			}
			preview.Skipped++
		}
		if !commit || preview.Invalid > 0 {
			return nil, errPreviewOnly
		}
		now := time.Now()
		for i, entry := range preview.Entries {
			switch entry.Action {
			case "add":
				c := entry.Contact
				c.ID = nextContactID(contacts)
				c.Source = "import"
				c.Numbers = trimNumbers(c.Numbers)
				c.Updated = now
				contacts = append(contacts, c)
				preview.Entries[i].Contact.ID = c.ID
			case "update":
				index := slices.IndexFunc(contacts, func(c Contact) bool { return c.ID == entry.Contact.ID })
				contacts[index].Numbers = append(contacts[index].Numbers, updates[i]...)
				contacts[index].Updated = now
			}
		}
		return contacts, nil
	})
	if errors.Is(err, errPreviewOnly) {
		return preview, nil
	}
	if err != nil {
		return preview, err
	}
	preview.Committed = true
	return preview, nil
}

// errPreviewOnly ends an import transaction without writing contacts.
var errPreviewOnly = errors.New("import preview only")
