package vllmmetrics

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type Stats struct {
	RequestsTotal     int
	PromptTokensTotal int
	ComputedTokens    int
	CacheHitTokens    int
	ExternalKVTokens  int
	GenerationTokens  int
}

func metricValue(line, namePrefix string) (float64, bool) {
	if strings.HasPrefix(line, "#") {
		return 0, false
	}

	var rest string
	bracesOpen := strings.IndexByte(line, '{')
	if bracesOpen >= 0 {
		bracesClose := strings.LastIndexByte(line, '}')
		if bracesClose <= bracesOpen {
			return 0, false
		}
		name := line[:bracesOpen]
		rest = line[bracesClose+1:]
		if !strings.HasPrefix(name, namePrefix) {
			return 0, false
		}
	} else {
		spaceIdx := strings.IndexByte(line, ' ')
		if spaceIdx <= 0 {
			return 0, false
		}
		name := line[:spaceIdx]
		rest = line[spaceIdx:]
		if !strings.HasPrefix(name, namePrefix) {
			return 0, false
		}
	}

	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return 0, false
	}
	value, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0, false
	}
	return value, true
}

func sumCounter(lines []string, namePrefix string) int {
	total := 0
	for _, line := range lines {
		if value, ok := metricValue(line, namePrefix); ok {
			total += int(value)
		}
	}
	return total
}

func sumCounterBySource(lines []string, namePrefix, sourceLabel string) int {
	total := 0
	labelFragment := fmt.Sprintf(`source="%s"`, sourceLabel)
	for _, line := range lines {
		if !strings.Contains(line, labelFragment) {
			continue
		}
		if value, ok := metricValue(line, namePrefix); ok {
			total += int(value)
		}
	}
	return total
}

func ParseMetrics(body string) Stats {
	lines := strings.Split(body, "\n")

	const sourcePrefix = "vllm:prompt_tokens_by_source_total"
	return Stats{
		RequestsTotal:     sumCounter(lines, "vllm:request_success_total"),
		PromptTokensTotal: sumCounter(lines, "vllm:prompt_tokens_total"),
		ComputedTokens:    sumCounterBySource(lines, sourcePrefix, "local_compute"),
		CacheHitTokens:    sumCounterBySource(lines, sourcePrefix, "local_cache_hit"),
		ExternalKVTokens:  sumCounterBySource(lines, sourcePrefix, "external_kv_transfer"),
		GenerationTokens:  sumCounter(lines, "vllm:generation_tokens_total"),
	}
}

func Fetch(engineBaseURL string) (Stats, error) {
	metricsURL := strings.TrimRight(engineBaseURL, "/") + "/metrics"
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, metricsURL, nil)
	if err != nil {
		return Stats{}, fmt.Errorf("creating metrics request: %w", err)
	}

	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return Stats{}, fmt.Errorf("fetching metrics: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return Stats{}, fmt.Errorf("metrics endpoint returned status %d", response.StatusCode)
	}

	body, err := io.ReadAll(response.Body)
	if err != nil {
		return Stats{}, fmt.Errorf("reading metrics body: %w", err)
	}

	return ParseMetrics(string(body)), nil
}

func formatCompact(count int) string {
	switch {
	case count >= 1_000_000:
		return fmt.Sprintf("%.1f млн", float64(count)/1_000_000)
	case count >= 1_000:
		return fmt.Sprintf("%.1f тыс.", float64(count)/1_000)
	default:
		return strconv.Itoa(count)
	}
}

func Format(stats Stats) string {
	status := "Статистика vLLM:" +
		"\nЗапросов всего: " + strconv.Itoa(stats.RequestsTotal) +
		"\nInput токенов: " + formatCompact(stats.PromptTokensTotal) +
		"\n├─ вычислено: " + formatCompact(stats.ComputedTokens) +
		"\n└─ из KV-кэша: " + formatCompact(stats.CacheHitTokens) +
		"\nOutput токенов: " + formatCompact(stats.GenerationTokens)
	return status
}
