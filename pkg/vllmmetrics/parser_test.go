package vllmmetrics

import (
	"strings"
	"testing"
)

const sampleBody = `
# HELP vllm:prompt_tokens_total Number of prefill tokens processed.
# TYPE vllm:prompt_tokens_total counter
vllm:prompt_tokens_total{engine="0",model_name="qwen3.8-27b"} 9.8313546e+07
# HELP vllm:prompt_tokens_by_source_total Number of prompt tokens by source.
# TYPE vllm:prompt_tokens_by_source_total counter
vllm:prompt_tokens_by_source_total{engine="0",model_name="qwen3.8-27b",source="local_compute"} 5.371914e+06
vllm:prompt_tokens_by_source_total{engine="0",model_name="qwen3.8-27b",source="local_cache_hit"} 9.2941632e+07
vllm:prompt_tokens_by_source_total{engine="0",model_name="qwen3.8-27b",source="external_kv_transfer"} 0.0
# HELP vllm:generation_tokens_total Generated tokens total.
# TYPE vllm:generation_tokens_total counter
vllm:generation_tokens_total{engine="0",model_name="qwen3.8-27b"} 496231.0
# HELP vllm:request_success_total Successful requests.
# TYPE vllm:request_success_total counter
vllm:request_success_total{engine="0",finished_reason="stop",model_name="qwen3.8-27b"} 796.0
vllm:request_success_total{engine="0",finished_reason="length",model_name="qwen3.8-27b"} 7.0
vllm:request_success_total{engine="0",finished_reason="abort",model_name="qwen3.8-27b"} 0.0
# HELP python_gc_objects_collected_total noise metric
# TYPE python_gc_objects_collected_total counter
python_gc_objects_collected_total{generation="0"} 343928.0
`

func TestParseMetricsFullSample(t *testing.T) {
	stats := ParseMetrics(sampleBody)

	if stats.RequestsTotal != 803 {
		t.Errorf("RequestsTotal = %d, want 803", stats.RequestsTotal)
	}
	if stats.PromptTokensTotal != 98313546 {
		t.Errorf("PromptTokensTotal = %d, want 98313546", stats.PromptTokensTotal)
	}
	if stats.ComputedTokens != 5371914 {
		t.Errorf("ComputedTokens = %d, want 5371914", stats.ComputedTokens)
	}
	if stats.CacheHitTokens != 92941632 {
		t.Errorf("CacheHitTokens = %d, want 92941632", stats.CacheHitTokens)
	}
	if stats.GenerationTokens != 496231 {
		t.Errorf("GenerationTokens = %d, want 496231", stats.GenerationTokens)
	}
}

func TestParseMetricsEmptyBody(t *testing.T) {
	stats := ParseMetrics("")
	if stats.RequestsTotal != 0 || stats.PromptTokensTotal != 0 ||
		stats.ComputedTokens != 0 || stats.CacheHitTokens != 0 || stats.GenerationTokens != 0 {
		t.Errorf("expected zero Stats for empty body, got %+v", stats)
	}
}

func TestParseMetricsIgnoresNonVllmLines(t *testing.T) {
	body := "# comment\nfoo_bar_metric{x=\"1\"} 42.0\n"
	stats := ParseMetrics(body)
	if stats.RequestsTotal != 0 {
		t.Errorf("non-vllm metric leaked into stats: %+v", stats)
	}
}

func TestMetricValueExtraction(t *testing.T) {
	cases := []struct {
		line string
		want float64
		ok   bool
	}{
		{`vllm:generation_tokens_total{engine="0"} 496231.0`, 496231, true},
		{`vllm:prompt_tokens_total{engine="0"} 9.8313546e+07`, 98313546, true},
		{`vllm:x_total 0.0`, 0, true},
		{`# HELP vllm:generation_tokens_total doc`, 0, false},
		{`unrelated_line 5.0`, 0, false},
	}
	for _, c := range cases {
		got, ok := metricValue(c.line, "vllm:")
		if ok != c.ok {
			t.Errorf("%q: ok = %v, want %v", c.line, ok, c.ok)
			continue
		}
		if ok && got != c.want {
			t.Errorf("%q: value = %v, want %v", c.line, got, c.want)
		}
	}
}

