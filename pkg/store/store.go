package store

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

type SessionData struct {
	PeerID     int64     `json:"peer_id"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	WorkingDir string    `json:"working_dir,omitempty"`
	LoopCount  int       `json:"loop_count"`
	IsLooped   bool      `json:"is_looped"`
	LastLooped string    `json:"last_looped,omitempty"`
}

type MessageData struct {
	ID         int64  `json:"id"`
	PeerID     int64  `json:"peer_id"`
	Role       string `json:"role"`
	Content    string `json:"content"`
	ToolCallID string `json:"tool_call_id,omitempty"`
	ToolName   string `json:"tool_name,omitempty"`
	ToolCalls  string `json:"tool_calls,omitempty"`
	Timestamp  string `json:"timestamp,omitempty"`
}

type TodoItem struct {
	ID        string `json:"id"`
	SessionID string `json:"session_id"`
	Content   string `json:"content"`
	Status    string `json:"status"`
	Priority  string `json:"priority,omitempty"`
	Position  int    `json:"position"`
}

type PermissionRecord struct {
	ID        int64  `json:"id"`
	SessionID string `json:"session_id"`
	ToolName  string `json:"tool_name"`
	Decision  string `json:"decision"`
	Resource  string `json:"resource,omitempty"`
}

type Store interface {
	Close() error

	GetSession(peerID int64) (*SessionData, error)
	SaveSession(s *SessionData) error
	ClearSession(peerID int64) error

	AddMessage(peerID int64, msg MessageData) error
	GetMessages(peerID int64) ([]MessageData, error)
	ClearMessages(peerID int64) error

	GetTodos(sessionID string) ([]TodoItem, error)
	UpdateTodos(sessionID string, todos []TodoItem) error

	GetPermission(sessionID, toolName, resource string) (*PermissionRecord, error)
	SavePermission(sessionID, toolName, resource, decision string) error
	ClearPermissions(sessionID string) error
}

type sqliteDB struct {
	db *sql.DB
}

func NewStore(dbPath string) (Store, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	s := &sqliteDB{db: db}
	if err := runMigrations(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

func (s *sqliteDB) Close() error {
	return s.db.Close()
}
