package store

import (
	"database/sql"
	"fmt"
)

func runMigrations(db *sql.DB) error {
	queries := []string{
		sessionsTable,
		messagesTable,
		todosTable,
		permissionsTable,
		messagesIndex,
		todosIndex,
		permissionsIndex,
	}

	for _, q := range queries {
		if _, err := db.Exec(q); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	return nil
}

const sessionsTable = `CREATE TABLE IF NOT EXISTS sessions (
	peer_id INTEGER PRIMARY KEY,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	working_dir TEXT DEFAULT '',
	loop_count INTEGER DEFAULT 0,
	is_looped INTEGER DEFAULT 0,
	last_looped TEXT DEFAULT ''
)`

const messagesTable = `CREATE TABLE IF NOT EXISTS messages (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	peer_id INTEGER NOT NULL,
	role TEXT NOT NULL,
	content TEXT NOT NULL DEFAULT '',
	tool_call_id TEXT DEFAULT '',
	tool_name TEXT DEFAULT '',
	tool_calls TEXT DEFAULT '',
	timestamp TEXT NOT NULL,
	FOREIGN KEY (peer_id) REFERENCES sessions(peer_id)
)`

const todosTable = `CREATE TABLE IF NOT EXISTS todos (
	id TEXT NOT NULL,
	session_id TEXT NOT NULL,
	content TEXT NOT NULL,
	status TEXT NOT NULL DEFAULT 'pending',
	priority TEXT DEFAULT 'medium',
	position INTEGER DEFAULT 0,
	PRIMARY KEY (id, session_id)
)`

const permissionsTable = `CREATE TABLE IF NOT EXISTS permissions (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	session_id TEXT NOT NULL,
	tool_name TEXT NOT NULL,
	decision TEXT NOT NULL,
	resource TEXT DEFAULT '',
	UNIQUE(session_id, tool_name, resource)
)`

const messagesIndex = `CREATE INDEX IF NOT EXISTS idx_messages_peer_id ON messages(peer_id)`
const todosIndex = `CREATE INDEX IF NOT EXISTS idx_todos_session ON todos(session_id)`
const permissionsIndex = `CREATE INDEX IF NOT EXISTS idx_permissions_session ON permissions(session_id)`
