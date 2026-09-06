package store

import (
	"context"
	"database/sql"
	"fmt"
)

func (s *sqliteDB) AddMessage(peerID int64, msg MessageData) error {
	ctx, cancel := withDBTimeout()
	defer cancel()

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO messages (peer_id, role, content, tool_call_id,
		                      tool_name, tool_calls, timestamp,
		                      summary, compacted, tail_start_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		peerID, msg.Role, msg.Content, msg.ToolCallID,
		msg.ToolName, msg.ToolCalls, msg.Timestamp,
		boolToInt(msg.Summary), boolToInt(msg.Compacted), msg.TailStartID)
	return err
}

func (s *sqliteDB) SavePeerMessages(peerID int64, msgs []MessageData) error {
	ctx, cancel := withDBTimeout()
	defer cancel()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin save messages: %w", err)
	}

	if err := s.clearPeerMessagesTx(ctx, tx, peerID); err != nil {
		tx.Rollback()
		return err
	}
	if err := s.insertPeerMessagesTx(ctx, tx, peerID, msgs); err != nil {
		tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit save messages: %w", err)
	}
	return nil
}

func (s *sqliteDB) clearPeerMessagesTx(ctx context.Context, tx *sql.Tx, peerID int64) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM messages WHERE peer_id = ?`, peerID); err != nil {
		return fmt.Errorf("clear messages: %w", err)
	}
	return nil
}

func (s *sqliteDB) insertPeerMessagesTx(ctx context.Context, tx *sql.Tx, peerID int64, msgs []MessageData) error {
	for _, msg := range msgs {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO messages (peer_id, role, content, tool_call_id,
			                      tool_name, tool_calls, timestamp,
			                      summary, compacted, tail_start_id)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			peerID, msg.Role, msg.Content, msg.ToolCallID,
			msg.ToolName, msg.ToolCalls, msg.Timestamp,
			boolToInt(msg.Summary), boolToInt(msg.Compacted), msg.TailStartID); err != nil {
			return fmt.Errorf("insert message: %w", err)
		}
	}
	return nil
}

func (s *sqliteDB) GetMessages(peerID int64) ([]MessageData, error) {
	ctx, cancel := withDBTimeout()
	defer cancel()

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, peer_id, role, content, tool_call_id,
		       tool_name, tool_calls, timestamp,
		       summary, compacted, tail_start_id
		FROM messages WHERE peer_id = ? ORDER BY id`, peerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []MessageData
	for rows.Next() {
		var m MessageData
		var summary, compacted int
		if err := rows.Scan(&m.ID, &m.PeerID, &m.Role, &m.Content,
			&m.ToolCallID, &m.ToolName, &m.ToolCalls, &m.Timestamp,
			&summary, &compacted, &m.TailStartID); err != nil {
			return nil, err
		}
		m.Summary = summary != 0
		m.Compacted = compacted != 0
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}

func (s *sqliteDB) ClearMessages(peerID int64) error {
	ctx, cancel := withDBTimeout()
	defer cancel()

	_, err := s.db.ExecContext(ctx, `DELETE FROM messages WHERE peer_id = ?`, peerID)
	return err
}