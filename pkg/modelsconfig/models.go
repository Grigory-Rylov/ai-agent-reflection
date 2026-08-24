package modelsconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
)

type ModelEntry struct {
	Name        string `json:"name"`
	Host        string `json:"host"`
	Context     int    `json:"context,omitempty"`
	Vision      bool   `json:"vision,omitempty"`
	SlotSave    bool   `json:"slot-save,omitempty"`
	Type        string `json:"type,omitempty"`
	StartScript string `json:"start-script,omitempty"`
	StopScript  string `json:"stop-script,omitempty"`
}

type ModelsConfig struct {
	Default string                `json:"default"`
	Models  map[string]ModelEntry `json:"models"`
}

func (mc *ModelsConfig) validate() error {
	if mc.Default == "" {
		return fmt.Errorf("models.json: 'default' field is required")
	}
	if mc.Models == nil || len(mc.Models) == 0 {
		return fmt.Errorf("models.json: 'models' field is required with at least one entry")
	}
	if _, ok := mc.Models[mc.Default]; !ok {
		return fmt.Errorf("models.json: default model %q not found in models list", mc.Default)
	}
	return nil
}

var errFormatHint = `models.json not found.

Create models.json in the agent directory with this format:
{
    "default": "my-model",
    "models": {
        "my-model": {
            "name": "model-name-on-server.gguf",
            "host": "127.0.0.1:8081"
        }
    }
}

See models.example.json for a complete example.`

func Load(path string) (*ModelsConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, errors.New(errFormatHint)
		}
		return nil, fmt.Errorf("reading models.json: %w", err)
	}

	var cfg ModelsConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing models.json: %w", err)
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func ensureHTTPScheme(host string) string {
	if host == "" {
		return ""
	}
	if !strings.HasPrefix(host, "http://") && !strings.HasPrefix(host, "https://") {
		return "http://" + host
	}
	return host
}

type Holder struct {
	mu       sync.RWMutex
	config   *ModelsConfig
	filePath string
}

func NewHolder(path string) (*Holder, error) {
	cfg, err := Load(path)
	if err != nil {
		return nil, err
	}
	return &Holder{
		config:   cfg,
		filePath: path,
	}, nil
}

func (h *Holder) GetCurrent() (alias, modelName, host string) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	entry := h.config.Models[h.config.Default]
	return h.config.Default, entry.Name, ensureHTTPScheme(entry.Host)
}

func (h *Holder) Switch(alias string) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if _, ok := h.config.Models[alias]; !ok {
		return fmt.Errorf("unknown model alias %q. Available: %s", alias, h.listAliasesLocked())
	}

	h.config.Default = alias

	data, err := json.MarshalIndent(h.config, "", "    ")
	if err != nil {
		return fmt.Errorf("marshaling models config: %w", err)
	}
	if err := os.WriteFile(h.filePath, data, 0644); err != nil {
		return fmt.Errorf("saving models.json: %w", err)
	}

	return nil
}

func (h *Holder) List() map[string]ModelEntry {
	h.mu.RLock()
	defer h.mu.RUnlock()
	result := make(map[string]ModelEntry, len(h.config.Models))
	for k, v := range h.config.Models {
		result[k] = v
	}
	return result
}

func (h *Holder) GetDefaultAlias() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.config.Default
}


func (h *Holder) GetCurrentContext() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.config.Models[h.config.Default].Context
}


func (h *Holder) GetModelContext(alias string) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.config.Models[alias].Context
}


func (h *Holder) GetCurrentVision() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.config.Models[h.config.Default].Vision
}


func (h *Holder) GetModelVision(alias string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.config.Models[alias].Vision
}


func (h *Holder) GetCurrentSlotSave() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.config.Models[h.config.Default].SlotSave
}


func (h *Holder) GetModelSlotSave(alias string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.config.Models[alias].SlotSave
}

func (h *Holder) GetModelHost(alias string) (ModelEntry, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	entry, ok := h.config.Models[alias]
	return entry, ok
}

func (h *Holder) GetCurrentEngineType() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.config.Models[h.config.Default].Type
}

func (h *Holder) GetModelEngineType(alias string) string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.config.Models[alias].Type
}

func (h *Holder) GetCurrentStartScript() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.config.Models[h.config.Default].StartScript
}

func (h *Holder) GetModelStartScript(alias string) string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.config.Models[alias].StartScript
}

func (h *Holder) GetCurrentStopScript() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.config.Models[h.config.Default].StopScript
}

func (h *Holder) GetModelStopScript(alias string) string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.config.Models[alias].StopScript
}

func (h *Holder) listAliasesLocked() string {
	aliases := make([]string, 0, len(h.config.Models))
	for alias := range h.config.Models {
		aliases = append(aliases, alias)
	}
	return strings.Join(aliases, ", ")
}