func TestSourceLabelMatching(t *testing.T) {
	line := `vllm:prompt_tokens_by_source_total{engine="0",model_name="m",source="local_cache_hit"} 100.0`
	v, ok := metricValue(line, "vllm:prompt_tokens_by_source_total")
	if !ok || v != 100 {
		t.Fatalf("unexpected extraction: %v %v", v, ok)
	}
	if !strings.Contains(line, `source="local_cache_hit"`) {
		t.Fatal("fixture lost label")
	}
}

const sampleWithActiveGeneration = sampleBody + `
# HELP vllm:num_requests_running Number of requests currently running.
# TYPE vllm:num_requests_running gauge
vllm:num_requests_running{engine="0",model_name="qwen3.8-27b"} 3.0
# HELP vllm:gpu_cache_usage_perc Fraction of GPU cache occupied by KV blocks.
# TYPE vllm:gpu_cache_usage_perc gauge
vllm:gpu_cache_usage_perc{engine="0",model_name="qwen3.8-27b"} 0.42
`

func TestParseMetricsActiveGeneration(t *testing.T) {
	stats := ParseMetrics(sampleWithActiveGeneration)

	if stats.NumRequestsRunning != 3 {
		t.Errorf("NumRequestsRunning = %d, want 3", stats.NumRequestsRunning)
	}
	if stats.GPUCacheUsagePercent < 41.9 || stats.GPUCacheUsagePercent > 42.1 {
		t.Errorf("GPUCacheUsagePercent = %v, want ~42", stats.GPUCacheUsagePercent)
	}
}

func TestParseMetricsIdleEngine(t *testing.T) {
	body := `
vllm:num_requests_running{engine="0",model_name="m"} 0.0
vllm:gpu_cache_usage_perc{engine="0",model_name="m"} 0.0
`
	stats := ParseMetrics(body)
	if stats.NumRequestsRunning != 0 || stats.GPUCacheUsagePercent != 0 {
		t.Errorf("expected idle stats, got %+v", stats)
	}
}

func TestParseMetricsSumsRunningAndMaxesCacheAcrossEngines(t *testing.T) {
	body := `
vllm:num_requests_running{engine="0",model_name="m"} 1.0
vllm:num_requests_running{engine="1",model_name="m"} 2.0
vllm:gpu_cache_usage_perc{engine="0",model_name="m"} 0.10
vllm:gpu_cache_usage_perc{engine="1",model_name="m"} 0.90
`
	stats := ParseMetrics(body)
	if stats.NumRequestsRunning != 3 {
		t.Errorf("NumRequestsRunning = %d, want 3", stats.NumRequestsRunning)
	}
	if stats.GPUCacheUsagePercent < 89.9 || stats.GPUCacheUsagePercent > 90.1 {
		t.Errorf("GPUCacheUsagePercent = %v, want ~90", stats.GPUCacheUsagePercent)
	}
}

func TestFormatShowsActiveGeneration(t *testing.T) {
	out := Format(ParseMetrics(sampleWithActiveGeneration))
	if !strings.Contains(out, "активно") {
		t.Errorf("format should mark generation as active, got:\n%s", out)
	}
	if !strings.Contains(out, "3") {
		t.Errorf("format should show running request count, got:\n%s", out)
	}
	if !strings.Contains(out, "42") {
		t.Errorf("format should show GPU cache percent, got:\n%s", out)
	}
}

func TestFormatShowsIdleGeneration(t *testing.T) {
	idle := Stats{RequestsTotal: 5, GenerationTokens: 10}
	out := Format(idle)
	if !strings.Contains(out, "нет активных") {
		t.Errorf("format should mark generation as idle, got:\n%s", out)
	}
}
