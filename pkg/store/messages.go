package store

func (s *sqliteDB) AddMessage(peerID int64, msg MessageData) error {
	_, err := s.db.Exec(`
		INSERT INTO messages (peer_id, role, content, tool_call_id,
		                      tool_name, tool_calls, timestamp)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		peerID, msg.Role, msg.Content, msg.ToolCallID,
		msg.ToolName, msg.ToolCalls, msg.Timestamp)
	return err
}

func (s *sqliteDB) GetMessages(peerID int64) ([]MessageData, error) {
	rows, err := s.db.Query(`
		SELECT id, peer_id, role, content, tool_call_id,
		       tool_name, tool_calls, timestamp
		FROM messages WHERE peer_id = ? ORDER BY id`, peerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []MessageData
	for rows.Next() {
		var m MessageData
		if err := rows.Scan(&m.ID, &m.PeerID, &m.Role, &m.Content,
			&m.ToolCallID, &m.ToolName, &m.ToolCalls, &m.Timestamp); err != nil {
			return nil, err
		}
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}

func (s *sqliteDB) ClearMessages(peerID int64) error {
	_, err := s.db.Exec(`DELETE FROM messages WHERE peer_id = ?`, peerID)
	return err
}
