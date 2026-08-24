package engine

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Prober struct {
	Client *http.Client
	Probe  ProbeFn
}

func NewProber(client *http.Client) *Prober {
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	return &Prober{Client: client, Probe: ClientProbe}
}

func (p *Prober) Healthy(url string, modelSubstr string) (bool, error) {
	fn := p.Probe
	if fn == nil {
		fn = ClientProbe
	}
	return fn(url, modelSubstr)
}

func ClientProbe(url string, modelSubstr string) (bool, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return false, fmt.Errorf("probing %s: %w", url, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return false, fmt.Errorf("reading probe response from %s: %w", url, err)
	}
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("probe of %s answered with status %d", url, resp.StatusCode)
	}
	if modelSubstr == "" {
		return true, nil
	}
	data, err := decodeServedModels(body)
	if err != nil {
		return false, fmt.Errorf("decoding model list from %s: %w", url, err)
	}
	for _, m := range data {
		if strings.Contains(m.ID, modelSubstr) || strings.Contains(m.Name, modelSubstr) {
			return true, nil
		}
	}
	return false, fmt.Errorf("model %q not advertised by %s", modelSubstr, url)
}

type servedModel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type servedModelDoc struct {
	Data []servedModel `json:"data"`
}

func decodeServedModels(body []byte) ([]servedModel, error) {
	var doc servedModelDoc
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, err
	}
	return doc.Data, nil
}

