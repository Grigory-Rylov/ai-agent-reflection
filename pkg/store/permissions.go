package store

import (
	"database/sql"
	"fmt"
)

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
