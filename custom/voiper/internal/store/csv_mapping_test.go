package store

import (
	"encoding/csv"
	"strings"
	"testing"
)

func TestMapCommonContactCSV(t *testing.T) {
	const input = "First Name,Last Name,Business Phone,Mobile Phone,Home Phone,Notes\r\nAda,Lovelace,100,200,200,\"Line one\nLine two\"\r\nGrace,Hopper,,300,400,Office\r\nEmpty,Contact,,,,Review me\r\n"
	columns, err := CSVColumns("\ufeff" + input)
	if err != nil || len(columns) != 6 || columns[0] != "First Name" {
		t.Fatalf("columns: %v %v", columns, err)
	}
	mapped, err := MapContactCSV(input, CSVMapping{Name: "First Name", AdditionalName: "Last Name", Address: "Business Phone", Notes: "Notes", Numbers: []CSVNumberColumn{{Column: "Mobile Phone", Label: "Mobile"}, {Column: "Home Phone", Label: "Home"}}})
	if err != nil {
		t.Fatal(err)
	}
	entries, err := parseContactFile("csv", mapped)
	if err != nil || len(entries) != 3 {
		t.Fatalf("mapped entries: %+v %v", entries, err)
	}
	if entries[0].Contact.Name != "Ada Lovelace" || entries[0].Contact.Address != "100" || entries[0].Contact.Notes != "Line one\nLine two" || len(entries[0].Contact.Numbers) != 1 || entries[0].Contact.Numbers[0] != (ContactNumber{Address: "200", Label: "Mobile"}) {
		t.Fatalf("first contact: %+v", entries[0])
	}
	if entries[1].Contact.Name != "Grace Hopper" || entries[1].Contact.Address != "300" || len(entries[1].Contact.Numbers) != 1 || entries[1].Contact.Numbers[0] != (ContactNumber{Address: "400", Label: "Home"}) {
		t.Fatalf("fallback primary: %+v", entries[1])
	}
	if entries[2].Action != "invalid" {
		t.Fatal("blank-number record disappeared instead of becoming a preview error")
	}
	records, err := csv.NewReader(strings.NewReader(mapped)).ReadAll()
	if err != nil || records[3][4] != "Mobile" {
		t.Fatalf("first nonempty number lost its output label: %v %v", records, err)
	}
}

func TestCSVMappingRejectsMalformedAndAmbiguousInput(t *testing.T) {
	for name, input := range map[string]string{
		"duplicate header": "Phone, phone\n100,200\n",
		"missing header":   "Name,\nA,100\n",
		"short row":        "Name,Phone\nA\n",
		"broken quote":     "Name,Phone\n\"broken,100\n",
		"oversized":        strings.Repeat("x", maxContactCSVBytes+1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := CSVColumns(input); err == nil {
				t.Fatal("invalid CSV headers/input accepted")
			}
			if mapped, err := MapContactCSV(input, CSVMapping{Address: "Phone"}); err == nil || mapped != "" {
				t.Fatal("malformed CSV produced a partial mapping")
			}
		})
	}
	const input = "Name,Phone,Mobile\nA,100,200\n"
	for name, mapping := range map[string]CSVMapping{
		"missing column":      {Address: "Absent"},
		"duplicate use":       {Name: "Phone", Address: "Phone"},
		"duplicate number":    {Address: "Phone", Numbers: []CSVNumberColumn{{Column: "Phone"}}},
		"no number":           {Name: "Name"},
		"empty number column": {Address: "Phone", Numbers: []CSVNumberColumn{{Label: "Home"}}},
		"oversized label":     {Address: "Phone", Numbers: []CSVNumberColumn{{Column: "Mobile", Label: strings.Repeat("x", 81)}}},
		"too many numbers":    {Address: "Phone", Numbers: make([]CSVNumberColumn, 21)},
	} {
		t.Run(name, func(t *testing.T) {
			if mapped, err := MapContactCSV(input, mapping); err == nil || mapped != "" {
				t.Fatal("invalid mapping accepted")
			}
		})
	}
}

func TestCSVMappingBoundsRowsAndExpandedOutput(t *testing.T) {
	oversized := "Phone\n" + strings.Repeat("100\n", 10001)
	if _, err := CSVColumns(oversized); err == nil {
		t.Fatal("accepted more than 10000 contacts")
	}
	var input strings.Builder
	input.WriteString("Name,Phone,Mobile,Notes\n")
	for range 270 {
		input.WriteString("Alice,100,200,\"" + strings.Repeat("x", 8000) + "\"\n")
	}
	if input.Len() >= maxContactCSVBytes {
		t.Fatal("fixture already exceeds input bound")
	}
	if mapped, err := MapContactCSV(input.String(), CSVMapping{Name: "Name", Address: "Phone", Notes: "Notes", Numbers: []CSVNumberColumn{{Column: "Mobile", Label: "Mobile"}}}); err == nil || mapped != "" {
		t.Fatal("expanded mapping exceeded output bound")
	}
}

func TestCSVMappingNumbersOnlyAndQuotedValues(t *testing.T) {
	mapped, err := MapContactCSV("Display,Telephone,Other\n\"Surname, Given\",,\"sip:person@example.org\"\n", CSVMapping{Name: "Display", Numbers: []CSVNumberColumn{{Column: "Telephone", Label: "Work"}, {Column: "Other", Label: "SIP"}}})
	if err != nil {
		t.Fatal(err)
	}
	contacts, err := ParseCSV(mapped)
	if err != nil || len(contacts) != 1 || contacts[0].Name != "Surname, Given" || contacts[0].Address != "sip:person@example.org" {
		t.Fatalf("quoted fields or fallback lost: %+v %v", contacts, err)
	}
}
