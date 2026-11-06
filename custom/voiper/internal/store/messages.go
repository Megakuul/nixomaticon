package store

import (
	"errors"
	"strings"
	"time"
)

type Conversation struct {
	Account, Remote, Preview, Status string
	Updated                          time.Time
	Unread                           int
}

func (s *Store) AddMessage(m Message) (int64, error) {
	if len(m.Body) > 16384 || strings.TrimSpace(m.Body) == "" {
		return 0, errors.New("message must contain text and be at most 16 KiB")
	}
	if m.Account == "" || m.Remote == "" || len(m.Remote) > 2048 {
		return 0, errors.New("message account and recipient are required")
	}
	if m.Created.IsZero() {
		m.Created = time.Now()
	}
	if m.Direction == "outgoing" {
		m.Read = true
	}
	result, err := s.db.Exec(`INSERT INTO messages(account,remote,body,direction,status,created,read) VALUES(?,?,?,?,?,?,?)`, m.Account, m.Remote, m.Body, m.Direction, m.Status, m.Created.Format(time.RFC3339Nano), m.Read)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}
func (s *Store) MessageStatus(id int64, status string) error {
	_, err := s.db.Exec(`UPDATE messages SET status=? WHERE id=?`, status, id)
	return err
}
func (s *Store) Messages(account, remote string) ([]Message, error) {
	return s.MessagesPage(account, remote, "", 0, 200)
}
func (s *Store) MessagesPage(account, remote, search string, beforeID int64, limit int) ([]Message, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	if len(search) > 512 {
		return nil, errors.New("message search is too long")
	}
	rows, err := s.db.Query(`SELECT id,account,remote,body,direction,status,created,read FROM
(SELECT * FROM messages WHERE account=? AND (?='' OR remote=?) AND (?=0 OR id<?) AND instr(lower(body),lower(?))>0 ORDER BY id DESC LIMIT ?) ORDER BY id`, account, remote, remote, beforeID, beforeID, search, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Message{}
	for rows.Next() {
		var m Message
		var created string
		if err = rows.Scan(&m.ID, &m.Account, &m.Remote, &m.Body, &m.Direction, &m.Status, &created, &m.Read); err != nil {
			return nil, err
		}
		m.Created, _ = time.Parse(time.RFC3339Nano, created)
		out = append(out, m)
	}
	return out, rows.Err()
}
func (s *Store) Conversations(account, search string) ([]Conversation, error) {
	rows, err := s.db.Query(`SELECT m.account,m.remote,m.body,m.status,m.created,g.unread FROM messages m JOIN
(SELECT account,remote,max(id) AS last_id,sum(CASE WHEN read=0 AND direction='incoming' THEN 1 ELSE 0 END) AS unread FROM messages WHERE (?='' OR account=?) GROUP BY account,remote) g ON m.id=g.last_id
WHERE instr(lower(m.remote),lower(?))>0 OR instr(lower(m.body),lower(?))>0 ORDER BY m.id DESC LIMIT 200`, account, account, search, search)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Conversation{}
	for rows.Next() {
		var c Conversation
		var created string
		if err = rows.Scan(&c.Account, &c.Remote, &c.Preview, &c.Status, &created, &c.Unread); err != nil {
			return nil, err
		}
		if len([]rune(c.Preview)) > 120 {
			c.Preview = string([]rune(c.Preview)[:120]) + "…"
		}
		c.Updated, _ = time.Parse(time.RFC3339Nano, created)
		out = append(out, c)
	}
	return out, rows.Err()
}
func (s *Store) MarkConversationRead(account, remote string, throughID int64) error {
	_, err := s.db.Exec(`UPDATE messages SET read=1 WHERE account=? AND remote=? AND id<=?`, account, remote, throughID)
	return err
}
func (s *Store) UnreadCount() (int, error) {
	var count int
	err := s.db.QueryRow(`SELECT count(*) FROM messages WHERE read=0 AND direction='incoming'`).Scan(&count)
	return count, err
}
func (s *Store) DeleteConversation(account, remote string) error {
	if account == "" || remote == "" {
		return errors.New("select a conversation first")
	}
	_, err := s.db.Exec(`DELETE FROM messages WHERE account=? AND remote=?`, account, remote)
	return err
}
func (s *Store) ClearMessages() error { _, err := s.db.Exec(`DELETE FROM messages`); return err }
func (s *Store) PruneMessages(days int) error {
	if days < 0 || days > 3650 {
		return errors.New("message retention must be between 0 and 3650 days")
	}
	if days == 0 {
		return nil
	}
	_, err := s.db.Exec(`DELETE FROM messages WHERE created<?`, time.Now().AddDate(0, 0, -days).Format(time.RFC3339Nano))
	return err
}
