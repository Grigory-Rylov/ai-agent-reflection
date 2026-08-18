package vk

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

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

			// Roundtrip: marshal then unmarshal
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

	downloaded, err := DownloadAttachments(attachments, "./attachments")
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

// TestBuildFullTextLongPollFallback проверяет, что когда getById не принёс
// сообщение (id=0 / fullMsgMap пуст), buildFullText всё равно скачивает аттачи
// из самого long-poll события и дописывает путь к файлу в промпт. Без этого
// фикса агент не видел путь к картинке/файлу и не понимал, о чём его просят.
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

	// id=0 и fullMsgMap пуст — ровно тот сценарий из лога:
	// "long poll carried 1 attachment(s) but full message not fetched".
	msg := &VKMessage{ID: 0, PeerID: 2000000001, Text: "что на фото", Attachments: []VKAttachment{att}}
	out := h.buildFullText(msg, nil)

	if !strings.Contains(out, "что на фото") {
		t.Errorf("prompt should keep original text, got: %s", out)
	}
	if !strings.Contains(out, "saved to:") {
		t.Fatalf("prompt should contain downloaded file path, got: %s", out)
	}
	// Путь должен указывать на реально скачанный файл в attachmentsDir.
	if !strings.Contains(out, dir) {
		t.Errorf("prompt should contain path under %s, got: %s", dir, out)
	}
}

// TestBuildFullTextNoAttachmentsWithoutFullMsg — без аттачей и без getById
// возвращается только текст (без падений).
func TestBuildFullTextNoAttachmentsWithoutFullMsg(t *testing.T) {
	h := NewBotHandler(nil, newMockAgentLoop(), mustLogger(t))
	h.attachmentsDir = t.TempDir()

	msg := &VKMessage{ID: 0, PeerID: 2000000001, Text: "просто текст"}
	if got := h.buildFullText(msg, nil); got != "просто текст" {
		t.Errorf("expected plain text, got %q", got)
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

	downloaded, err := DownloadAttachments(attachments, t.TempDir())
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
