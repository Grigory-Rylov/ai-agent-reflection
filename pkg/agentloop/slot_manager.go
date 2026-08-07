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

// slotEntry tracks a single slot assignment for LRU eviction.
type slotEntry struct {
	SessionID string
	LastUsed  time.Time
}

// hostAvailability tracks per-host slot availability detection.
type hostAvailability struct {
	known         bool
	avail         bool
	// unavailLogged suppresses repeated "feature unavailable" info logs.
	unavailLogged bool
}

// SlotManager manages llama-server KV-cache slots across sessions.
// Each session gets its own slot, preventing context pollution in multi-agent setups.
// When all slots are occupied, LRU eviction saves and reassigns the least recently used.
//
// File naming: {model}_slot{N}.bin — keyed by slot number, not session.
// The session→slot binding lives exclusively in SlotManager.
type SlotManager struct {
	mu                sync.Mutex
	totalSlots        int                       // from server GET /slots
	slots             map[int]*slotEntry        // slotID → assignment
	availability      map[string]*hostAvailability // serverURL → availability
	probeLogged       map[string]bool           // serverURL → probe failure already logged
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

// SetLogger назначает логгер для сообщений о доступности слот-фичи.
func (m *SlotManager) SetLogger(l Logger) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.log = l
}

// CheckAvailability probes the server for slot support.
// Returns true if GET /slots succeeds and returns ≥1 slot.
// Caches result per host for process lifetime. modelName передаётся
// query-параметром: роутер llama-server требует имя модели, обычный сервер
// игнорирует его. Провал пробы логируется один раз на хост.
func (m *SlotManager) CheckAvailability(ctx context.Context, serverURL, modelName string) bool {
	m.mu.Lock()
	if ha, ok := m.availability[serverURL]; ok && ha.known {
		avail := ha.avail
		m.mu.Unlock()
		return avail
	}
	m.mu.Unlock()

	// Probe server
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

// logProbeFailure логирует причину, по которой слот-фича стала недоступной
// (один раз на хост), если настроен логгер.
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

// MarkUnavailable принудительно помечает хост как недоступный.
// Вызывается, когда save/restore возвращает ошибку конфигурации
// (например, llama-server запущен без --slot-save-path): после этого
// последующие запросы пропускают save/restore, не падая.
// Возвращает true, если это первый переход в недоступное состояние
// (чтобы вызывающий код мог залогировать один раз на уровне info).
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

// TotalSlots возвращает число слотов, обнаруженное последней успешной
// проверкой доступности (GET /slots). Потокобезопасно.
func (m *SlotManager) TotalSlots() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.totalSlots
}

// ShouldLogUnavailable помечает, что сообщение о недоступности для хоста
// уже залогировано, и возвращает true, если это был первый раз.
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

// IsAvailable returns true if slot feature is available on the given host.
func (m *SlotManager) IsAvailable(serverURL string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ha, ok := m.availability[serverURL]; ok && ha.known {
		return ha.avail
	}
	return false
}

// GetOrAssign allocates a slot for sessionID.
// Returns slotID, evicted session ID (empty if no eviction).
// Policy: if all slots occupied, evict LRU — caller must save its cache before using the slot.
func (m *SlotManager) GetOrAssign(sessionID string, totalSlots int) (int, string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if totalSlots <= 0 {
		return -1, ""
	}

	// Check if session already has a slot
	for slotID, entry := range m.slots {
		if entry.SessionID == sessionID {
			entry.LastUsed = time.Now()
			return slotID, ""
		}
	}

	// Find free slot
	for slotID := 0; slotID < totalSlots; slotID++ {
		if m.slots[slotID] == nil {
			m.slots[slotID] = &slotEntry{
				SessionID: sessionID,
				LastUsed:  time.Now(),
			}
			return slotID, ""
		}
	}

	// All occupied — evict LRU (return evicted session ID, don't reassign yet)
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

// Release removes the slot assignment for sessionID.
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

// GetSlotID returns the slot assigned to sessionID, or -1 if none.
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

// Touch updates the last-used time for sessionID's slot.
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

// GetAssignedSessions returns all session IDs that have slots assigned.
func (m *SlotManager) GetAssignedSessions() map[string]int {
	m.mu.Lock()
	defer m.mu.Unlock()

	result := make(map[string]int)
	for slotID, entry := range m.slots {
		result[entry.SessionID] = slotID
	}
	return result
}

// SlotFileName generates the slot file name: {model}_slot{N}.bin.
func SlotFileName(modelName string, slotID int) string {
	safe := sanitizeSlotName(modelName)
	return fmt.Sprintf("%s_slot%d.bin", safe, slotID)
}
