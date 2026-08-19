package agentloop

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)


type slotEntry struct {
	SessionID string
	LastUsed  time.Time
}


type hostAvailability struct {
	known         bool
	avail         bool
	
	unavailLogged bool
}


type SlotManager struct {
	mu                sync.Mutex
	totalSlots        int                       
	slots             map[int]*slotEntry        
	availability      map[string]*hostAvailability 
	probeLogged       map[string]bool           
	slotClient        *SlotClient
	log               Logger
}

func NewSlotManager(client *SlotClient) *SlotManager {
	return &SlotManager{
		slots:        make(map[int]*slotEntry),
		availability: make(map[string]*hostAvailability),
		probeLogged:  make(map[string]bool),
		slotClient:   client,
	}
}


func (m *SlotManager) SetLogger(l Logger) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.log = l
}


func (m *SlotManager) CheckAvailability(ctx context.Context, serverURL, modelName string) bool {
	m.mu.Lock()
	if ha, ok := m.availability[serverURL]; ok && ha.known {
		avail := ha.avail
		m.mu.Unlock()
		return avail
	}
	m.mu.Unlock()

	
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, slotsProbeURL(serverURL, modelName), nil)
	if err != nil {
		m.setUnavailableLocked(serverURL)
		m.logProbeFailure(serverURL, err)
		return false
	}

	resp, err := m.slotClient.httpClient.Do(req)
	if err != nil {
		m.setUnavailableLocked(serverURL)
		m.logProbeFailure(serverURL, err)
		return false
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		m.setUnavailableLocked(serverURL)
		m.logProbeFailure(serverURL, fmt.Errorf("GET /slots: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body))))
		return false
	}

	var slotList []struct {
		ID int `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&slotList); err != nil {
		m.setUnavailableLocked(serverURL)
		m.logProbeFailure(serverURL, err)
		return false
	}

	m.mu.Lock()
	if ha, ok := m.availability[serverURL]; !ok || !ha.known {
		avail := len(slotList) >= 1
		m.totalSlots = len(slotList)
		m.availability[serverURL] = &hostAvailability{known: true, avail: avail}
	}
	m.mu.Unlock()
	return len(slotList) >= 1
}


func (m *SlotManager) logProbeFailure(serverURL string, err error) {
	m.mu.Lock()
	shouldLog := m.log != nil && !m.probeLogged[serverURL]
	m.probeLogged[serverURL] = true
	m.mu.Unlock()
	if shouldLog {
		m.log.WarnLogf("[SLOT] slot feature probe failed on %s, disabling slot save/restore: %v", serverURL, err)
	}
}

func (m *SlotManager) setUnavailableLocked(serverURL string) {
	m.availability[serverURL] = &hostAvailability{known: true, avail: false}
}


func (m *SlotManager) MarkUnavailable(serverURL string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	ha, ok := m.availability[serverURL]
	if !ok {
		ha = &hostAvailability{}
		m.availability[serverURL] = ha
	}
	alreadyDown := ha.known && !ha.avail
	ha.known = true
	ha.avail = false
	return !alreadyDown
}


func (m *SlotManager) TotalSlots() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.totalSlots
}


func (m *SlotManager) ShouldLogUnavailable(serverURL string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	ha, ok := m.availability[serverURL]
	if !ok {
		ha = &hostAvailability{}
		m.availability[serverURL] = ha
	}
	if ha.unavailLogged {
		return false
	}
	ha.unavailLogged = true
	return true
}


func (m *SlotManager) IsAvailable(serverURL string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ha, ok := m.availability[serverURL]; ok && ha.known {
		return ha.avail
	}
	return false
}


func (m *SlotManager) GetOrAssign(sessionID string, totalSlots int) (int, string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if totalSlots <= 0 {
		return -1, ""
	}

	
	for slotID, entry := range m.slots {
		if entry.SessionID == sessionID {
			entry.LastUsed = time.Now()
			return slotID, ""
		}
	}

	
	for slotID := 0; slotID < totalSlots; slotID++ {
		if m.slots[slotID] == nil {
			m.slots[slotID] = &slotEntry{
				SessionID: sessionID,
				LastUsed:  time.Now(),
			}
			return slotID, ""
		}
	}

	
	evictSlot := m.findLRUSlotLocked(totalSlots)
	evictedSessionID := evictSlot.Entry.SessionID
	evictSlot.Entry.SessionID = sessionID
	evictSlot.Entry.LastUsed = time.Now()
	return evictSlot.ID, evictedSessionID
}

type slotWithID struct {
	ID     int
	Entry  *slotEntry
}

func (m *SlotManager) findLRUSlotLocked(totalSlots int) slotWithID {
	var oldest slotWithID
	for slotID, entry := range m.slots {
		if slotID >= totalSlots {
			continue
		}
		if oldest.Entry == nil || entry.LastUsed.Before(oldest.Entry.LastUsed) {
			oldest = slotWithID{ID: slotID, Entry: entry}
		}
	}
	return oldest
}


func (m *SlotManager) Release(sessionID string) int {
	m.mu.Lock()
	defer m.mu.Unlock()

	for slotID, entry := range m.slots {
		if entry.SessionID == sessionID {
			delete(m.slots, slotID)
			return slotID
		}
	}
	return -1
}


func (m *SlotManager) GetSlotID(sessionID string) int {
	m.mu.Lock()
	defer m.mu.Unlock()

	for slotID, entry := range m.slots {
		if entry.SessionID == sessionID {
			entry.LastUsed = time.Now()
			return slotID
		}
	}
	return -1
}


func (m *SlotManager) Touch(sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, entry := range m.slots {
		if entry.SessionID == sessionID {
			entry.LastUsed = time.Now()
			return
		}
	}
}


func (m *SlotManager) GetAssignedSessions() map[string]int {
	m.mu.Lock()
	defer m.mu.Unlock()

	result := make(map[string]int)
	for slotID, entry := range m.slots {
		result[entry.SessionID] = slotID
	}
	return result
}


func SlotFileName(modelName string, slotID int) string {
	safe := sanitizeSlotName(modelName)
	return fmt.Sprintf("%s_slot%d.bin", safe, slotID)
}
