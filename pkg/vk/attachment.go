package vk

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// ============================================================
// Attachment download and formatting
// ============================================================

// GetAttachmentDownloadURL extracts download URL and filename from a VK attachment.
// Supports types: photo, doc, audio, audio_message, sticker.
func GetAttachmentDownloadURL(attachment map[string]interface{}) (string, string, bool) {
	attType, _ := attachment["type"].(string)
	if attType == "" {
		return "", "", false
	}

	attData, ok := attachment[attType].(map[string]interface{})
	if !ok {
		return "", "", false
	}

	switch attType {
	case "photo":
		return extractPhotoURL(attData)
	case "doc":
		return extractDocURL(attData)
	case "audio_message":
		return extractAudioMessageURL(attData)
	case "audio":
		return extractAudioURL(attData)
	case "sticker":
		return extractStickerURL(attData)
	default:
		return "", "", false
	}
}

// extractPhotoURL picks the largest photo size from sizes[] array.
func extractPhotoURL(attData map[string]interface{}) (string, string, bool) {
	sizes, _ := attData["sizes"].([]interface{})
	if len(sizes) == 0 {
		return "", "", false
	}

	sorted := sortPhotoSizes(sizes)
	best, ok := sorted[0].(map[string]interface{})
	if !ok {
		return "", "", false
	}

	url, _ := best["url"].(string)
	id, _ := attData["id"].(float64)
	filename := fmt.Sprintf("photo_%.0f.jpg", id)

	return url, filename, url != ""
}

// sortPhotoSizes sorts photo sizes by priority: w > z > y > x > m > s.
func sortPhotoSizes(sizes []interface{}) []interface{} {
	priority := map[string]int{"s": 1, "m": 2, "x": 3, "y": 4, "z": 5, "w": 6}

	sort.Slice(sizes, func(i, j int) bool {
		si, _ := sizes[i].(map[string]interface{})
		sj, _ := sizes[j].(map[string]interface{})
		tyi, _ := si["type"].(string)
		tyj, _ := sj["type"].(string)
		return priority[tyi] > priority[tyj]
	})

	return sizes
}

// extractDocURL gets the document download URL and title.
func extractDocURL(attData map[string]interface{}) (string, string, bool) {
	url, _ := attData["url"].(string)
	title, _ := attData["title"].(string)
	id, _ := attData["id"].(float64)

	filename := title
	if filename == "" {
		filename = fmt.Sprintf("doc_%.0f", id)
	}

	return url, filename, url != ""
}

// extractAudioMessageURL gets the voice message download URL.
func extractAudioMessageURL(attData map[string]interface{}) (string, string, bool) {
	url, _ := attData["link_mp3"].(string)
	ext := "mp3"
	if url == "" {
		url, _ = attData["link_ogg"].(string)
		ext = "ogg"
	}

	duration, _ := attData["duration"].(float64)
	filename := fmt.Sprintf("voice_msg_%.0fs.%s", duration, ext)

	return url, filename, url != ""
}

// extractAudioURL gets the audio track download URL.
func extractAudioURL(attData map[string]interface{}) (string, string, bool) {
	url, _ := attData["url"].(string)
	if url == "" {
		return "", "", false
	}

	id, _ := attData["id"].(float64)
	artist, _ := attData["artist"].(string)
	title, _ := attData["title"].(string)

	filename := fmt.Sprintf("audio_%.0f.mp3", id)
	if artist != "" && title != "" {
		filename = sanitizeFilename(artist + " - " + title + ".mp3")
	}

	return url, filename, true
}

// extractStickerURL gets the sticker image URL (last element of images[]).
func extractStickerURL(attData map[string]interface{}) (string, string, bool) {
	images, _ := attData["images"].([]interface{})
	if len(images) == 0 {
		return "", "", false
	}

	last, ok := images[len(images)-1].(map[string]interface{})
	if !ok {
		return "", "", false
	}

	url, _ := last["url"].(string)
	id, _ := attData["id"].(float64)
	filename := fmt.Sprintf("sticker_%.0f.png", id)

	return url, filename, url != ""
}

// ============================================================
// Download
// ============================================================

// DownloadAttachments downloads all attachments to saveDir.
// Creates saveDir if it doesn't exist.
// Prepends timestamp (YYYYMMDD_HHmmSS) to each filename.
func DownloadAttachments(attachments []map[string]interface{}, saveDir string) ([]DownloadedAttachment, error) {
	absDir, err := filepath.Abs(saveDir)
	if err != nil {
		return nil, fmt.Errorf("resolve dir %s: %w", saveDir, err)
	}
	if err := os.MkdirAll(absDir, 0755); err != nil {
		return nil, fmt.Errorf("create dir %s: %w", absDir, err)
	}

	var results []DownloadedAttachment
	timestamp := time.Now().Format("20060102_150405")

	for _, att := range attachments {
		url, filename, ok := GetAttachmentDownloadURL(att)
		if !ok {
			continue
		}

		attType, _ := att["type"].(string)
		safeName := sanitizeFilename(filename)
		name, ext := splitFileName(safeName)
		timedName := fmt.Sprintf("%s_%s%s", timestamp, name, ext)

		result, err := downloadSingle(url, filepath.Join(absDir, timedName))
		if err != nil {
			return results, fmt.Errorf("download %s: %w", filename, err)
		}
		result.Type = attType
		results = append(results, result)
	}

	return results, nil
}

// downloadSingle fetches a URL and saves it to the given path.
func downloadSingle(urlStr, destPath string) (DownloadedAttachment, error) {
	client := &http.Client{Timeout: 30 * time.Second}

	resp, err := client.Get(urlStr)
	if err != nil {
		return DownloadedAttachment{}, fmt.Errorf("http get: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return DownloadedAttachment{}, fmt.Errorf("http error: %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return DownloadedAttachment{}, fmt.Errorf("read body: %w", err)
	}

	if err := os.WriteFile(destPath, data, 0644); err != nil {
		return DownloadedAttachment{}, fmt.Errorf("write file: %w", err)
	}

	return DownloadedAttachment{
		Path:     destPath,
		Filename: filepath.Base(destPath),
	}, nil
}

// splitFileName separates a filename into name and extension parts.
func splitFileName(filename string) (string, string) {
	idx := strings.LastIndex(filename, ".")
	if idx <= 0 {
		return filename, ""
	}
	return filename[:idx], filename[idx:]
}

// sanitizeFilename keeps only safe characters in the filename.
func sanitizeFilename(name string) string {
	re := regexp.MustCompile(`[^a-zA-Z0-9._\-]`)
	return re.ReplaceAllString(name, "_")
}

// ============================================================
// Formatting
// ============================================================

// FormatAttachmentInfo returns a formatted summary of downloaded files.
func FormatAttachmentInfo(downloaded []DownloadedAttachment) string {
	if len(downloaded) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📥 Downloaded %d file(s):\n", len(downloaded)))

	for _, d := range downloaded {
		line := fmt.Sprintf("• [%s] `%s` saved to: `%s`\n", d.Type, d.Filename, d.Path)
		sb.WriteString(line)
	}

	return sb.String()
}
