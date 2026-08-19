package session

import "testing"


func TestPeerInputAdmitClaimDrain(t *testing.T) {
	in := &PeerInput{}
	in.Admit("a")
	in.Admit("b")
	in.Admit("c")

	// Claim removes one specific message.
	if !in.Claim("b") {
		t.Fatal("Claim(b) should succeed")
	}
	if in.Claim("b") {
		t.Fatal("Claim(b) a second time should fail (already claimed)")
	}
	if !in.HasPending() {
		t.Fatal("HasPending should be true while messages remain")
	}

	// Drain returns the remaining messages in order.
	got := in.Drain()
	if len(got) != 2 || got[0] != "a" || got[1] != "c" {
		t.Fatalf("Drain() = %v, want [a c]", got)
	}
	if in.HasPending() {
		t.Fatal("HasPending should be false after drain")
	}

	// TakePromoted returns what Drain promoted, once.
	if promoted := in.TakePromoted(); len(promoted) != 2 {
		t.Fatalf("TakePromoted() = %v, want [a c]", promoted)
	}
	if promoted := in.TakePromoted(); len(promoted) != 0 {
		t.Fatalf("TakePromoted() should be empty after first call, got %v", promoted)
	}
}


func TestPeerInputClear(t *testing.T) {
	in := &PeerInput{}
	in.Admit("x")
	in.Drain() // promoted
	in.Admit("y")
	in.Clear()
	if in.HasPending() {
		t.Error("pending should be empty after Clear")
	}
	if promoted := in.TakePromoted(); len(promoted) != 0 {
		t.Errorf("promoted should be empty after Clear, got %v", promoted)
	}
}


func TestPeerInputNilSafety(t *testing.T) {
	var in *PeerInput
	in.Admit("x")      // must not panic
	in.Claim("x")      // must not panic
	in.Drain()         // must not panic
	in.TakePromoted()  // must not panic
	in.Clear()         // must not panic
	if in.HasPending() {
		t.Error("nil PeerInput must not report pending")
	}
}
