package vk

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/logger"
)

func mustLogger(t *testing.T) *logger.Logger {
	t.Helper()
	log, err := logger.New(logger.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	return log
}

func TestVKAttachmentUnmarshalJSON(t *testing.T) {
	tests := []struct {
		name       string
		json       string
		wantType   string
		wantKeys   []string
		wantPhoto  map[string]interface{}
		wantDoc    map[string]interface{}
		wantErr    bool
	}{
		{
			name: "photo attachment",
			json: `{
				"type": "photo",
				"photo": {
					"id": 123456,
					"owner_id": 1,
					"width": 800,
					"height": 600,
					"sizes": [{"type": "w", "url": "http://example.com/photo.jpg"}]
				}
			}`,
			wantType: "photo",
			wantKeys: []string{"photo"},
			wantPhoto: map[string]interface{}{
				"id":       float64(123456),
				"owner_id": float64(1),
				"width":    float64(800),
				"height":   float64(600),
			},
		},
		{
			name: "doc attachment",
			json: `{
				"type": "doc",
				"doc": {
					"id": 789,
					"title": "report.pdf",
					"size": 1024,
					"url": "http://example.com/report.pdf"
				}
			}`,
			wantType: "doc",
			wantKeys: []string{"doc"},
			wantDoc: map[string]interface{}{
				"id":    float64(789),
				"title": "report.pdf",
				"size":  float64(1024),
				"url":   "http://example.com/report.pdf",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var att VKAttachment
			err := json.Unmarshal([]byte(tt.json), &att)
			if (err != nil) != tt.wantErr {
				t.Errorf("UnmarshalJSON error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if att.Type != tt.wantType {
				t.Errorf("Type = %q, want %q", att.Type, tt.wantType)
			}
			for _, key := range tt.wantKeys {
				if _, ok := att.Raw[key]; !ok {
					t.Errorf("Raw missing key %q", key)
				}
			}
			if _, ok := att.Raw["type"]; ok {
				t.Error("Raw should not contain 'type' key")
			}
			if tt.wantPhoto != nil {
				checkMap(t, "photo", att.Raw["photo"], tt.wantPhoto)
			}
			if tt.wantDoc != nil {
				checkMap(t, "doc", att.Raw["doc"], tt.wantDoc)
			}
		})
	}
}

func checkMap(t *testing.T, key string, got interface{}, want map[string]interface{}) {
	t.Helper()
	gotMap, ok := got.(map[string]interface{})
	if !ok {
		t.Fatalf("%s is not a map", key)
	}
	for k, v := range want {
		if gotMap[k] != v {
			t.Errorf("%s[%s] = %v, want %v", key, k, gotMap[k], v)
		}
	}
}

func TestVKAttachmentToRaw(t *testing.T) {
	tests := []struct {
		name   string
		attach VKAttachment
	}{
		{
			name: "photo roundtrip",
			attach: VKAttachment{
				Type: "photo",
				Raw: map[string]interface{}{
					"photo": map[string]interface{}{
						"id":     float64(123),
						"width":  float64(800),
						"height": float64(600),
					},
				},
			},
		},
		{
			name: "doc roundtrip",
			attach: VKAttachment{
				Type: "doc",
				Raw: map[string]interface{}{
					"doc": map[string]interface{}{
						"id":    float64(456),
						"title": "file.pdf",
						"url":   "http://example.com/file.pdf",
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := tt.attach.ToRaw()

			if raw["type"] != tt.attach.Type {
				t.Errorf("raw type = %q, want %q", raw["type"], tt.attach.Type)
			}
			for k := range tt.attach.Raw {
				if _, ok := raw[k]; !ok {
					t.Errorf("raw missing key %q", k)
				}
			}

			
			data, err := json.Marshal(raw)
			if err != nil {
				t.Fatalf("marshal error: %v", err)
			}
			var result VKAttachment
			if err := json.Unmarshal(data, &result); err != nil {
				t.Fatalf("unmarshal error: %v", err)
			}
			if result.Type != tt.attach.Type {
				t.Errorf("roundtrip Type = %q, want %q", result.Type, tt.attach.Type)
			}
			if len(result.Raw) != len(tt.attach.Raw) {
				t.Errorf("roundtrip Raw len = %d, want %d", len(result.Raw), len(tt.attach.Raw))
			}
		})
	}
}

func TestDownloadAttachmentsAbsolutePath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("file-content"))
	}))
	defer srv.Close()

	attachments := []map[string]interface{}{
		{
			"type": "doc",
			"doc": map[string]interface{}{
				"id":    float64(1),
				"title": "report.txt",
				"url":   srv.URL + "/report.txt",
			},
		},
	}

	workDir := t.TempDir()
	origWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(workDir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origWD)

	downloaded, err := DownloadAttachments(attachments, "./attachments", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(downloaded) != 1 {
		t.Fatalf("expected 1 downloaded file, got %d", len(downloaded))
	}

	path := downloaded[0].Path
	if !filepath.IsAbs(path) {
		t.Errorf("expected absolute path, got %q", path)
	}
	if filepath.Base(filepath.Dir(path)) != "attachments" {
		t.Errorf("expected file under 'attachments' dir, got %q", path)
	}

	info := FormatAttachmentInfo(downloaded)
	if !strings.Contains(info, path) {
		t.Errorf("attachment info should contain full path %q, got: %s", path, info)
	}
}


func TestBuildFullTextLongPollFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("photo-bytes"))
	}))
	defer srv.Close()

	var att VKAttachment
	mustUnmarshal := func(t *testing.T, s string) {
		t.Helper()
		if err := json.Unmarshal([]byte(s), &att); err != nil {
			t.Fatal(err)
		}
	}
	mustUnmarshal(t, `{
		"type": "photo",
		"photo": {
			"id": 555,
			"sizes": [{"type": "w", "url": "` + srv.URL + `/photo.jpg"}]
		}
	}`)

	dir := t.TempDir()
	h := NewBotHandler(nil, newMockAgentLoop(), mustLogger(t))
	h.attachmentsDir = dir

	
	
	msg := &VKMessage{ID: 0, PeerID: 2000000001, Text: "что на фото", Attachments: []VKAttachment{att}}
	out := h.buildFullText(msg, nil, nil)

	if !strings.Contains(out, "что на фото") {
		t.Errorf("prompt should keep original text, got: %s", out)
	}
	if !strings.Contains(out, "saved to:") {
		t.Fatalf("prompt should contain downloaded file path, got: %s", out)
	}
	
	if !strings.Contains(out, dir) {
		t.Errorf("prompt should contain path under %s, got: %s", dir, out)
	}
}


func TestBuildFullTextNoAttachmentsWithoutFullMsg(t *testing.T) {
	h := NewBotHandler(nil, newMockAgentLoop(), mustLogger(t))
	h.attachmentsDir = t.TempDir()

	msg := &VKMessage{ID: 0, PeerID: 2000000001, Text: "просто текст"}
	if got := h.buildFullText(msg, nil, nil); got != "просто текст" {
		t.Errorf("expected plain text, got %q", got)
	}
}

