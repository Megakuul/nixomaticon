package store

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/emersion/go-vcard"
	"github.com/megakuul/voiper/internal/compat/swyx"
)

type ContactImportEntry struct {
	Record         int
	Contact        Contact
	Action, Detail string
}

func ParseCSV(text string) ([]Contact, error) {
	entries, err := parseContactFile("csv", text)
	if err != nil {
		return nil, err
	}
	return parsedContacts(entries)
}

func parsedContacts(entries []ContactImportEntry) ([]Contact, error) {
	contacts := make([]Contact, 0, len(entries))
	for _, entry := range entries {
		if entry.Action == "invalid" {
			return nil, fmt.Errorf("record %d: %s", entry.Record, entry.Detail)
		}
		contacts = append(contacts, entry.Contact)
	}
	return contacts, nil
}

func parseContactFile(format, text string) ([]ContactImportEntry, error) {
	if len(text) > 4<<20 {
		return nil, errors.New("import exceeds 4 MiB")
	}
	var entries []ContactImportEntry
	var err error
	switch format {
	case "csv":
		entries, err = parseCSVEntries(text)
	case "vcard":
		entries, err = parseVCardEntries(text)
	case "xml":
		entries, err = parsePhonebookEntries(text)
	default:
		return nil, errors.New("choose CSV, vCard or Swyx XML import")
	}
	if err != nil {
		return nil, err
	}
	if len(entries) > 10000 {
		return nil, errors.New("import exceeds 10000 contacts")
	}
	for i := range entries {
		entry := &entries[i]
		c := &entry.Contact
		c.Address = strings.TrimSpace(c.Address)
		c.Name = strings.TrimSpace(c.Name)
		if c.Name == "" {
			c.Name = c.Address
		}
		c.Source = "import"
		if entry.Action != "invalid" {
			if err := validContact(*c); err != nil {
				entry.Action, entry.Detail = "invalid", err.Error()
			}
		}
	}
	return entries, nil
}

func parseCSVEntries(text string) ([]ContactImportEntry, error) {
	r := csv.NewReader(strings.NewReader(strings.TrimPrefix(text, "\ufeff")))
	r.TrimLeadingSpace = true
	header, err := r.Read()
	if err != nil {
		return nil, err
	}
	fields := map[string]int{}
	for i, field := range header {
		name := strings.ToLower(strings.TrimSpace(field))
		if _, exists := fields[name]; exists {
			return nil, fmt.Errorf("duplicate CSV column %q", name)
		}
		fields[name] = i
	}
	if _, ok := fields["address"]; !ok {
		return nil, errors.New("CSV requires an address column; optional columns: name, notes, group, label")
	}
	entries := []ContactImportEntry{}
	groups := map[string]int{}
	for record := 2; ; record++ {
		row, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			if len(entries) >= 10000 {
				return nil, errors.New("import exceeds 10000 contacts")
			}
			entries = append(entries, ContactImportEntry{Record: record, Action: "invalid", Detail: err.Error()})
			if errors.Is(err, csv.ErrFieldCount) {
				continue
			}
			break
		}
		field := func(name string) string {
			if i, ok := fields[name]; ok {
				return row[i]
			}
			return ""
		}
		c := Contact{Name: field("name"), Address: strings.TrimSpace(field("address")), Notes: field("notes")}
		group := strings.TrimSpace(field("group"))
		if group != "" {
			if index, exists := groups[group]; exists {
				entry := &entries[index]
				if err := validContact(c); err != nil {
					entry.Action, entry.Detail = "invalid", fmt.Sprintf("row %d: %s", record, err)
				}
				if len(entry.Contact.Numbers) >= 20 {
					entry.Action, entry.Detail = "invalid", "a contact supports at most 20 additional numbers"
				} else {
					entry.Contact.Numbers = append(entry.Contact.Numbers, ContactNumber{Address: c.Address, Label: strings.TrimSpace(field("label"))})
				}
				continue
			}
			groups[group] = len(entries)
		}
		if len(entries) >= 10000 {
			return nil, errors.New("import exceeds 10000 contacts")
		}
		entries = append(entries, ContactImportEntry{Record: record, Contact: c})
	}
	return entries, nil
}

func parseVCardEntries(text string) ([]ContactImportEntry, error) {
	decoder := vcard.NewDecoder(strings.NewReader(strings.TrimPrefix(text, "\ufeff")))
	entries := []ContactImportEntry{}
	for record := 1; ; record++ {
		card, err := decoder.Decode()
		if err == io.EOF {
			break
		}
		if len(entries) >= 10000 {
			return nil, errors.New("import exceeds 10000 contacts")
		}
		if err != nil {
			entries = append(entries, ContactImportEntry{Record: record, Action: "invalid", Detail: err.Error()})
			break
		}
		entry := ContactImportEntry{Record: record, Contact: Contact{Name: strings.TrimSpace(card.PreferredValue(vcard.FieldFormattedName)), Notes: card.Value(vcard.FieldNote)}}
		if version := card.Value(vcard.FieldVersion); version != "3.0" && version != "4.0" {
			entry.Action, entry.Detail = "invalid", "export contacts as vCard 3.0 or 4.0"
		}
		addresses := card.Values(vcard.FieldTelephone)
		for _, address := range card.Values(vcard.FieldIMPP) {
			lower := strings.ToLower(strings.TrimSpace(address))
			if strings.HasPrefix(lower, "sip:") || strings.HasPrefix(lower, "sips:") {
				addresses = append(addresses, address)
			}
		}
		seen := map[string]bool{}
		for _, address := range addresses {
			address = strings.TrimSpace(address)
			if strings.HasPrefix(strings.ToLower(address), "tel:") {
				address = strings.TrimSpace(address[4:])
			}
			if seen[address] {
				continue
			}
			seen[address] = true
			if len(seen) == 1 {
				entry.Contact.Address = address
			} else {
				entry.Contact.Numbers = append(entry.Contact.Numbers, ContactNumber{Address: address})
			}
		}
		if len(addresses) == 0 {
			entry.Action, entry.Detail = "invalid", "no phone number or SIP address"
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// parsePhonebookEntries converts a Swyx phonebook export. The number with
// published presence becomes the primary address, so status and dialing use
// the extension; the remaining numbers keep readable labels.
func parsePhonebookEntries(text string) ([]ContactImportEntry, error) {
	phonebook, err := swyx.ParsePhonebook([]byte(text))
	if err != nil {
		return nil, err
	}
	entries := make([]ContactImportEntry, 0, len(phonebook))
	for i, item := range phonebook {
		entry := ContactImportEntry{Record: i + 1, Contact: Contact{Name: item.Name, Company: item.Company, Numbers: []ContactNumber{}}}
		primary := slices.IndexFunc(item.Numbers, func(n swyx.PhonebookNumber) bool { return n.Presence })
		if primary < 0 {
			primary = 0
		}
		for j, number := range item.Numbers {
			if j == primary {
				entry.Contact.Address = number.Number
				continue
			}
			label := strings.Join(number.Types, "/")
			if slices.Contains(number.Types, "Cell") {
				label = "Mobile"
			} else if slices.Contains(number.Types, "IPPhone") {
				label = "Extension"
			}
			entry.Contact.Numbers = append(entry.Contact.Numbers, ContactNumber{Label: label, Address: number.Number})
		}
		if len(item.Numbers) == 0 {
			entry.Action, entry.Detail = "invalid", "no phone number"
		}
		entries = append(entries, entry)
	}
	return entries, nil
}
