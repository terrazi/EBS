package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestV3ActionAndNoLegacyDependency(t *testing.T) {
	stop := make(chan struct{})
	t.Setenv("ProgramData", t.TempDir())
	srv, cleanup := newHTTPServer(stop)
	defer cleanup()
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()
	close(stop)

	resp, err := http.Post(ts.URL+"/v3api/api/action", "application/json", bytes.NewBufferString(`{"card_code":"IN-012","action":"test","client":"personnel"}`))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(b), `"ok":true`) {
		t.Fatalf("v3 action=%d %s", resp.StatusCode, b)
	}
}
