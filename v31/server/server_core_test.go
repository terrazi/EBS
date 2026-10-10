package main

import (
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

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
	srv := newHTTPServer(stop)
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
