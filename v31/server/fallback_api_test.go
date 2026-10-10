package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func loginTest(t *testing.T, base, user, pass string) string {
	t.Helper()
	resp, err := http.Post(base+"/api/login", "application/json", bytes.NewBufferString(`{"username":"`+user+`","password":"`+pass+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("login failed %d %s", resp.StatusCode, b)
	}
	var x map[string]any
	if err := json.Unmarshal(b, &x); err != nil {
		t.Fatal(err)
	}
	tok, _ := x["token"].(string)
	if tok == "" {
		t.Fatalf("token missing: %s", b)
	}
	return tok
}

func authGet(t *testing.T, base, path, tok string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, base+path, nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestCentralAPILoginWithoutLegacy(t *testing.T) {
	stop := make(chan struct{})
	srv := newHTTPServer(stop)
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()
	close(stop)

	tok := loginTest(t, ts.URL, "admin", "EBS2026!")
	resp := authGet(t, ts.URL, "/api/users", tok)
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(b), "gise01") || strings.Contains(string(b), "ogrenci01") {
		t.Fatalf("users=%d %s", resp.StatusCode, b)
	}

	resp = authGet(t, ts.URL, "/api/customers?limit=5000", tok)
	b, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(b), "Ayşe") {
		t.Fatalf("customers=%d %s", resp.StatusCode, b)
	}
}

func TestCustomerLoginAndSummary(t *testing.T) {
	stop := make(chan struct{})
	srv := newHTTPServer(stop)
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()
	close(stop)
	tok := loginTest(t, ts.URL, "Ayşe Yılmaz", "1234")
	resp := authGet(t, ts.URL, "/api/customer-portal/summary", tok)
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(b), "POL-2026-001245") {
		t.Fatalf("summary=%d %s", resp.StatusCode, b)
	}
}

func TestAllDefaultStaffCredentials(t *testing.T) {
	stop := make(chan struct{})
	srv := newHTTPServer(stop)
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()
	close(stop)
	cases := [][2]string{{"admin", "EBS2026!"}, {"gise01", "1234"}, {"gise02", "1234"}, {"musteritemsilci", "1234"}, {"operasyonyetkilisi", "1234"}, {"krediyetkilisi", "1234"}, {"sigortayetkilisi", "1234"}, {"hasaryetkilisi", "1234"}, {"denetci", "1234"}}
	for _, tc := range cases {
		if tok := loginTest(t, ts.URL, tc[0], tc[1]); tok == "" {
			t.Fatalf("empty token for %s", tc[0])
		}
	}
}
