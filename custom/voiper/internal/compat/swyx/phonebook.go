package swyx

import (
	"encoding/xml"
	"errors"
	"fmt"
	"strings"
)

// PhonebookEntry is one contact from a Swyx phonebook XML export.
type PhonebookEntry struct {
	Name, Company string
	Numbers       []PhonebookNumber
}

// PhonebookNumber keeps the export's types, such as Work/IPPhone or Home/Cell.
// Presence is set when the PBX publishes status for this number.
type PhonebookNumber struct {
	Types    []string
	Number   string
	Presence bool
}

// ParsePhonebook reads the <contacts><Contact>… export format. Display names
// take precedence over first and last name.
func ParsePhonebook(data []byte) ([]PhonebookEntry, error) {
	var document struct {
		XMLName  xml.Name `xml:"contacts"`
		Contacts []struct {
			Name struct {
				First   string `xml:"First"`
				Last    string `xml:"Last"`
				Display string `xml:"Display"`
			} `xml:"Name"`
			Company string `xml:"Info>Company"`
			Phones  []struct {
				Types    []string `xml:"Type"`
				Number   string   `xml:"Phone"`
				Presence string   `xml:"Presence"`
			} `xml:"Phone"`
		} `xml:"Contact"`
	}
	if err := xml.Unmarshal(data, &document); err != nil {
		var syntax *xml.SyntaxError
		if errors.As(err, &syntax) {
			return nil, fmt.Errorf("invalid phonebook XML on line %d: %s", syntax.Line, syntax.Msg)
		}
		return nil, errors.New("expected a Swyx phonebook export with a <contacts> root element")
	}
	entries := make([]PhonebookEntry, 0, len(document.Contacts))
	for _, contact := range document.Contacts {
		name := strings.TrimSpace(contact.Name.Display)
		if name == "" {
			name = strings.TrimSpace(strings.TrimSpace(contact.Name.First) + " " + strings.TrimSpace(contact.Name.Last))
		}
		entry := PhonebookEntry{Name: name, Company: strings.TrimSpace(contact.Company)}
		for _, phone := range contact.Phones {
			number := PhonebookNumber{Number: strings.TrimSpace(phone.Number), Presence: strings.TrimSpace(phone.Presence) != ""}
			for _, kind := range phone.Types {
				if kind = strings.TrimSpace(kind); kind != "" {
					number.Types = append(number.Types, kind)
				}
			}
			if number.Number != "" {
				entry.Numbers = append(entry.Numbers, number)
			}
		}
		entries = append(entries, entry)
	}
	return entries, nil
}
