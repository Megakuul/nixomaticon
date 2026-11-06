package store

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

type CSVNumberColumn struct {
	Column, Label string
}

type CSVMapping struct {
	Name, AdditionalName, Address, Notes string
	Numbers                              []CSVNumberColumn
}

const maxContactCSVBytes = 4 << 20

// CSVColumns validates a comma-separated file before offering its header fields for mapping.
func CSVColumns(text string) ([]string, error) {
	columns, _, err := readContactCSV(text)
	return columns, err
}

func readContactCSV(text string) ([]string, [][]string, error) {
	if len(text) > maxContactCSVBytes {
		return nil, nil, errors.New("CSV exceeds 4 MiB")
	}
	reader := csv.NewReader(strings.NewReader(strings.TrimPrefix(text, "\ufeff")))
	reader.TrimLeadingSpace = true
	columns, err := reader.Read()
	if err != nil {
		return nil, nil, fmt.Errorf("read CSV header: %w", err)
	}
	if len(columns) > 256 {
		return nil, nil, errors.New("CSV supports at most 256 columns")
	}
	seen := map[string]bool{}
	for i, column := range columns {
		column = strings.TrimSpace(column)
		key := strings.ToLower(column)
		if column == "" {
			return nil, nil, errors.New("CSV header contains an unnamed column")
		}
		if len(column) > 512 {
			return nil, nil, errors.New("CSV column name exceeds 512 bytes")
		}
		if seen[key] {
			return nil, nil, fmt.Errorf("duplicate CSV column %q", column)
		}
		seen[key] = true
		columns[i] = column
	}
	rows := [][]string{}
	for record := 2; ; record++ {
		row, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, fmt.Errorf("CSV record %d: %w", record, err)
		}
		if len(rows) >= 10000 {
			return nil, nil, errors.New("CSV exceeds 10000 contacts")
		}
		rows = append(rows, row)
	}
	return columns, rows, nil
}

// MapContactCSV emits the grouped format used by the import preview. It never writes contacts.
func MapContactCSV(text string, mapping CSVMapping) (string, error) {
	columns, rows, err := readContactCSV(text)
	if err != nil {
		return "", err
	}
	if len(mapping.Numbers) > 20 {
		return "", errors.New("map at most 20 additional number columns")
	}
	fields := map[string]int{}
	for i, column := range columns {
		fields[column] = i
	}
	used := map[string]bool{}
	selected := []string{mapping.Name, mapping.AdditionalName, mapping.Address, mapping.Notes}
	for _, number := range mapping.Numbers {
		if strings.TrimSpace(number.Column) == "" {
			return "", errors.New("choose a column for each additional number")
		}
		if len(strings.TrimSpace(number.Label)) > 80 {
			return "", errors.New("number labels must not exceed 80 bytes")
		}
		selected = append(selected, number.Column)
	}
	indexes := make([]int, len(selected))
	for i, column := range selected {
		column = strings.TrimSpace(column)
		indexes[i] = -1
		if column == "" {
			continue
		}
		index, ok := fields[column]
		if !ok {
			return "", fmt.Errorf("CSV has no column %q", column)
		}
		if used[column] {
			return "", fmt.Errorf("CSV column %q is mapped more than once", column)
		}
		used[column] = true
		indexes[i] = index
	}
	if indexes[2] < 0 && len(mapping.Numbers) == 0 {
		return "", errors.New("choose at least one number or SIP address column")
	}
	output := &csvMappingBuffer{}
	writer := csv.NewWriter(output)
	if err = writer.Write([]string{"name", "address", "notes", "group", "label"}); err != nil {
		return "", err
	}
	for i, row := range rows {
		value := func(position int) string {
			if indexes[position] < 0 {
				return ""
			}
			return row[indexes[position]]
		}
		name := strings.TrimSpace(strings.TrimSpace(value(0)) + " " + strings.TrimSpace(value(1)))
		notes := value(3)
		numbers := []ContactNumber{}
		seen := map[string]bool{}
		for j := 0; j <= len(mapping.Numbers); j++ {
			position, label := 2, ""
			if j > 0 {
				position = 3 + j
				label = strings.TrimSpace(mapping.Numbers[j-1].Label)
			}
			address := strings.TrimSpace(value(position))
			if address == "" || seen[address] {
				continue
			}
			seen[address] = true
			numbers = append(numbers, ContactNumber{Address: address, Label: label})
		}
		// Keep an empty-number row so the existing preview reports the invalid contact.
		if len(numbers) == 0 {
			numbers = append(numbers, ContactNumber{})
		}
		group := strconv.Itoa(i + 1)
		for _, number := range numbers {
			if err = writer.Write([]string{name, number.Address, notes, group, number.Label}); err != nil {
				return "", err
			}
		}
	}
	writer.Flush()
	if err = writer.Error(); err != nil {
		return "", err
	}
	return output.String(), nil
}

type csvMappingBuffer struct{ strings.Builder }

func (b *csvMappingBuffer) Write(data []byte) (int, error) {
	if b.Len()+len(data) > maxContactCSVBytes {
		return 0, errors.New("mapped CSV exceeds 4 MiB; import a smaller file")
	}
	return b.Builder.Write(data)
}
