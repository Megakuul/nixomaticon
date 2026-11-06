package store

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// ImportContacts validates everything before writing and leaves matching local
// entries untouched. Shared display names never imply a shared identity.
func (s *Store) ImportContacts(contacts []Contact) (int, error) {
	if len(contacts) > 10000 {
		return 0, fmt.Errorf("import exceeds 10000 contacts")
	}
	for i := range contacts {
		c := &contacts[i]
		c.Address = strings.TrimSpace(c.Address)
		c.Name = strings.TrimSpace(c.Name)
		if c.Name == "" {
			c.Name = c.Address
		}
		if c.Source == "" {
			c.Source = "import"
		}
		if err := validContact(*c); err != nil {
			return 0, fmt.Errorf("contact %d: %w", i+1, err)
		}
	}
	count := 0
	err := s.updateContacts(func(existing []Contact) ([]Contact, error) {
		now := time.Now()
		for _, c := range contacts {
			if slices.ContainsFunc(existing, func(other Contact) bool { return other.Address == c.Address }) {
				continue
			}
			c.ID = nextContactID(existing)
			c.DirectoryID = ""
			c.Numbers = trimNumbers(c.Numbers)
			c.Updated = now
			existing = append(existing, c)
			count++
		}
		return existing, nil
	})
	return count, err
}
