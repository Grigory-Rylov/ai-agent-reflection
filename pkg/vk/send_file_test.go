package vk

import "testing"

func TestSafeUploadName(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		want     string
	}{
		{"html gets .txt", "index.html", "index.html.txt"},
		{"htm gets .txt", "page.htm", "page.htm.txt"},
		{"svg gets .txt", "icon.svg", "icon.svg.txt"},
		{"js gets .txt", "app.js", "app.js.txt"},
		{"uppercase HTML gets .txt", "INDEX.HTML", "INDEX.HTML.txt"},
		{"txt unchanged", "notes.txt", "notes.txt"},
		{"png unchanged", "image.png", "image.png"},
		{"pdf unchanged", "doc.pdf", "doc.pdf"},
		{"no extension unchanged", "README", "README"},
		{"empty unchanged", "", ""},
		{"nested path uses base ext", "report.html", "report.html.txt"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SafeUploadName(tt.filename); got != tt.want {
				t.Errorf("SafeUploadName(%q) = %q, want %q", tt.filename, got, tt.want)
			}
		})
	}
}
