package store

import (
	"strings"

	"github.com/emersion/go-vcard"
)

// ParseVCard preserves the identity of each card and all its callable numbers.
func ParseVCard(text string) ([]Contact, error) {
	entries, err := parseContactFile("vcard", text)
	if err != nil {
		return nil, err
	}
	return parsedContacts(entries)
}

func (s *Store) ImportVCard(text string) (int, error) {
	contacts, err := ParseVCard(text)
	if err != nil {
		return 0, err
	}
	return s.ImportContacts(contacts)
}

func (s *Store) ExportVCard() (string, error) {
	var out strings.Builder
	encoder := vcard.NewEncoder(&out)
	for offset := 0; ; offset += 500 {
		contacts, err := s.ContactsPage("", offset, 500)
		if err != nil {
			return "", err
		}
		for _, c := range contacts {
			card := vcard.Card{}
			card.SetValue(vcard.FieldVersion, "4.0")
			card.SetValue(vcard.FieldFormattedName, c.Name)
			card.SetValue(vcard.FieldNote, c.Notes)
			numbers := append([]ContactNumber{{Address: c.Address}}, c.Numbers...)
			for _, n := range numbers {
				field := vcard.FieldTelephone
				params := vcard.Params{vcard.ParamValue: {"text"}}
				lower := strings.ToLower(n.Address)
				if strings.HasPrefix(lower, "sip:") || strings.HasPrefix(lower, "sips:") {
					field = vcard.FieldIMPP
					params = nil
				}
				card.Add(field, &vcard.Field{Value: n.Address, Params: params})
			}
			if err := encoder.Encode(card); err != nil {
				return "", err
			}
		}
		if len(contacts) < 500 {
			break
		}
	}
	return out.String(), nil
}
