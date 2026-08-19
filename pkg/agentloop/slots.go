package agentloop

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)


const slotSaveTimeout = 2 * time.Minute


type SlotClient struct {
	httpClient *http.Client
}

func newSlotClient() *SlotClient {
	return &SlotClient{
		httpClient: &http.Client{Timeout: slotSaveTimeout},
	}
}


func (c *SlotClient) saveSlot(ctx context.Context, serverURL string, slotID int, modelName, filename string) error {
	return c.slotAction(ctx, serverURL, slotID, modelName, "save", filename)
}


func (c *SlotClient) restoreSlot(ctx context.Context, serverURL string, slotID int, modelName, filename string) error {
	return c.slotAction(ctx, serverURL, slotID, modelName, "restore", filename)
}


func (c *SlotClient) eraseSlot(ctx context.Context, serverURL string, slotID int, modelName string) error {
	url := fmt.Sprintf("%s/slots/%d?action=erase", serverURL, slotID)
	body, _ := json.Marshal(map[string]string{"model": modelName})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create erase request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("erase slot %d: %w", slotID, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("erase slot %d: status %d: %s", slotID, resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return nil
}


func (c *SlotClient) ClearAllSlots(ctx context.Context, serverURL, modelName string, totalSlots int, log Logger) {
	for slotID := 0; slotID < totalSlots; slotID++ {
		if err := c.eraseSlot(ctx, serverURL, slotID, modelName); err != nil {
			if log != nil {
				log.DebugLogf("[SLOT] startup erase slot %d: %v", slotID, err)
			}
		} else if log != nil {
			log.InfoLogf("[SLOT] startup erase slot %d", slotID)
		}
	}
}


func IsSlotConfigError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	
	if strings.Contains(msg, "status 5") {
		return true
	}
	return strings.Contains(msg, "slot save path") ||
		strings.Contains(msg, "save path") ||
		strings.Contains(msg, "not configured")
}


func IsSlotMissingFileError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "file not found") ||
		strings.Contains(msg, "no such file") ||
		strings.Contains(msg, "not exist") ||
		strings.Contains(msg, "invalid slot save file")
}


func slotsProbeURL(serverURL, modelName string) string {
	return fmt.Sprintf("%s/slots?model=%s", serverURL, url.QueryEscape(modelName))
}


func (c *SlotClient) slotAction(ctx context.Context, serverURL string, slotID int, modelName, action, filename string) error {
	body, _ := json.Marshal(map[string]string{"filename": filename, "model": modelName})
	url := fmt.Sprintf("%s/slots/%d?action=%s", serverURL, slotID, action)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create %s request: %w", action, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%s slot %d: %w", action, slotID, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%s slot %d: status %d: %s", action, slotID, resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return nil
}


func (c *SlotClient) firstSlotID(ctx context.Context, serverURL, modelName string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, slotsProbeURL(serverURL, modelName), nil)
	if err != nil {
		return 0, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("GET /slots: status %d", resp.StatusCode)
	}

	var slots []struct {
		ID int `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&slots); err != nil {
		return 0, err
	}
	if len(slots) == 0 {
		return 0, fmt.Errorf("GET /slots: no slots reported")
	}
	return slots[0].ID, nil
}
