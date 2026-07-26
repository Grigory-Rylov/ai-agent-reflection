package store

func (s *sqliteDB) GetTodos(sessionID string) ([]TodoItem, error) {
	rows, err := s.db.Query(`
		SELECT id, session_id, content, status, priority, position
		FROM todos WHERE session_id = ? ORDER BY position`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var todos []TodoItem
	for rows.Next() {
		var t TodoItem
		if err := rows.Scan(&t.ID, &t.SessionID, &t.Content,
			&t.Status, &t.Priority, &t.Position); err != nil {
			return nil, err
		}
		todos = append(todos, t)
	}
	return todos, rows.Err()
}

func (s *sqliteDB) UpdateTodos(sessionID string, todos []TodoItem) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM todos WHERE session_id = ?`, sessionID); err != nil {
		return err
	}

	for i, t := range todos {
		t.Position = i
		if _, err := tx.Exec(`
			INSERT INTO todos (id, session_id, content, status, priority, position)
			VALUES (?, ?, ?, ?, ?, ?)`,
			t.ID, sessionID, t.Content, t.Status, t.Priority, t.Position); err != nil {
			return err
		}
	}

	return tx.Commit()
}
