package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

func (s *sqliteDB) SaveAgentSession(sd *AgentSessionData) error {
	now := time.Now().UTC().Format(time.RFC3339)
	if sd.CreatedAt.IsZero() {
		sd.CreatedAt = time.Now().UTC()
	}
	if sd.UpdatedAt.IsZero() {
		sd.UpdatedAt = time.Now().UTC()
	}
	if sd.Status == "" {
		sd.Status = "active"
	}

	messagesJSON := sd.Messages

	_, err := s.db.Exec(`
		INSERT INTO agent_sessions (id, parent_id, agent_name, peer_id, system_prompt, last_prompt, last_tool_call, status, created_at, updated_at, messages)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			agent_name = excluded.agent_name,
			system_prompt = excluded.system_prompt,
			last_prompt = excluded.last_prompt,
			last_tool_call = excluded.last_tool_call,
			status = excluded.status,
			updated_at = excluded.updated_at,
			messages = excluded.messages`,
		sd.ID, sd.ParentID, sd.AgentName, sd.PeerID, sd.SystemPrompt,
		sd.LastPrompt, sd.LastToolCall, sd.Status,
		sd.CreatedAt.Format(time.RFC3339),
		now,
		string(messagesJSON),
	)
	if err != nil {
		return fmt.Errorf("save agent session: %w", err)
	}
	return nil
}

func (s *sqliteDB) GetAgentSession(id string) (*AgentSessionData, error) {
	row := s.db.QueryRow(`
		SELECT id, parent_id, agent_name, peer_id, system_prompt, last_prompt, last_tool_call, status, created_at, updated_at, messages
		FROM agent_sessions WHERE id = ?`, id)

	var sd AgentSessionData
	var createdAt, updatedAt, messagesJSON string
	err := row.Scan(&sd.ID, &sd.ParentID, &sd.AgentName, &sd.PeerID,
		&sd.SystemPrompt, &sd.LastPrompt, &sd.LastToolCall, &sd.Status,
		&createdAt, &updatedAt, &messagesJSON)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan agent session: %w", err)
	}

	sd.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	sd.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	sd.Messages = messagesJSON
	return &sd, nil
}

func (s *sqliteDB) GetActiveAgentSessions(peerID int64) ([]AgentSessionData, error) {
	rows, err := s.db.Query(`
		SELECT id, parent_id, agent_name, peer_id, system_prompt, last_prompt, last_tool_call, status, created_at, updated_at, messages
		FROM agent_sessions WHERE peer_id = ? AND status = 'active'
		ORDER BY created_at`, peerID)
	if err != nil {
		return nil, fmt.Errorf("query active agent sessions: %w", err)
	}
	defer rows.Close()

	var result []AgentSessionData
	for rows.Next() {
		var sd AgentSessionData
		var createdAt, updatedAt, messagesJSON string
		if err := rows.Scan(&sd.ID, &sd.ParentID, &sd.AgentName, &sd.PeerID,
			&sd.SystemPrompt, &sd.LastPrompt, &sd.LastToolCall, &sd.Status,
			&createdAt, &updatedAt, &messagesJSON); err != nil {
			return nil, fmt.Errorf("scan agent session: %w", err)
		}
		sd.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		sd.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
		sd.Messages = messagesJSON
		result = append(result, sd)
	}
	return result, nil
}

func (s *sqliteDB) CompleteAgentSession(id string) error {
	_, err := s.db.Exec(`UPDATE agent_sessions SET status = 'completed', updated_at = ? WHERE id = ?`,
		time.Now().UTC().Format(time.RFC3339), id)
	return err
}

func (s *sqliteDB) CancelAgentSession(id string) error {
	_, err := s.db.Exec(`UPDATE agent_sessions SET status = 'cancelled', updated_at = ? WHERE id = ?`,
		time.Now().UTC().Format(time.RFC3339), id)
	return err
}

func (s *sqliteDB) DeleteAgentSession(id string) error {
	_, err := s.db.Exec(`DELETE FROM agent_sessions WHERE id = ?`, id)
	return err
}

func (s *sqliteDB) UpdateAgentSession(id, lastPrompt, messages string) error {
	_, err := s.db.Exec(`UPDATE agent_sessions SET last_prompt = ?, messages = ?, updated_at = ? WHERE id = ?`,
		lastPrompt, messages, time.Now().UTC().Format(time.RFC3339), id)
	return err
}

func (s *sqliteDB) GetAgentChain(peerID int64) (*AgentChainData, error) {
	row := s.db.QueryRow(`SELECT peer_id, chain, updated_at FROM active_agent_chain WHERE peer_id = ?`, peerID)

	var chainData AgentChainData
	var chainJSON, updatedAt string
	err := row.Scan(&chainData.PeerID, &chainJSON, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan agent chain: %w", err)
	}

	if chainJSON != "" {
		_ = json.Unmarshal([]byte(chainJSON), &chainData.Chain)
	}
	chainData.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return &chainData, nil
}

func (s *sqliteDB) SaveAgentChain(peerID int64, chain []string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	chainJSON, _ := json.Marshal(chain)
	_, err := s.db.Exec(`
		INSERT INTO active_agent_chain (peer_id, chain, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(peer_id) DO UPDATE SET
			chain = excluded.chain,
			updated_at = excluded.updated_at`,
		peerID, string(chainJSON), now,
	)
	return err
}

func (s *sqliteDB) ClearAgentChain(peerID int64) error {
	// Отменяем все активные сессии для этого peer_id
	_, _ = s.db.Exec(`UPDATE agent_sessions SET status = 'cancelled', updated_at = ? WHERE peer_id = ? AND status = 'active'`,
		time.Now().UTC().Format(time.RFC3339), peerID)

	_, err := s.db.Exec(`DELETE FROM active_agent_chain WHERE peer_id = ?`, peerID)
	return err
}

// ClearPeerData полностью удаляет все данные пира: сессии сабагентов,
// активную цепочку и todos. В отличие от ClearAgentChain (которая лишь
// помечает сессии 'cancelled'), здесь строки удаляются физически — чтобы
// ни ResumeActiveChains, ни /sessions не увидели «остатки» после /clear.
func (s *sqliteDB) ClearPeerData(peerID int64) error {
	if _, err := s.db.Exec(`DELETE FROM agent_sessions WHERE peer_id = ?`, peerID); err != nil {
		return fmt.Errorf("delete agent sessions: %w", err)
	}
	if _, err := s.db.Exec(`DELETE FROM active_agent_chain WHERE peer_id = ?`, peerID); err != nil {
		return fmt.Errorf("delete active agent chain: %w", err)
	}
	if _, err := s.db.Exec(`DELETE FROM todos WHERE session_id = ?`, strconv.FormatInt(peerID, 10)); err != nil {
		return fmt.Errorf("delete todos: %w", err)
	}
	return nil
}

func (s *sqliteDB) GetAllActiveChains() ([]AgentChainData, error) {
	rows, err := s.db.Query(`SELECT peer_id, chain, updated_at FROM active_agent_chain`)
	if err != nil {
		return nil, fmt.Errorf("query active chains: %w", err)
	}
	defer rows.Close()

	var result []AgentChainData
	for rows.Next() {
		var cd AgentChainData
		var chainJSON, updatedAt string
		if err := rows.Scan(&cd.PeerID, &chainJSON, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan agent chain: %w", err)
		}
		if chainJSON != "" {
			_ = json.Unmarshal([]byte(chainJSON), &cd.Chain)
		}
		cd.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
		result = append(result, cd)
	}
	return result, nil
}
