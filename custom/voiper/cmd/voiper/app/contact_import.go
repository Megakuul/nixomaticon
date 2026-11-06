package app

import "github.com/megakuul/voiper/internal/store"

func (a *App) PreviewContactImport(format, text string, mergeNumbers bool) (store.ContactImportPreview, error) {
	if err := a.ready(); err != nil {
		return store.ContactImportPreview{}, err
	}
	return a.store.PreviewContactImport(format, text, mergeNumbers)
}

func (a *App) ImportContactFile(format, text string, mergeNumbers bool) (store.ContactImportPreview, error) {
	if err := a.ready(); err != nil {
		return store.ContactImportPreview{}, err
	}
	return a.store.ImportContactFile(format, text, mergeNumbers)
}

func (a *App) CSVColumns(text string) ([]string, error) {
	if err := a.ready(); err != nil {
		return nil, err
	}
	return store.CSVColumns(text)
}

func (a *App) MapContactCSV(text string, mapping store.CSVMapping) (string, error) {
	if err := a.ready(); err != nil {
		return "", err
	}
	return store.MapContactCSV(text, mapping)
}
