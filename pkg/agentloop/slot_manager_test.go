package agentloop

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestSlotManager_GetOrAssign_FreeSlot(t *testing.T) {
	m := NewSlotManager(newSlotClient())

	slotID, evicted := m.GetOrAssign("session-1", 4)

	if slotID < 0 || slotID >= 4 {
		t.Errorf("expected slot 0-3, got %d", slotID)
	}
	if evicted != "" {
		t.Error("should not evict when free slots available")
	}
}

func TestSlotManager_GetOrAssign_ReusesSlot(t *testing.T) {
	m := NewSlotManager(newSlotClient())

	slot1, _ := m.GetOrAssign("session-1", 4)
	slot2, _ := m.GetOrAssign("session-1", 4)

	if slot1 != slot2 {
		t.Errorf("expected same slot, got %d then %d", slot1, slot2)
	}
}

func TestSlotManager_GetOrAssign_AssignsDistinctSlots(t *testing.T) {
	m := NewSlotManager(newSlotClient())

	slots := make(map[int]string)
	for i := 0; i < 4; i++ {
		sessionID := fmt.Sprintf("session-%d", i)
		id, _ := m.GetOrAssign(sessionID, 4)
		if dup, exists := slots[id]; exists {
			t.Errorf("slot %d assigned twice: %s and %s", id, dup, sessionID)
		}
		slots[id] = sessionID
	}

	if len(slots) != 4 {
		t.Errorf("expected 4 distinct slots, got %d", len(slots))
	}
}

func TestSlotManager_GetOrAssign_LRUEviction(t *testing.T) {
	m := NewSlotManager(newSlotClient())

	// Fill all 2 slots
	s0, _ := m.GetOrAssign("session-1", 2)
	s1, _ := m.GetOrAssign("session-2", 2)

	if s0 == s1 {
		t.Fatal("slots should be distinct")
	}

	// Access session-1 to make it more recent
	m.Touch("session-1")
	time.Sleep(1 * time.Millisecond)

	// New session should evict session-2 (LRU)
	_, evicted := m.GetOrAssign("session-3", 2)

	if evicted == "" {
		t.Error("expected eviction when all slots occupied")
	}
	if evicted != "session-2" {
		t.Errorf("expected session-2 evicted, got %s", evicted)
	}

	// session-2 was LRU, its slot should now belong to session-3
	if m.GetSlotID("session-2") != -1 {
		t.Error("session-2 should be evicted")
	}
	if m.GetSlotID("session-3") != s1 {
		t.Errorf("session-3 should get session-2's slot %d, got %d", s1, m.GetSlotID("session-3"))
	}
	if m.GetSlotID("session-1") != s0 {
		t.Error("session-1 should keep its slot")
	}
}

func TestSlotManager_Release(t *testing.T) {
	m := NewSlotManager(newSlotClient())

	slot, _ := m.GetOrAssign("session-1", 4)
	released := m.Release("session-1")

	if released != slot {
		t.Errorf("expected released slot %d, got %d", slot, released)
	}
	if m.GetSlotID("session-1") != -1 {
		t.Error("session should have no slot after release")
	}
}

func TestSlotManager_Release_ReassignsSlot(t *testing.T) {
	m := NewSlotManager(newSlotClient())

	slot1, _ := m.GetOrAssign("session-1", 4)
	m.Release("session-1")

	slot2, _ := m.GetOrAssign("session-2", 4)
	// Should get same slot
	if slot2 != slot1 {
		t.Errorf("expected reused slot %d, got %d", slot1, slot2)
	}
}

func TestSlotManager_Release_NoOpUnknown(t *testing.T) {
	m := NewSlotManager(newSlotClient())
	released := m.Release("nonexistent")
	if released != -1 {
		t.Errorf("expected -1 for unknown session, got %d", released)
	}
}

