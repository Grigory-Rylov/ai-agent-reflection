package tools

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParsePatch_Update(t *testing.T) {
	patch := `--- a/hello.go
+++ b/hello.go
@@ -1,3 +1,4 @@
 package main
 
-func hello() string {
-	return "Hello, World!"
+func hello(name string) string {
+	return "Hello, " + name + "!"
 }
`

	files := ParsePatch(patch)
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}

	f := files[0]
	if f.OldPath != "hello.go" {
		t.Errorf("expected old path hello.go, got %s", f.OldPath)
	}
	if f.NewPath != "hello.go" {
		t.Errorf("expected new path hello.go, got %s", f.NewPath)
	}
	if f.IsNew {
		t.Error("expected IsNew=false")
	}
	if f.IsDelete {
		t.Error("expected IsDelete=false")
	}
	if len(f.Hunks) != 1 {
		t.Fatalf("expected 1 hunk, got %d", len(f.Hunks))
	}

	h := f.Hunks[0]
	if h.OldStart != 1 {
		t.Errorf("expected old_start=1, got %d", h.OldStart)
	}
	if h.NewStart != 1 {
		t.Errorf("expected new_start=1, got %d", h.NewStart)
	}
}

func TestParsePatch_NewFile(t *testing.T) {
	patch := `--- /dev/null
+++ b/newfile.go
@@ -0,0 +1,5 @@
+package main
+
+func main() {
+	println("hello")
+}
`

	files := ParsePatch(patch)
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}

	f := files[0]
	if !f.IsNew {
		t.Error("expected IsNew=true")
	}
	if f.OldPath != "/dev/null" {
		t.Errorf("expected old /dev/null, got %s", f.OldPath)
	}
	if f.NewPath != "newfile.go" {
		t.Errorf("expected new newfile.go, got %s", f.NewPath)
	}
}

func TestParsePatch_DeleteFile(t *testing.T) {
	patch := `--- a/old.go
+++ /dev/null
@@ -1,3 +0,0 @@
-package main
-
-func old() {}
`

	files := ParsePatch(patch)
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}

	f := files[0]
	if !f.IsDelete {
		t.Error("expected IsDelete=true")
	}
	if f.NewPath != "/dev/null" {
		t.Errorf("expected new /dev/null, got %s", f.NewPath)
	}
}

func TestParsePatch_MultipleFiles(t *testing.T) {
	patch := `--- a/a.go
+++ b/a.go
@@ -1,1 +1,1 @@
-old a
+new a
--- a/b.go
+++ b/b.go
@@ -1,1 +1,1 @@
-old b
+new b
`

	files := ParsePatch(patch)
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(files))
	}
}

func TestParsePatch_EmptyPatch(t *testing.T) {
	files := ParsePatch("")
	if len(files) != 0 {
		t.Errorf("expected 0 files, got %d", len(files))
	}
}

func TestApplyNewFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "new.txt")

	pf := PatchFile{
		NewPath: path,
		IsNew:   true,
		Hunks: []Hunk{{
			NewStart: 1,
			NewCount: 2,
			Lines:    []string{"+hello", "+world"},
		}},
	}

	result := applyNewFile(path, pf)
	if result["status"] != "created" {
		t.Fatalf("expected status created, got %v", result["status"])
	}

	data, _ := os.ReadFile(path)
	if string(data) != "hello\nworld\n" {
		t.Errorf("unexpected content: %q", string(data))
	}
}

func TestApplyDeleteFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "del.txt")
	os.WriteFile(path, []byte("content"), 0644)

	result := applyDeleteFile(path)
	if result["status"] != "deleted" {
		t.Fatalf("expected status deleted, got %v", result["status"])
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("file should not exist")
	}
}

func TestApplyUpdateFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "update.txt")
	os.WriteFile(path, []byte("before\nmiddle\nafter\n"), 0644)

	pf := PatchFile{
		NewPath: path,
		Hunks: []Hunk{{
			OldStart: 2,
			OldCount: 1,
			NewStart: 2,
			NewCount: 1,
			Lines:    []string{"-middle", "+replaced"},
		}},
	}

	result := applyUpdateFile(path, pf)
	if result["status"] != "updated" {
		t.Fatalf("expected status updated, got %v", result["status"])
	}

	data, _ := os.ReadFile(path)
	expected := "before\nreplaced\nafter\n"
	if string(data) != expected {
		t.Errorf("expected %q, got %q", expected, string(data))
	}
}

func TestClassifyHunkLines(t *testing.T) {
	hunk := Hunk{
		Lines: []string{
			" context",
			"-remove",
			"+add",
		},
	}

	ctx, _, removed := classifyHunkLines(hunk)
	if len(ctx) != 2 {
		t.Errorf("expected 2 context lines, got %d", len(ctx))
	}
	if removed != 1 {
		t.Errorf("expected 1 removed, got %d", removed)
	}
}

func TestMatchContextLines(t *testing.T) {
	candidate := []string{"a", "b", "c"}
	context := []string{"a", "b"}

	if !matchContextLines(candidate, context) {
		t.Error("expected match")
	}

	wrongContext := []string{"a", "x"}
	if matchContextLines(candidate, wrongContext) {
		t.Error("expected no match")
	}
}
