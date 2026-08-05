package agentloop

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func newSlotTestServer(t *testing.T) (*httptest.Server, *int32) {
	t.Helper()
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/slots":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`[{"id":0,"is_processing":false},{"id":1,"is_processing":false}]`))
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/slots/0"):
			atomic.AddInt32(&calls, 1)
			if r.URL.Query().Get("action") == "" {
				w.WriteHeader(http.StatusBadRequest)
				w.Write([]byte(`{"error":{"message":"missing action"}}`))
				return
			}
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"id_slot":0,"filename":"` + body["filename"] + `","n_saved":1,"n_written":42}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func TestSlotClientSaveRestore(t *testing.T) {
	srv, calls := newSlotTestServer(t)
	client := newSlotClient()

	if err := client.saveSlot(context.Background(), srv.URL, 0, "session_1_model.bin"); err != nil {
		t.Fatalf("saveSlot: %v", err)
	}
	if atomic.LoadInt32(calls) != 1 {
		t.Errorf("expected 1 save call, got %d", calls)
	}

	if err := client.restoreSlot(context.Background(), srv.URL, 0, "session_1_model.bin"); err != nil {
		t.Fatalf("restoreSlot: %v", err)
	}
	if atomic.LoadInt32(calls) != 2 {
		t.Errorf("expected 2 calls total, got %d", calls)
	}
}

func TestSlotClientSaveError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":{"message":"slot save path not configured"}}`))
	}))
	defer srv.Close()

	err := newSlotClient().saveSlot(context.Background(), srv.URL, 0, "x.bin")
	if err == nil {
		t.Fatal("expected error for failed save")
	}
	if !strings.Contains(err.Error(), "status 500") {
		t.Errorf("error should contain status, got: %v", err)
	}
}

func TestSlotClientRestoreMissingFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":{"message":"file not found"}}`))
	}))
	defer srv.Close()

	err := newSlotClient().restoreSlot(context.Background(), srv.URL, 0, "missing.bin")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestSlotClientFirstSlotID(t *testing.T) {
	srv, _ := newSlotTestServer(t)
	id, err := newSlotClient().firstSlotID(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("firstSlotID: %v", err)
	}
	if id != 0 {
		t.Errorf("got slot id %d, want 0", id)
	}
}

func TestSlotClientFirstSlotIDError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	if _, err := newSlotClient().firstSlotID(context.Background(), srv.URL); err == nil {
		t.Fatal("expected error for forbidden /slots")
	}
}
