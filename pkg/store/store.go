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
	Pinned     []string  `json:"pinned,omitempty"`
	// ResumePrompt — последний user-промпт незавершённой обработки.
	// Непустое значение после рестарта означает, что задачу надо продолжить.
	ResumePrompt string `json:"resume_prompt,omitempty"`
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
	// Summary/Compacted/TailStartID — метаданные компактизации, чтобы маркеры
	// переживали перезагрузку сессии (резюм после рестарта).
	Summary     bool `json:"summary,omitempty"`
	Compacted   bool `json:"compacted,omitempty"`
	TailStartID int  `json:"tail_start_id,omitempty"`
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

type AgentSessionData struct {
	ID           string    `json:"id"`
	ParentID     string    `json:"parent_id,omitempty"`
	AgentName    string    `json:"agent_name"`
	PeerID       int64     `json:"peer_id"`
	SystemPrompt string    `json:"system_prompt"`
	LastPrompt   string    `json:"last_prompt,omitempty"`
	LastToolCall string    `json:"last_tool_call,omitempty"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	Messages     string    `json:"messages,omitempty"` // JSON array of messages
}

type AgentChainData struct {
	PeerID    int64     `json:"peer_id"`
	Chain     []string  `json:"chain"` // ordered list of session IDs from root to current
	UpdatedAt time.Time `json:"updated_at"`
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
	GetPermissions(sessionID string) ([]PermissionRecord, error)
	GetDistinctGrantSessions() ([]string, error)
	SavePermission(sessionID, toolName, resource, decision string) error
	ClearPermissions(sessionID string) error

	// Agent sessions
	SaveAgentSession(s *AgentSessionData) error
	GetAgentSession(id string) (*AgentSessionData, error)
	GetActiveAgentSessions(peerID int64) ([]AgentSessionData, error)
	CompleteAgentSession(id string) error
	CancelAgentSession(id string) error
	DeleteAgentSession(id string) error
	UpdateAgentSession(id, lastPrompt, messages string) error
	GetAgentChain(peerID int64) (*AgentChainData, error)
	SaveAgentChain(peerID int64, chain []string) error
	ClearAgentChain(peerID int64) error
	GetAllActiveChains() ([]AgentChainData, error)
	// ClearPeerData полностью удаляет все данные пира: сессии сабагентов,
	// активную цепочку и todos. Вызывается /clear, чтобы в БД не осталось
	// ничего, что могло бы возродить задачу после рестарта.
	ClearPeerData(peerID int64) error
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
