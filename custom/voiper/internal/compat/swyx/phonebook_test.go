package swyx

import "testing"

const phonebook = `<?xml version="1.0" encoding="utf-8"?>
<contacts>
    <Contact id="312">
        <Name><First>Martin</First><Last>Muster</Last><Display>Martin Muster (MaMu)</Display></Name>
        <Info><Company>Finance</Company></Info>
        <Phone><Type>Home</Type><Type>Cell</Type><Phone>0079 123 45 67</Phone></Phone>
        <Phone><Type>Work</Type><Type>IPPhone</Type><Phone>312</Phone><Presence>312</Presence></Phone>
        <Avatar></Avatar>
    </Contact>
    <Contact id="611">
        <Name><First>Anna</First><Last>Beispiel</Last></Name>
        <Phone><Type>Work</Type><Type>IPPhone</Type><Phone>611</Phone></Phone>
    </Contact>
</contacts>`

func TestParsePhonebook(t *testing.T) {
	entries, err := ParsePhonebook([]byte(phonebook))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries: %+v", entries)
	}
	first := entries[0]
	if first.Name != "Martin Muster (MaMu)" || first.Company != "Finance" || len(first.Numbers) != 2 {
		t.Fatalf("first entry: %+v", first)
	}
	if mobile := first.Numbers[0]; mobile.Number != "0079 123 45 67" || mobile.Presence || len(mobile.Types) != 2 || mobile.Types[1] != "Cell" {
		t.Fatalf("mobile number: %+v", mobile)
	}
	if extension := first.Numbers[1]; extension.Number != "312" || !extension.Presence {
		t.Fatalf("extension: %+v", extension)
	}
	if entries[1].Name != "Anna Beispiel" {
		t.Fatalf("name without display: %q", entries[1].Name)
	}
}

func TestParsePhonebookRejectsOtherDocuments(t *testing.T) {
	for _, document := range []string{`<phonebook><Contact/></phonebook>`, `<contacts><Contact>`} {
		if _, err := ParsePhonebook([]byte(document)); err == nil {
			t.Errorf("accepted %q", document)
		}
	}
}