func TestExtractVideoURL(t *testing.T) {
	tests := []struct {
		name       string
		attData    map[string]interface{}
		wantOK     bool
		wantExt    string
		wantPrefix string
	}{
		{
			name: "mp4_854 preferred",
			attData: map[string]interface{}{
				"id":    float64(100),
				"title": "my_video",
				"files": map[string]interface{}{
					"mp4_854": map[string]interface{}{"url": "http://example.com/854.mp4"},
					"mp4_360": map[string]interface{}{"url": "http://example.com/360.mp4"},
				},
			},
			wantOK:     true,
			wantExt:    ".mp4",
			wantPrefix: "http://example.com/854.mp4",
		},
		{
			name: "mp4_640 fallback",
			attData: map[string]interface{}{
				"id": float64(200),
				"files": map[string]interface{}{
					"mp4_640": map[string]interface{}{"url": "http://example.com/640.mp4"},
				},
			},
			wantOK:     true,
			wantExt:    ".mp4",
			wantPrefix: "http://example.com/640.mp4",
		},
		{
			name: "playUrl fallback when no files",
			attData: map[string]interface{}{
				"id":        float64(300),
				"playUrl":   "http://example.com/play.mp4",
				"title":     "fallback",
			},
			wantOK:     true,
			wantExt:    ".mp4",
			wantPrefix: "http://example.com/play.mp4",
		},
		{
			name: "no url at all",
			attData: map[string]interface{}{
				"id": float64(400),
			},
			wantOK: false,
		},
		{
			name: "empty files map",
			attData: map[string]interface{}{
				"id":    float64(500),
				"files": map[string]interface{}{},
			},
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url, filename, ok := extractVideoURL(tt.attData, nil)
			if ok != tt.wantOK {
				t.Errorf("ok = %v, want %v", ok, tt.wantOK)
			}
			if tt.wantOK && !strings.HasPrefix(url, tt.wantPrefix) {
				t.Errorf("url = %q, want prefix %q", url, tt.wantPrefix)
			}
			if tt.wantOK && !strings.HasSuffix(filename, tt.wantExt) {
				t.Errorf("filename = %q, want suffix %q", filename, tt.wantExt)
			}
		})
	}
}

func TestDownloadAttachmentsPartialOnFailure(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Write([]byte("ok-content"))
	}))
	defer srv.Close()

	attachments := []map[string]interface{}{
		{
			"type": "doc",
			"doc": map[string]interface{}{
				"id":    float64(1),
				"title": "bad.txt",
				"url":   srv.URL + "/bad.txt",
			},
		},
		{
			"type": "doc",
			"doc": map[string]interface{}{
				"id":    float64(2),
				"title": "good.txt",
				"url":   srv.URL + "/good.txt",
			},
		},
	}

	downloaded, err := DownloadAttachments(attachments, t.TempDir(), nil)
	if err == nil {
		t.Fatal("expected error for failed download, got nil")
	}
	if len(downloaded) != 1 {
		t.Fatalf("expected 1 downloaded file (partial), got %d", len(downloaded))
	}
	if !strings.Contains(downloaded[0].Path, "good") {
		t.Errorf("expected the good file to be downloaded, got %q", downloaded[0].Path)
	}
	if _, err := os.Stat(downloaded[0].Path); err != nil {
		t.Errorf("downloaded file should exist: %v", err)
	}

	info := FormatAttachmentInfo(downloaded)
	if !strings.Contains(info, downloaded[0].Path) {
		t.Errorf("partial attachment info should contain path %q, got: %s", downloaded[0].Path, info)
	}
}


func newVideosGetStub(t *testing.T, body string) (*BotClient, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, body)
	}))
	client := NewBotClient("test_token")
	client.baseURL = srv.URL + "/method/"
	return client, srv
}


func TestExtractVideoURLOwnerOnlyFallsBackToAPI(t *testing.T) {
	body := `{"response":[
		{"id":400,"owner_id":-100,"files":{
			"mp4_360":{"url":"http://stub.example/m360.mp4","width":640,"height":360},
			"mp4_854":{"url":"http://stub.example/m854.mp4","width":1280,"height":854}
		}}
	]}`
	client, srv := newVideosGetStub(t, body)
	defer srv.Close()

	attData := map[string]interface{}{
		"id":       float64(400),
		"owner_id": float64(-100),
		"title":    "owner only clip",
	}

	url, filename, ok := extractVideoURL(attData, client)
	if !ok {
		t.Fatal("expected ok=true")
	}
	want := "http://stub.example/m854.mp4"
	if url != want {
		t.Errorf("url = %q, want %q", url, want)
	}
	if filename != "owner_only_clip.mp4" {
		t.Errorf("filename = %q, want %q", filename, "owner_only_clip.mp4")
	}
}


