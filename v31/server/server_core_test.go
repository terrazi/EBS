package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCentralLoginReturnsV3SessionToken(t *testing.T) {
	stop := make(chan struct{})
	t.Setenv("ProgramData", t.TempDir())
	srv, cleanup := newHTTPServer(stop)
	defer cleanup()
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()
	close(stop)

	resp, err := http.Post(ts.URL+"/api/login", "application/json", bytes.NewBufferString(`{"username":"gise01","password":"1234"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	v3, _ := out["v3_token"].(string)
	if resp.StatusCode != http.StatusOK || v3 == "" {
		t.Fatalf("combined login status=%d response=%v", resp.StatusCode, out)
	}
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/v3api/api/v3/session", nil)
	req.Header.Set("Authorization", "Bearer "+v3)
	check, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer check.Body.Close()
	if check.StatusCode != http.StatusOK {
		t.Fatalf("v3 session status=%d", check.StatusCode)
	}
}

func TestCentralHostedUIAndLegacyProxy(t *testing.T) {
	legacyMux := http.NewServeMux()
	legacyMux.HandleFunc("/api/public/branding", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"bank_name":"Demo"}`)
	})
	legacyMux.HandleFunc("/web/app.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		_, _ = io.WriteString(w, `console.log("http://127.0.0.1:8787/api/test")`)
	})
	legacyMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("X-Frame-Options", "DENY")
		_, _ = io.WriteString(w, `<!doctype html><html><head></head><body>http://127.0.0.1:8787/api/test</body></html>`)
	})
	legacyLn, err := net.Listen("tcp", "127.0.0.1:8787")
	if err != nil {
		t.Skipf("8787 test port unavailable: %v", err)
	}
	legacySrv := &http.Server{Handler: legacyMux}
	go legacySrv.Serve(legacyLn)
	defer legacySrv.Close()

	stop := make(chan struct{})
	t.Setenv("ProgramData", t.TempDir())
	srv, cleanup := newHTTPServer(stop)
	defer cleanup()
	v3Ln, err := net.Listen("tcp", "127.0.0.1:8792")
	if err != nil {
		t.Skipf("8792 test port unavailable: %v", err)
	}
	go srv.Serve(v3Ln)
	defer func() {
		close(stop)
		_ = srv.Close()
	}()
	time.Sleep(100 * time.Millisecond)

	paths := []string{
		"/health",
		"/ui/?client=admin",
		"/runtime.json?client=admin",
		"/design_cards.json",
		"/app.js",
		"/styles.css",
		"/api/public/branding",
		"/legacy/?v3token=abc&v3page=dashboard",
		"/web/app.js",
		"/v3api/api/health",
	}
	for _, p := range paths {
		resp, err := http.Get("http://127.0.0.1:8792" + p)
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode >= 400 {
			t.Fatalf("%s status %d body %s", p, resp.StatusCode, string(b))
		}
		if strings.Contains(string(b), "http://127.0.0.1:8787") {
			t.Fatalf("%s leaks legacy localhost into browser response: %s", p, string(b))
		}
		if p == "/runtime.json?client=admin" && !strings.Contains(string(b), `"legacy_url":"/legacy/"`) {
			t.Fatalf("runtime legacy_url wrong: %s", string(b))
		}
	}
}
