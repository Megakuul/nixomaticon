package store

import (
	"database/sql"
	"errors"
	"time"
)

func (s *Store) LastDialed(account string) (string, error) {
	var remote string
	err := s.db.QueryRow(`SELECT remote FROM history WHERE account=? AND direction='outgoing' ORDER BY id DESC LIMIT 1`, account).Scan(&remote)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return remote, err
}

func (s *Store) HistoryPage(account, search string, beforeID int64, limit int) ([]History, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := s.db.Query(`SELECT id,account,remote,direction,status,started,ended FROM history WHERE (?='' OR account=?) AND (?=0 OR id<?) AND instr(lower(remote),lower(?))>0 ORDER BY id DESC LIMIT ?`, account, account, beforeID, beforeID, search, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []History{}
	for rows.Next() {
		var h History
		var started, ended string
		if err = rows.Scan(&h.ID, &h.Account, &h.Remote, &h.Direction, &h.Status, &started, &ended); err != nil {
			return nil, err
		}
		h.Started, _ = time.Parse(time.RFC3339Nano, started)
		h.Ended, _ = time.Parse(time.RFC3339Nano, ended)
		out = append(out, h)
	}
	return out, rows.Err()
}
func (s *Store) DeleteHistory(id int64) error {
	if id <= 0 {
		return errors.New("select a history entry")
	}
	_, err := s.db.Exec(`DELETE FROM history WHERE id=?`, id)
	return err
}
