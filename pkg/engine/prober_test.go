package engine

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientProbeMatchesAdvertisedModel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"data":[{"id":"foo","name":"Foo"}]}`))
	}))
	defer srv.Close()

	ok, err := ClientProbe(srv.URL, "foo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("expected healthy=true for advertised model")
	}
}

func TestClientProbeRejectsUnknownModel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"data":[{"id":"foo","name":"Foo"}]}`))
	}))
	defer srv.Close()

	ok, err := ClientProbe(srv.URL, "bar")
	if ok {
		t.Error("expected healthy=false for unknown model")
	}
	if err == nil {
		t.Fatal("expected error for unknown model")
	}
	requireContains(t, err.Error(), "not advertised")
}

func TestClientProbeNon200ReportsStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	ok, err := ClientProbe(srv.URL, "foo")
	if ok {
		t.Error("expected healthy=false for 500 response")
	}
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
	requireContains(t, err.Error(), "500")
}

func TestClientProbeRefusedConnection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close()

	ok, err := ClientProbe(url, "foo")
	if ok {
		t.Error("expected healthy=false for closed listener")
	}
	if err == nil {
		t.Fatal("expected error for closed listener")
	}
	requireContains(t, err.Error(), "probing")
}

func TestClientProbeEmptySubstrAcceptsAnyListing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"data":[{"id":"whatever"}]}`))
	}))
	defer srv.Close()

	ok, err := ClientProbe(srv.URL, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("expected healthy=true with empty substring filter")
	}
}

func TestProberHealthyDelegatesToInjectedProbe(t *testing.T) {
	p := NewProber(nil)
	p.Probe = func(url string, modelSubstr string) (bool, error) {
		if !strings.HasSuffix(url, "/v1/models") {
			t.Errorf("unexpected probe url %q", url)
		}
		return true, nil
	}
	ok, err := p.Healthy("http://example.invalid:9/v1/models", "m")
	if err != nil || !ok {
		t.Errorf("healthy = %v, %v; want true, nil", ok, err)
	}
}
