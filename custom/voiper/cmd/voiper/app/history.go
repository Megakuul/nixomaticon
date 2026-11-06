package app

import "github.com/megakuul/voiper/internal/store"

func (a *App) LastDialed(account string) (string, error) {
	if err := a.ready(); err != nil {
		return "", err
	}
	return a.store.LastDialed(account)
}

func (a *App) ContactsPage(query string, offset, limit int) ([]store.Contact, error) {
	if err := a.ready(); err != nil {
		return nil, err
	}
	return a.store.ContactsPage(query, offset, limit)
}
func (a *App) Conversations(account, query string) ([]store.Conversation, error) {
	if err := a.ready(); err != nil {
		return nil, err
	}
	return a.store.Conversations(account, query)
}
func (a *App) MessagesPage(account, remote, query string, beforeID int64, limit int) ([]store.Message, error) {
	if err := a.ready(); err != nil {
		return nil, err
	}
	return a.store.MessagesPage(account, a.phone.ConversationAddress(account, remote), query, beforeID, limit)
}
func (a *App) MarkConversationRead(account, remote string, throughID int64) error {
	if err := a.ready(); err != nil {
		return err
	}
	return a.store.MarkConversationRead(account, a.phone.ConversationAddress(account, remote), throughID)
}
func (a *App) DeleteConversation(account, remote string) error {
	if err := a.ready(); err != nil {
		return err
	}
	return a.store.DeleteConversation(account, a.phone.ConversationAddress(account, remote))
}
func (a *App) HistoryPage(account, query string, beforeID int64, limit int) ([]store.History, error) {
	if err := a.ready(); err != nil {
		return nil, err
	}
	return a.store.HistoryPage(account, query, beforeID, limit)
}
func (a *App) DeleteHistory(id int64) error {
	if err := a.ready(); err != nil {
		return err
	}
	return a.store.DeleteHistory(id)
}
