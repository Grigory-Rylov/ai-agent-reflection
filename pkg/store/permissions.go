package store

import (
	"database/sql"
	"fmt"
)

func (s *sqliteDB) GetPermissions(sessionID string) ([]PermissionRecord, error) {
	rows, err := s.db.Query(`
		SELECT id, session_id, tool_name, decision, resource
		FROM permissions
		WHERE session_id = ?`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("query permissions: %w", err)
	}
	defer rows.Close()

	var result []PermissionRecord
	for rows.Next() {
		var p PermissionRecord
		if err := rows.Scan(&p.ID, &p.SessionID, &p.ToolName, &p.Decision, &p.Resource); err != nil {
			return nil, fmt.Errorf("scan permission: %w", err)
		}
		result = append(result, p)
	}
	return result, rows.Err()
}

func (s *sqliteDB) GetDistinctGrantSessions() ([]string, error) {
	rows, err := s.db.Query(`SELECT DISTINCT session_id FROM permissions`)
	if err != nil {
		return nil, fmt.Errorf("query distinct sessions: %w", err)
	}
	defer rows.Close()

	var result []string
	for rows.Next() {
		var sid string
		if err := rows.Scan(&sid); err != nil {
			return nil, fmt.Errorf("scan session_id: %w", err)
		}
		result = append(result, sid)
	}
	return result, rows.Err()
}

func (s *sqliteDB) GetPermission(sessionID, toolName, resource string) (*PermissionRecord, error) {
	row := s.db.QueryRow(`
		SELECT id, session_id, tool_name, decision, resource
		FROM permissions
		WHERE session_id = ? AND tool_name = ? AND resource = ?`,
		sessionID, toolName, resource)

	var p PermissionRecord
	err := row.Scan(&p.ID, &p.SessionID, &p.ToolName, &p.Decision, &p.Resource)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan permission: %w", err)
	}
	return &p, nil
}

func (s *sqliteDB) SavePermission(sessionID, toolName, resource, decision string) error {
	_, err := s.db.Exec(`
		INSERT INTO permissions (session_id, tool_name, decision, resource)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(session_id, tool_name, resource) DO UPDATE SET
			decision = excluded.decision`,
		sessionID, toolName, decision, resource)
	return err
}

func (s *sqliteDB) ClearPermissions(sessionID string) error {
	_, err := s.db.Exec(`DELETE FROM permissions WHERE session_id = ?`, sessionID)
	return err
}
