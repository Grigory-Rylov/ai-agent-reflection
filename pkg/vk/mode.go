package vk

import (
	"sync"

	"github.com/opencode/llama-client/pkg/workflow"
)

// ============================================================
// Mode — стратегия режимов работы
// ============================================================

// Mode определяет текущий режим работы
type Mode int

const (
	ModeNormal Mode = iota // обычный режим — один агент отвечает
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

// ModeState хранит состояние режима для каждого пользователя
type ModeState struct {
	mu         sync.RWMutex
	peerMode   map[int64]Mode       // peerID -> режим
	peerWf     map[int64]string     // peerID -> active workflow ID
	workflowM  *workflow.WorkflowManager
}

// NewModeState создаёт стратегию режимов
func NewModeState(wfMgr *workflow.WorkflowManager) *ModeState {
	return &ModeState{
		peerMode:  make(map[int64]Mode),
		peerWf:    make(map[int64]string),
		workflowM: wfMgr,
	}
}

// GetMode возвращает текущий режим пользователя
func (s *ModeState) GetMode(peerID int64) Mode {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.peerMode[peerID]
}

// SetMode устанавливает режим пользователя
func (s *ModeState) SetMode(peerID int64, mode Mode, workflowID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	oldMode := s.peerMode[peerID]
	oldWf := s.peerWf[peerID]

	// Если уже в том же режиме — не меняем
	if oldMode == mode && oldWf == workflowID {
		return
	}

	s.peerMode[peerID] = mode
	if workflowID != "" {
		s.peerWf[peerID] = workflowID
	} else {
		delete(s.peerWf, peerID)
	}
}

// GetWorkflowID возвращает активный workflow пользователя (если есть)
func (s *ModeState) GetWorkflowID(peerID int64) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.peerWf[peerID]
}

// HasActiveWorkflow возвращает true если у пользователя активный workflow
func (s *ModeState) HasActiveWorkflow(peerID int64) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, exists := s.peerWf[peerID]
	return exists
}

// ExitMode завершает агентный режим для пользователя
func (s *ModeState) ExitMode(peerID int64) {
	s.mu.Lock()
	delete(s.peerMode, peerID)
	delete(s.peerWf, peerID)
	s.mu.Unlock()
}

// ============================================================
// GlobalAgentState — глобальное состояние для agent_result tool
// ============================================================

// GlobalAgentState хранит ссылки на workflow manager и mode state
type GlobalAgentState struct {
	mu       sync.RWMutex
	wfMgr    *workflow.WorkflowManager
	modeState *ModeState
}

// SetGlobalAgentState устанавливает глобальное состояние (вызывается из main.go)
func SetGlobalAgentState(wfMgr *workflow.WorkflowManager, ms *ModeState) {
	globalAgentState = &GlobalAgentState{
		wfMgr:    wfMgr,
		modeState: ms,
	}
}

// GetGlobalAgentState возвращает глобальное состояние
func GetGlobalAgentState() *GlobalAgentState {
	return globalAgentState
}

var globalAgentState *GlobalAgentState

// GetAgentManager возвращает workflow manager из глобального состояния
func GetAgentManager() *workflow.WorkflowManager {
	if globalAgentState == nil {
		return nil
	}
	return globalAgentState.wfMgr
}

// GetModeState возвращает state manager из глобального состояния
func GetModeState() *ModeState {
	if globalAgentState == nil {
		return nil
	}
	return globalAgentState.modeState
}
