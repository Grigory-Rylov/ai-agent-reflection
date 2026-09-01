package access

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGrantPathThroughSymlinkNonExistent(t *testing.T) {
	baseDir, err := os.MkdirTemp("", "sym_base_*")
	if err != nil {
		t.Fatalf("create base: %v", err)
	}
	defer os.RemoveAll(baseDir)

	realDir, err := os.MkdirTemp("", "sym_real_*")
	if err != nil {
		t.Fatalf("create real: %v", err)
	}
	defer os.RemoveAll(realDir)

	linkPath := filepath.Join(baseDir, "app")
	if err := os.Symlink(realDir, linkPath); err != nil {
		t.Skip("symlinks not supported")
	}

	ctrl := NewController([]string{})

	targetFile := filepath.Join(linkPath, "newfile.txt")
	ctrl.GrantPathForPeer(1, targetFile)

	result := ctrl.CheckAccessForPeer(1, targetFile)
	if !result.Allowed {
		t.Errorf("expected granted path to be allowed, got: %s", result.Reason)
	}

	siblingFile := filepath.Join(linkPath, "other.txt")
	result = ctrl.CheckAccessForPeer(1, siblingFile)
	if !result.Allowed {
		t.Errorf("expected sibling in same dir to be allowed, got: %s", result.Reason)
	}
}

func TestPerPeerIsolation(t *testing.T) {
	dir, err := os.MkdirTemp("", "peer_iso_*")
	if err != nil {
		t.Fatalf("create dir: %v", err)
	}
	defer os.RemoveAll(dir)

	ctrl := NewController([]string{})
	testFile := filepath.Join(dir, "file.txt")

	ctrl.GrantPathForPeer(100, testFile)

	if !ctrl.CheckAccessForPeer(100, testFile).Allowed {
		t.Error("peer 100 should have access")
	}
	if ctrl.CheckAccessForPeer(200, testFile).Allowed {
		t.Error("peer 200 should NOT have access (isolation)")
	}
}

func TestParentDirGrantCoversSiblings(t *testing.T) {
	dir, err := os.MkdirTemp("", "parent_dir_*")
	if err != nil {
		t.Fatalf("create dir: %v", err)
	}
	defer os.RemoveAll(dir)

	subDir := filepath.Join(dir, "sub")
	os.MkdirAll(subDir, 0755)

	ctrl := NewController([]string{})
	newFile := filepath.Join(subDir, "brand_new.txt")

	ctrl.GrantPathForPeer(1, newFile)

	if !ctrl.CheckAccessForPeer(1, newFile).Allowed {
		t.Error("granted file should be accessible")
	}
	sibling := filepath.Join(subDir, "another.txt")
	if !ctrl.CheckAccessForPeer(1, sibling).Allowed {
		t.Error("sibling file in same dir should be accessible")
	}
}

func TestClearPeerRemovesGrants(t *testing.T) {
	dir, err := os.MkdirTemp("", "clear_peer_*")
	if err != nil {
		t.Fatalf("create dir: %v", err)
	}
	defer os.RemoveAll(dir)

	ctrl := NewController([]string{})
	file := filepath.Join(dir, "test.txt")

	ctrl.GrantPathForPeer(42, file)
	if !ctrl.CheckAccessForPeer(42, file).Allowed {
		t.Fatal("expected access before clear")
	}

	ctrl.ClearPeer(42)
	if ctrl.CheckAccessForPeer(42, file).Allowed {
		t.Error("expected no access after ClearPeer")
	}
}

func TestRevokePathForPeer(t *testing.T) {
	dir, err := os.MkdirTemp("", "revoke_peer_*")
	if err != nil {
		t.Fatalf("create dir: %v", err)
	}
	defer os.RemoveAll(dir)

	ctrl := NewController([]string{})
	file := filepath.Join(dir, "doc.txt")

	ctrl.GrantPathForPeer(7, file)
	if !ctrl.CheckAccessForPeer(7, file).Allowed {
		t.Fatal("expected access after grant")
	}

	ctrl.RevokePathForPeer(7, file)
	if ctrl.CheckAccessForPeer(7, file).Allowed {
		t.Error("expected no access after revoke")
	}
}

func TestCanonicalPathExported(t *testing.T) {
	dir, err := os.MkdirTemp("", "canonical_*")
	if err != nil {
		t.Fatalf("create dir: %v", err)
	}
	defer os.RemoveAll(dir)

	canonical, err := CanonicalPath(filepath.Join(dir, "nonexistent", "file.txt"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := filepath.Join(dir, "nonexistent", "file.txt")
	if canonical != expected {
		t.Errorf("expected %s, got %s", expected, canonical)
	}
}

func TestCanonicalPathFollowsMidSymlink(t *testing.T) {
	baseDir, err := os.MkdirTemp("", "canon_sym_*")
	if err != nil {
		t.Fatalf("create base: %v", err)
	}
	defer os.RemoveAll(baseDir)

	realDir, err := os.MkdirTemp("", "canon_real_*")
	if err != nil {
		t.Fatalf("create real: %v", err)
	}
	defer os.RemoveAll(realDir)

	linkPath := filepath.Join(baseDir, "lnk")
	if err := os.Symlink(realDir, linkPath); err != nil {
		t.Skip("symlinks not supported")
	}

	input := filepath.Join(linkPath, "deep", "file.txt")
	canonical, err := CanonicalPath(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := filepath.Join(realDir, "deep", "file.txt")
	if canonical != expected {
		t.Errorf("expected %s, got %s", expected, canonical)
	}
}

func TestGlobalScopeStillWorks(t *testing.T) {
	dir, err := os.MkdirTemp("", "global_scope_*")
	if err != nil {
		t.Fatalf("create dir: %v", err)
	}
	defer os.RemoveAll(dir)

	ctrl := NewController([]string{})
	file := filepath.Join(dir, "g.txt")

	ctrl.GrantPath(dir)
	if !ctrl.CheckAccess(file).Allowed {
		t.Error("global grant should allow access via CheckAccess")
	}

	ctrl.RevokePath(dir)
	if ctrl.CheckAccess(file).Allowed {
		t.Error("after revoke, CheckAccess should deny")
	}
}

func TestAllowedDirsIncludesGlobal(t *testing.T) {
	dir1, err := os.MkdirTemp("", "dirs_g1_*")
	if err != nil {
		t.Fatalf("create dir1: %v", err)
	}
	defer os.RemoveAll(dir1)

	dir2, err := os.MkdirTemp("", "dirs_g2_*")
	if err != nil {
		t.Fatalf("create dir2: %v", err)
	}
	defer os.RemoveAll(dir2)

	ctrl := NewController([]string{dir1})
	ctrl.GrantPath(dir2)

	dirs := ctrl.AllowedDirs()
	found1, found2 := false, false
	for _, d := range dirs {
		if d == dir1 {
			found1 = true
		}
		if d == dir2 {
			found2 = true
		}
	}
	if !found1 {
		t.Error("AllowedDirs should include statically-configured dir")
	}
	if !found2 {
		t.Error("AllowedDirs should include globally-granted dir")
	}
}
