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
		agentSessionsTable,
		agentChainTable,
		messagesIndex,
		todosIndex,
		permissionsIndex,
		agentSessionsIndex,
		agentChainIndex,
	}

	for _, q := range queries {
		if _, err := db.Exec(q); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}

	for _, col := range []string{"pinned", "resume_prompt"} {
		if err := ensureColumn(db, "sessions", col); err != nil {
			return fmt.Errorf("migrate %s column: %w", col, err)
		}
	}

	return nil
}

// ensureColumn добавляет колонку в таблицу, если её ещё нет
// (SQLite не поддерживает ADD COLUMN IF NOT EXISTS).
func ensureColumn(db *sql.DB, table, column string) error {
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt *string
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return err
		}
		if name == column {
			return nil
		}
	}
	_, err = db.Exec(`ALTER TABLE ` + table + ` ADD COLUMN ` + column + ` TEXT DEFAULT ''`)
	return err
}

const sessionsTable = `CREATE TABLE IF NOT EXISTS sessions (
	peer_id INTEGER PRIMARY KEY,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	working_dir TEXT DEFAULT '',
	loop_count INTEGER DEFAULT 0,
	is_looped INTEGER DEFAULT 0,
	last_looped TEXT DEFAULT '',
	pinned TEXT DEFAULT '',
	resume_prompt TEXT DEFAULT ''
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

// agent_sessions — сессии сабагентов с UUID
const agentSessionsTable = `CREATE TABLE IF NOT EXISTS agent_sessions (
	id TEXT PRIMARY KEY,
	parent_id TEXT DEFAULT '',
	agent_name TEXT NOT NULL,
	peer_id INTEGER NOT NULL,
	system_prompt TEXT NOT NULL DEFAULT '',
	last_prompt TEXT DEFAULT '',
	last_tool_call TEXT DEFAULT '',
	status TEXT NOT NULL DEFAULT 'active',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	messages TEXT DEFAULT ''
)`

// active_agent_chain — активные цепочки вызовов агентов
const agentChainTable = `CREATE TABLE IF NOT EXISTS active_agent_chain (
	peer_id INTEGER PRIMARY KEY,
	chain TEXT NOT NULL DEFAULT '',
	updated_at TEXT NOT NULL
)`

const agentSessionsIndex = `CREATE INDEX IF NOT EXISTS idx_agent_sessions_peer_id ON agent_sessions(peer_id)`
const agentChainIndex = `CREATE INDEX IF NOT EXISTS idx_agent_chain_updated ON active_agent_chain(updated_at)`
