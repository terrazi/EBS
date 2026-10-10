package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"ebs-v3-service/internal/sim"
)

func TestSimulationAPIRequiresSessionAndCommitsOperation(t *testing.T) {
	e, err := sim.Open(t.TempDir() + "/sim.sqlite3")
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	mux := v3APIMux(newStore())
	installSimulationAPI(mux, e)
	ts := httptest.NewServer(mux)
	defer ts.Close()
	post := func(path string, body any, token string) *http.Response {
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest(http.MethodPost, ts.URL+path, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, er := http.DefaultClient.Do(req)
		if er != nil {
			t.Fatal(er)
		}
		return resp
	}
	resp := post("/api/v3/operation", map[string]any{"operation": "banking.cash_open"}, "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauth status=%d", resp.StatusCode)
	}
	resp.Body.Close()
	resp = post("/api/v3/login", map[string]any{"username": "gise01", "password": "1234", "client": "personel"}, "")
	var login map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&login)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("login status=%d body=%v", resp.StatusCode, login)
	}
	token, _ := login["token"].(string)
	resp = post("/api/v3/operation", map[string]any{"operation": "banking.cash_open", "terminal": "test"}, token)
	if resp.StatusCode != 200 {
		t.Fatalf("cash status=%d", resp.StatusCode)
	}
	resp.Body.Close()
}
