package workflow

import (
	"fmt"
	"sync"
	"time"
)

// ============================================================
// Режимы работы мульти-агентной системы
// ============================================================

// Mode определяет режим работы системы
type Mode int

const (
	ModeNormal Mode = iota // обычный режим — один агент отвечает пользователю
	ModeAgent              // агентный режим — координатор + разработчик + ревьюер
)

func (m Mode) String() string {
	switch m {
	case ModeNormal:
		return "normal"
	case ModeAgent:
		return "agent"
	default:
		return "unknown"
	}
}

// ModeStrategy — стратегия управления режимами
type ModeStrategy struct {
	mu         sync.RWMutex
	peers      map[int64]*PeerMode // peerID -> режим
	activeWf   map[int64]string    // peerID -> workflowID активного workflow
}

// PeerMode хранит режим для каждого пользователя
type PeerMode struct {
	Mode        Mode
	WorkflowID  string
	EnteredAt   time.Time
	LastUpdated time.Time
}

// NewModeStrategy создаёт стратегию режимов
func NewModeStrategy() *ModeStrategy {
	return &ModeStrategy{
		peers:    make(map[int64]*PeerMode),
		activeWf: make(map[int64]string),
	}
}

// GetMode возвращает текущий режим пользователя
func (s *ModeStrategy) GetMode(peerID int64) Mode {
	s.mu.RLock()
	defer s.mu.RUnlock()

	pm, exists := s.peers[peerID]
	if !exists {
		return ModeNormal
	}
	return pm.Mode
}

// SetMode устанавливает режим пользователя
func (s *ModeStrategy) SetMode(peerID int64, mode Mode, workflowID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if current, exists := s.peers[peerID]; exists && current.Mode == mode && mode == ModeAgent {
		// Не меняем workflow если уже активен тот же
		return
	}

	s.peers[peerID] = &PeerMode{
		Mode:        mode,
		WorkflowID:  workflowID,
		EnteredAt:   time.Now(),
		LastUpdated: time.Now(),
	}

	if mode == ModeAgent {
		s.activeWf[peerID] = workflowID
	} else {
		delete(s.activeWf, peerID)
	}
}

// GetActiveWorkflow возвращает активный workflow пользователя (если есть)
func (s *ModeStrategy) GetActiveWorkflow(peerID int64) string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	pm, exists := s.peers[peerID]
	if !exists || pm.Mode != ModeAgent {
		return ""
	}
	return pm.WorkflowID
}

// IsActive проверяет что пользователь в агентном режиме
func (s *ModeStrategy) IsActive(peerID int64) bool {
	return s.GetMode(peerID) == ModeAgent
}

// ExitMode завершает агентный режим для пользователя
func (s *ModeStrategy) ExitMode(peerID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if pm, exists := s.peers[peerID]; exists {
		if pm.Mode == ModeAgent && pm.WorkflowID != "" {
			// Workflow завершён успешно — удаляем из activeWf
			delete(s.activeWf, peerID)
			fmt.Printf("[MODE] Agent mode exited for peer %d, workflow %s\n", peerID, pm.WorkflowID)
		}
		pm.Mode = ModeNormal
		pm.WorkflowID = ""
	}
}
