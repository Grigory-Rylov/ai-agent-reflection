package vk

import (
	"sync"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/workflow"
)


type Mode int

const (
	ModeNormal Mode = iota 
	ModeAgent              
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


type ModeState struct {
	mu         sync.RWMutex
	peerMode   map[int64]Mode       
	peerWf     map[int64]string     
	workflowM  *workflow.WorkflowManager
}


func NewModeState(wfMgr *workflow.WorkflowManager) *ModeState {
	return &ModeState{
		peerMode:  make(map[int64]Mode),
		peerWf:    make(map[int64]string),
		workflowM: wfMgr,
	}
}


func (s *ModeState) GetMode(peerID int64) Mode {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.peerMode[peerID]
}


func (s *ModeState) SetMode(peerID int64, mode Mode, workflowID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	oldMode := s.peerMode[peerID]
	oldWf := s.peerWf[peerID]

	
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


func (s *ModeState) GetWorkflowID(peerID int64) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.peerWf[peerID]
}


func (s *ModeState) HasActiveWorkflow(peerID int64) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, exists := s.peerWf[peerID]
	return exists
}


func (s *ModeState) ExitMode(peerID int64) {
	s.mu.Lock()
	delete(s.peerMode, peerID)
	delete(s.peerWf, peerID)
	s.mu.Unlock()
}


type GlobalAgentState struct {
	wfMgr     *workflow.WorkflowManager
	modeState *ModeState
}


func SetGlobalAgentState(wfMgr *workflow.WorkflowManager, ms *ModeState) {
	globalAgentState = &GlobalAgentState{
		wfMgr:    wfMgr,
		modeState: ms,
	}
}


func GetGlobalAgentState() *GlobalAgentState {
	return globalAgentState
}

var globalAgentState *GlobalAgentState


func GetAgentManager() *workflow.WorkflowManager {
	if globalAgentState == nil {
		return nil
	}
	return globalAgentState.wfMgr
}


func GetModeState() *ModeState {
	if globalAgentState == nil {
		return nil
	}
	return globalAgentState.modeState
}
