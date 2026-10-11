package main

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCentralHealthDoesNotWaitForOptionalLegacy(t *testing.T) {
	// The old central health handler synchronously waited 1.2 seconds for
	// this optional endpoint, which was also the installer's entire deadline.
	ln, err := net.Listen("tcp", "127.0.0.1:8787")
	if err != nil {
		t.Skipf("optional legacy test port unavailable: %v", err)
	}
	probeStarted := make(chan struct{}, 1)
	legacy := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case probeStarted <- struct{}{}:
		default:
		}
		<-r.Context().Done()
	})}
	go legacy.Serve(ln)
	defer legacy.Close()
	legacyAvailable.Store(false)
	probeDone := make(chan struct{})
	go func() {
		legacyAPIReady()
		close(probeDone)
	}()
	<-probeStarted
	central := httptest.NewServer(centralHealthHandler(true, ""))
	defer central.Close()
	client := &http.Client{Timeout: 400 * time.Millisecond}
	resp, err := client.Get(central.URL + "/health")
	if err != nil {
		t.Fatalf("optional legacy probe prevented central readiness: %v", err)
	}
	defer resp.Body.Close()
	var out struct {
		EngineReady bool `json:"engine_ready"`
		LegacyOK    bool `json:"legacy_ok"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || !out.EngineReady || out.LegacyOK {
		t.Fatalf("central readiness incorrectly depends on optional legacy: status=%d response=%+v", resp.StatusCode, out)
	}
	<-probeDone
}

func TestCentralEngineFailureIsJSONAndNeverReady(t *testing.T) {
	base := filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(base, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ProgramData", base)
	srv, cleanup, err := buildHTTPServer()
	defer cleanup()
	if err == nil {
		t.Fatal("unwritable database path accepted as initialized")
	}
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()
	for _, path := range []string{"/health", "/v3api/api/health", "/v3api/health"} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		var health struct {
			OK    bool   `json:"ok"`
			Ready bool   `json:"engine_ready"`
			Error string `json:"engine_error"`
			State string `json:"state"`
		}
		err = json.NewDecoder(resp.Body).Decode(&health)
		_ = resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusServiceUnavailable || health.OK || health.Ready || health.Error == "" || health.State != "failed" {
			t.Fatalf("%s masks engine failure: status=%d health=%+v", path, resp.StatusCode, health)
		}
	}
}

func TestServerPortConflictDoesNotOpenCentralDatabase(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	t.Setenv("EBS_V3_LISTEN", listener.Addr().String())
	t.Setenv("ProgramData", t.TempDir())
	ready := false
	err = runServerWithReady(make(chan struct{}), func() { ready = true })
	if err == nil || !strings.Contains(err.Error(), "listen") || ready {
		t.Fatalf("occupied port incorrectly accepted: ready=%t err=%v", ready, err)
	}
	if _, err := os.Stat(filepath.Join(programData(), "ebs-v31.sqlite3")); !os.IsNotExist(err) {
		t.Fatalf("duplicate instance initialized central database before binding: %v", err)
	}
}

func TestServerReadinessShutdownAndRestart(t *testing.T) {
	reservation, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := reservation.Addr().String()
	_ = reservation.Close()
	t.Setenv("EBS_V3_LISTEN", address)
	t.Setenv("ProgramData", t.TempDir())
	for attempt := 0; attempt < 2; attempt++ {
		stop := make(chan struct{})
		ready := make(chan struct{})
		result := make(chan error, 1)
		go func() { result <- runServerWithReady(stop, func() { close(ready) }) }()
		select {
		case <-ready:
		case err := <-result:
			t.Fatalf("startup %d failed before readiness: %v", attempt, err)
		case <-time.After(45 * time.Second):
			close(stop)
			t.Fatalf("startup %d did not finish", attempt)
		}
		resp, err := (&http.Client{Timeout: time.Second}).Get("http://" + address + "/health")
		if err != nil {
			close(stop)
			t.Fatal(err)
		}
		var health struct {
			Build string `json:"build"`
			Ready bool   `json:"engine_ready"`
		}
		err = json.NewDecoder(resp.Body).Decode(&health)
		_ = resp.Body.Close()
		close(stop)
		if err != nil || resp.StatusCode != http.StatusOK || !health.Ready || health.Build != centralBuild {
			t.Fatalf("startup %d announced readiness prematurely: status=%d health=%+v error=%v", attempt, resp.StatusCode, health, err)
		}
		select {
		case err := <-result:
			if err != nil {
				t.Fatalf("shutdown %d failed: %v", attempt, err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("service did not close HTTP and SQLite after stop")
		}
	}
}
