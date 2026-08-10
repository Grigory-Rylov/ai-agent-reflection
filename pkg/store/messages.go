package store

func (s *sqliteDB) AddMessage(peerID int64, msg MessageData) error {
	_, err := s.db.Exec(`
		INSERT INTO messages (peer_id, role, content, tool_call_id,
		                      tool_name, tool_calls, timestamp,
		                      summary, compacted, tail_start_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		peerID, msg.Role, msg.Content, msg.ToolCallID,
		msg.ToolName, msg.ToolCalls, msg.Timestamp,
		boolToInt(msg.Summary), boolToInt(msg.Compacted), msg.TailStartID)
	return err
}

func (s *sqliteDB) GetMessages(peerID int64) ([]MessageData, error) {
	rows, err := s.db.Query(`
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
	_, err := s.db.Exec(`DELETE FROM messages WHERE peer_id = ?`, peerID)
	return err
}
