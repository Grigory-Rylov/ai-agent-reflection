package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

func (s *sqliteDB) GetSession(peerID int64) (*SessionData, error) {
	row := s.db.QueryRow(
		`SELECT peer_id, created_at, updated_at, working_dir,
		        loop_count, is_looped, last_looped, pinned, resume_prompt
		 FROM sessions WHERE peer_id = ?`, peerID)

	var sd SessionData
	var createdAt, updatedAt, lastLooped, pinned, resumePrompt string
	err := row.Scan(&sd.PeerID, &createdAt, &updatedAt,
		&sd.WorkingDir, &sd.LoopCount, &sd.IsLooped, &lastLooped, &pinned, &resumePrompt)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan session: %w", err)
	}

	sd.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	sd.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	if lastLooped != "" {
		sd.LastLooped = lastLooped
	}
	if pinned != "" {
		_ = json.Unmarshal([]byte(pinned), &sd.Pinned)
	}
	sd.ResumePrompt = resumePrompt
	return &sd, nil
}

func (s *sqliteDB) SaveSession(sd *SessionData) error {
	return upsertSession(s.db, sd)
}

func (s *sqliteDB) ClearSession(peerID int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM messages WHERE peer_id = ?`, peerID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM sessions WHERE peer_id = ?`, peerID); err != nil {
		return err
	}
	return tx.Commit()
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

type execer interface {
	Exec(query string, args ...interface{}) (sql.Result, error)
}


func upsertSession(e execer, sd *SessionData) error {
	pinnedJSON, _ := json.Marshal(sd.Pinned)
	_, err := e.Exec(`
		INSERT INTO sessions (peer_id, created_at, updated_at, working_dir,
		                      loop_count, is_looped, last_looped, pinned, resume_prompt)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(peer_id) DO UPDATE SET
			updated_at = excluded.updated_at,
			working_dir = excluded.working_dir,
			loop_count = excluded.loop_count,
			is_looped = excluded.is_looped,
			last_looped = excluded.last_looped,
			pinned = excluded.pinned,
			resume_prompt = excluded.resume_prompt`,
		sd.PeerID,
		sd.CreatedAt.Format(time.RFC3339),
		sd.UpdatedAt.Format(time.RFC3339),
		sd.WorkingDir,
		sd.LoopCount,
		boolToInt(sd.IsLooped),
		sd.LastLooped,
		string(pinnedJSON),
		sd.ResumePrompt,
	)
	return err
}


func (s *sqliteDB) ReplaceSessionMessages(sd *SessionData, msgs []MessageData) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin replace: %w", err)
	}
	defer tx.Rollback()

	if err := upsertSession(tx, sd); err != nil {
		return fmt.Errorf("upsert session: %w", err)
	}

	if _, err := tx.Exec(`DELETE FROM messages WHERE peer_id = ?`, sd.PeerID); err != nil {
		return fmt.Errorf("clear messages: %w", err)
	}

	for _, msg := range msgs {
		if err := addMessageTo(tx, sd.PeerID, msg); err != nil {
			return fmt.Errorf("add message: %w", err)
		}
	}

	return tx.Commit()
}
