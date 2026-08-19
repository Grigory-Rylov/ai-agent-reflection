package workflow

import (
	"fmt"
	"sync"
	"time"
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


type ModeStrategy struct {
	mu         sync.RWMutex
	peers      map[int64]*PeerMode 
	activeWf   map[int64]string    
}


type PeerMode struct {
	Mode        Mode
	WorkflowID  string
	EnteredAt   time.Time
	LastUpdated time.Time
}


func NewModeStrategy() *ModeStrategy {
	return &ModeStrategy{
		peers:    make(map[int64]*PeerMode),
		activeWf: make(map[int64]string),
	}
}


func (s *ModeStrategy) GetMode(peerID int64) Mode {
	s.mu.RLock()
	defer s.mu.RUnlock()

	pm, exists := s.peers[peerID]
	if !exists {
		return ModeNormal
	}
	return pm.Mode
}


func (s *ModeStrategy) SetMode(peerID int64, mode Mode, workflowID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if current, exists := s.peers[peerID]; exists && current.Mode == mode && mode == ModeAgent {
		
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


func (s *ModeStrategy) GetActiveWorkflow(peerID int64) string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	pm, exists := s.peers[peerID]
	if !exists || pm.Mode != ModeAgent {
		return ""
	}
	return pm.WorkflowID
}


func (s *ModeStrategy) IsActive(peerID int64) bool {
	return s.GetMode(peerID) == ModeAgent
}


func (s *ModeStrategy) ExitMode(peerID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if pm, exists := s.peers[peerID]; exists {
		if pm.Mode == ModeAgent && pm.WorkflowID != "" {
			
			delete(s.activeWf, peerID)
			fmt.Printf("[MODE] Agent mode exited for peer %d, workflow %s\n", peerID, pm.WorkflowID)
		}
		pm.Mode = ModeNormal
		pm.WorkflowID = ""
	}
}
