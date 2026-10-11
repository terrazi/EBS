package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHealthRequiresReadyCurrentEngine(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"current ready", 200, fmt.Sprintf(`{"protocol":%q,"build":%q,"version":"3.1 STABLE","engine_ready":true}`, healthProtocol, healthBuild), ""},
		{"plain404", 404, "404 page not found\n", "geçersiz JSON"},
		{"database startup failure", 503, fmt.Sprintf(`{"protocol":%q,"build":%q,"version":"3.1 STABLE","engine_ready":false,"engine_error":"unable to open database file"}`, healthProtocol, healthBuild), "unable to open database file"},
		{"old release", 200, fmt.Sprintf(`{"protocol":%q,"build":"20261010-login-fix-6","version":"3.1 STABLE","engine_ready":true}`, healthProtocol), "sürüm uyumsuz"},
		{"warming current engine", 503, fmt.Sprintf(`{"protocol":%q,"build":%q,"version":"3.1 STABLE","engine_ready":false}`, healthProtocol, healthBuild), "hazırlanıyor"},
		{"malformed trailing response", 200, fmt.Sprintf(`{"protocol":%q,"build":%q,"version":"3.1 STABLE","engine_ready":true} 404`, healthProtocol, healthBuild), "geçersiz JSON"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/health" {
					t.Errorf("path=%s", r.URL.Path)
				}
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer server.Close()
			err := checkHealth(server.URL)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("ready service rejected: %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
		})
	}
}

func TestHealthAllowsSlowReadinessResponse(t *testing.T) {
	// The former 1.2/1.3 second timeout falsely rejected a healthy local server.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(1500 * time.Millisecond)
		fmt.Fprintf(w, `{"protocol":%q,"build":%q,"version":"3.1","engine_ready":true}`, healthProtocol, healthBuild)
	}))
	defer server.Close()
	if err := checkHealth(server.URL); err != nil {
		t.Fatal(err)
	}
}

func TestHealthNeverAcceptsRedirectOrOversizedBody(t *testing.T) {
	ready := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"protocol":%q,"build":%q,"version":"3.1","engine_ready":true}`, healthProtocol, healthBuild)
	}))
	defer ready.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, ready.URL+"/health", http.StatusFound)
	}))
	defer redirect.Close()
	if err := checkHealth(redirect.URL); err == nil {
		t.Fatal("redirect accepted")
	}
	oversized := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, strings.Repeat(" ", 64<<10)) }))
	defer oversized.Close()
	if err := checkHealth(oversized.URL); err == nil || !strings.Contains(err.Error(), "sınırı") {
		t.Fatalf("oversized body accepted: %v", err)
	}
}