func TestSlotManager_GetSlotID(t *testing.T) {
	m := NewSlotManager(newSlotClient())

	m.GetOrAssign("session-1", 4)

	id := m.GetSlotID("session-1")
	if id < 0 {
		t.Errorf("expected slot >= 0, got %d", id)
	}

	unknown := m.GetSlotID("unknown")
	if unknown != -1 {
		t.Errorf("expected -1 for unknown session, got %d", unknown)
	}
}

func TestSlotManager_Touch(t *testing.T) {
	m := NewSlotManager(newSlotClient())

	m.GetOrAssign("session-1", 2)
	m.GetOrAssign("session-2", 2)
	time.Sleep(1 * time.Millisecond)
	m.Touch("session-1")

	// session-2 should now be LRU
	_, evicted := m.GetOrAssign("session-3", 2)
	if evicted == "" {
		t.Error("expected eviction")
	}
	// session-2 should be evicted (it's LRU after touch)
	if m.GetSlotID("session-2") != -1 {
		t.Error("session-2 should be evicted as LRU")
	}
	if m.GetSlotID("session-1") == -1 {
		t.Error("session-1 should NOT be evicted (it was touched)")
	}
}

func TestSlotManager_GetAssignedSessions(t *testing.T) {
	m := NewSlotManager(newSlotClient())

	m.GetOrAssign("a", 4)
	m.GetOrAssign("b", 4)

	sessions := m.GetAssignedSessions()
	if len(sessions) != 2 {
		t.Errorf("expected 2 sessions, got %d", len(sessions))
	}
	if _, ok := sessions["a"]; !ok {
		t.Error("session a should be in map")
	}
	if _, ok := sessions["b"]; !ok {
		t.Error("session b should be in map")
	}
}

func TestSlotManager_Availability_Available(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slots" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`[{"id":0},{"id":1},{"id":2},{"id":3}]`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	m := NewSlotManager(newSlotClient())
	ok := m.CheckAvailability(context.Background(), server.URL, "model-x")

	if !ok {
		t.Error("expected availability detected")
	}
	if !m.IsAvailable(server.URL) {
		t.Error("IsAvailable should be true")
	}
}

func TestSlotManager_Availability_Unavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	m := NewSlotManager(newSlotClient())
	ok := m.CheckAvailability(context.Background(), server.URL, "model-x")

	if ok {
		t.Error("expected availability NOT detected")
	}
	if m.IsAvailable(server.URL) {
		t.Error("IsAvailable should be false")
	}
}

func TestSlotManager_Availability_CachesResult(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{"id":0}]`))
	}))
	defer server.Close()

	m := NewSlotManager(newSlotClient())
	m.CheckAvailability(context.Background(), server.URL, "model-x")
	m.CheckAvailability(context.Background(), server.URL, "model-x")
	m.CheckAvailability(context.Background(), server.URL, "model-x")

	// Should only call once (cached)
	if callCount != 1 {
		t.Errorf("expected 1 call (cached), got %d", callCount)
	}
}

func TestSlotManager_ConcurrentAccess(t *testing.T) {
	m := NewSlotManager(newSlotClient())

	var wg sync.WaitGroup
	slots := make(map[int]bool)
	var mu sync.Mutex

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			slot, _ := m.GetOrAssign("session-1", 4)
			mu.Lock()
			slots[slot] = true
			mu.Unlock()
		}(i)
	}
	wg.Wait()

	// All should get the same slot
	if len(slots) != 1 {
		t.Errorf("expected 1 unique slot for same session, got %d", len(slots))
	}
}

func TestSlotFileName(t *testing.T) {
	name := SlotFileName("my-model", 3)
	if name != "my-model_slot3.bin" {
		t.Errorf("expected my-model_slot3.bin, got %s", name)
	}
}

func TestSlotFileName_Sanitizes(t *testing.T) {
	name := SlotFileName("my model.gguf", 0)
	if name == "my model.gguf_slot0.bin" {
		t.Errorf("expected sanitized name (spaces replaced), got %s", name)
	}
	if name != "my_model.gguf_slot0.bin" {
		t.Errorf("expected my_model.gguf_slot0.bin, got %s", name)
	}
}