func TestExtractVideoURLNilClientGracefulFalse(t *testing.T) {
	attData := map[string]interface{}{
		"id":       float64(400),
		"owner_id": float64(-100),
		"title":    "no url here",
	}
	_, _, ok := extractVideoURL(attData, nil)
	if ok {
		t.Error("expected ok=false when no inline url and no client")
	}
}


func TestGetBestVideoURLShapes(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		want    string
		wantErr bool
	}{
		{
			name: "prefers highest known resolution among maps",
			body: `{"response":[{"id":1,"owner_id":-100,"files":{
				"mp4_360":{"url":"http://x/a360.mp4"},
				"mp4_854":{"url":"http://x/a854.mp4"},
				"mp4_480":{"url":"http://x/a480.mp4"}
			}}]}`,
			want: "http://x/a854.mp4",
		},
		{
			name: "unknown mp4 beats generic url",
			body: `{"response":[{"id":2,"owner_id":-100,"files":{
				"url":"http://x/generic.mp4",
				"mp4_720":{"url":"http://x/a720.mp4"}
			}}]}`,
			want: "http://x/a720.mp4",
		},
		{
			name: "bare string values handled",
			body: `{"response":[{"id":3,"owner_id":-100,"files":{
				"mp4_360":"http://x/s360.mp4",
				"mp4_854":"http://x/s854.mp4"
			}}]}`,
			want: "http://x/s854.mp4",
		},
		{
			name: "mixed map and string picks best",
			body: `{"response":[{"id":4,"owner_id":-100,"files":{
				"mp4_360":{"url":"http://x/m360.mp4"},
				"mp4_854":"http://x/s854.mp4"
			}}]}`,
			want: "http://x/s854.mp4",
		},
		{
			name: "missing files yields empty",
			body: `{"response":[{"id":5,"owner_id":-100,"title":"nofiles"}]}`,
			want: "",
		},
		{
			name: "empty response array",
			body: `{"response":[]}`,
			want: "",
		},
		{
			name: "files without usable urls",
			body: `{"response":[{"id":6,"owner_id":-100,"files":{"flv":{"ext":"flv"}}}]}`,
			want: "",
		},
		{
			name: "api error propagates",
			body: `{"error":{"error_code":15,"error_message":"Access denied"}}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, srv := newVideosGetStub(t, tt.body)
			defer srv.Close()
			got, err := client.GetBestVideoURL(-100, 1)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("url = %q, want %q", got, tt.want)
			}
		})
	}
}

type seqFetcher struct {
	mu     sync.Mutex
	resps  [][]VKMessage
	errs   []error
	called int
}

func (f *seqFetcher) GetMessagesByID(ids []int64) ([]VKMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	idx := f.called
	f.called++
	if idx < len(f.errs) && f.errs[idx] != nil {
		return nil, f.errs[idx]
	}
	if idx < len(f.resps) {
		return f.resps[idx], nil
	}
	return nil, nil
}

func videoAtt(ownerID, vid float64, files map[string]interface{}, playURL string) VKAttachment {
	data := map[string]interface{}{
		"id":       vid,
		"owner_id": ownerID,
	}
	if files != nil {
		data["files"] = files
	}
	if playURL != "" {
		data["playUrl"] = playURL
	}
	return VKAttachment{Type: "video", Raw: map[string]interface{}{"video": data}}
}

func setupFastEnrich(t *testing.T) {
	t.Helper()
	prevAttempts := enrichAttemptsN.Load()
	prevSleep := enrichSleepMs.Load()
	enrichAttemptsN.Store(5)
	enrichSleepMs.Store(1)
	t.Cleanup(func() {
		enrichAttemptsN.Store(prevAttempts)
		enrichSleepMs.Store(prevSleep)
	})
}

func restoreVideoExtHost(t *testing.T) {
	t.Helper()
	prev := videoExtHostOverride
	t.Cleanup(func() { videoExtHostOverride = prev })
}

func TestEnrichVideosViaRetry_FetchSucceedsOnSecondAttempt(t *testing.T) {
	setupFastEnrich(t)
	restoreVideoExtHost(t)
	log, _ := logger.New(logger.DefaultConfig())
	h := NewBotHandler(nil, newMockAgentLoop(), log)

	poor := videoAtt(-100, 400, nil, "")
	rich := videoAtt(-100, 400, map[string]interface{}{
		"mp4_854": map[string]interface{}{"url": "http://example.com/rich.mp4"},
	}, "")
	fetcher := &seqFetcher{resps: [][]VKMessage{{}, {{ID: 7, Attachments: []VKAttachment{rich}}}}}
	h.messageFetcher = fetcher

	msg := &VKMessage{ID: 7, PeerID: 2000000001, Text: "видео"}
	start := time.Now()
	got := h.enrichVideosViaRetry(msg, []VKAttachment{poor})
	elapsed := time.Since(start)

	if elapsed > 3*time.Second {
		t.Errorf("took too long: %v", elapsed)
	}
	if fetcher.called != 2 {
		t.Errorf("expected 2 fetches (early exit on success), got %d", fetcher.called)
	}
	videoData, _ := got[0].Raw["video"].(map[string]interface{})
	files, _ := videoData["files"].(map[string]interface{})
	if files == nil {
		t.Fatal("expected files map in enriched attachment")
	}
	mp4, _ := files["mp4_854"].(map[string]interface{})
	if u, _ := mp4["url"].(string); u != "http://example.com/rich.mp4" {
		t.Errorf("mp4_854 url = %q", u)
	}
}

func TestEnrichVideosViaRetry_ZeroIDUsesConstructedURL(t *testing.T) {
	setupFastEnrich(t)
	payload := make([]byte, 150*1024)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(payload)))
		if r.Method == http.MethodHead {
			return
		}
		w.Write(payload)
	}))
	defer srv.Close()
	videoExtHostOverride = srv.URL
	log, _ := logger.New(logger.DefaultConfig())
	h := NewBotHandler(nil, newMockAgentLoop(), log)

	poor := videoAtt(-100, 400, nil, "")
	msg := &VKMessage{ID: 0, PeerID: 2000000001, Text: "видео"}
	start := time.Now()
	got := h.enrichVideosViaRetry(msg, []VKAttachment{poor})
	elapsed := time.Since(start)

	if elapsed > 3*time.Second {
		t.Errorf("took too long: %v", elapsed)
	}
	want := fmt.Sprintf("%s/video_ext.php?oid=-100&id=400&expire=0&hash=&hd=1", srv.URL)
	videoData, _ := got[0].Raw["video"].(map[string]interface{})
	playURL, _ := videoData["playUrl"].(string)
	if playURL != want {
		t.Errorf("playUrl = %q, want %q", playURL, want)
	}
	url, _, ok := extractVideoURL(videoData, nil)
	if !ok || url != want {
		t.Errorf("extractVideoURL = %q, %v; want %q", url, ok, want)
	}
}

func TestEnrichVideosViaRetry_AllFailReturnsOriginal(t *testing.T) {
	setupFastEnrich(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()
	videoExtHostOverride = srv.URL
	log, _ := logger.New(logger.DefaultConfig())
	h := NewBotHandler(nil, newMockAgentLoop(), log)

	poor := videoAtt(-100, 400, nil, "")
	msg := &VKMessage{ID: 0, PeerID: 2000000001, Text: "видео"}
	start := time.Now()
	got := h.enrichVideosViaRetry(msg, []VKAttachment{poor})
	elapsed := time.Since(start)

	if elapsed > 3*time.Second {
		t.Errorf("budget exceeded: %v", elapsed)
	}
	if !deepEqualAttachments(got[0], poor) {
		t.Errorf("attachment mutated despite all failures: %+v", got[0])
	}
}

func deepEqualAttachments(a, b VKAttachment) bool {
	return a.Type == b.Type && reflect.DeepEqual(a.Raw, b.Raw)
}
