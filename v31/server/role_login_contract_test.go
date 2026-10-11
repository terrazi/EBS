package main

import (
	"bytes"
	"ebs-v3-service/internal/sim"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func postLogin(t *testing.T, base, username, password, client string) (int, map[string]any) {
	t.Helper()
	body, err := json.Marshal(map[string]string{"username": username, "password": password, "client": client})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(base+"/api/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("login returned non-JSON: %v", err)
	}
	return resp.StatusCode, out
}

func newLoginContractServer(t *testing.T) (*sim.Engine, *httptest.Server) {
	t.Helper()
	e, err := sim.Open(filepath.Join(t.TempDir(), "login.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close() })
	mux := http.NewServeMux()
	mux.Handle("/api/", centralAPIHandler(e, nil))
	v3 := http.NewServeMux()
	installSimulationAPI(v3, e)
	mux.Handle("/v3api/", http.StripPrefix("/v3api", v3))
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return e, ts
}

func TestAllAdvertisedCredentialsHaveOneDurableSession(t *testing.T) {
	_, ts := newLoginContractServer(t)
	doc, err := os.ReadFile("../installer/payload/GIRIS_BILGILERI.txt")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct{ username, password, client, role string }{
		{"admin", "EBS2026!", "admin", "admin"},
		{"gise01", "1234", "personnel", "teller"},
		{"gise02", "1234", "personnel", "teller"},
		{"musteritemsilci", "1234", "personnel", "customer_service"},
		{"operasyonyetkilisi", "1234", "personnel", "operations"},
		{"krediyetkilisi", "1234", "personnel", "credit"},
		{"sigortayetkilisi", "1234", "personnel", "insurance"},
		{"hasaryetkilisi", "1234", "personnel", "claims"},
		{"denetci", "1234", "personnel", "auditor"},
		{"Ahmet Karaca", "1234", "customer", "customer"},
		{"Selin Yılmaz", "1234", "customer", "customer"},
		{"Zeynep Demir", "1234", "customer", "customer"},
	}
	for _, tc := range cases {
		t.Run(tc.username, func(t *testing.T) {
			if !strings.Contains(string(doc), tc.username) {
				t.Fatalf("advertised credential omitted from documentation: %s", tc.username)
			}
			status, out := postLogin(t, ts.URL, tc.username, tc.password, tc.client)
			token, _ := out["token"].(string)
			if status != 200 || token == "" || out["v3_token"] != token {
				t.Fatalf("combined durable login=%d %v", status, out)
			}
			user, ok := out["user"].(map[string]any)
			if !ok || user["username"] != tc.username || user["role"] != tc.role {
				t.Fatalf("identity=%v", out["user"])
			}
			for _, path := range []string{"/api/me", "/v3api/api/v3/session"} {
				resp := authGet(t, ts.URL, path, token)
				b, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				if resp.StatusCode != 200 || !strings.Contains(string(b), tc.username) {
					t.Fatalf("session %s=%d %s", path, resp.StatusCode, b)
				}
			}
			if tc.client == "admin" {
				resp := authGet(t, ts.URL, "/api/users", token)
				var users []map[string]any
				decodeErr := json.NewDecoder(resp.Body).Decode(&users)
				resp.Body.Close()
				if decodeErr != nil || resp.StatusCode != 200 || len(users) != 9 {
					t.Fatalf("staff catalogue status=%d users=%v err=%v", resp.StatusCode, users, decodeErr)
				}
				for _, user := range users {
					matched := false
					for _, want := range cases {
						if want.username == user["username"] {
							matched = true
							if want.role != user["role"] {
								t.Fatalf("assignment role differs from login: %v want=%s", user, want.role)
							}
						}
					}
					if !matched {
						t.Fatalf("unexpected demo staff identity: %v", user)
					}
				}
			}
			if tc.client == "customer" {
				resp := authGet(t, ts.URL, "/api/customer-portal/summary", token)
				var summary struct {
					Customer map[string]any   `json:"customer"`
					Accounts []map[string]any `json:"accounts"`
				}
				decodeErr := json.NewDecoder(resp.Body).Decode(&summary)
				resp.Body.Close()
				if decodeErr != nil || resp.StatusCode != 200 || summary.Customer["full_name"] != tc.username || len(summary.Accounts) != 2 {
					t.Fatalf("persistent customer summary=%+v status=%d err=%v", summary, resp.StatusCode, decodeErr)
				}
				for _, account := range summary.Accounts {
					if account["customer_id"] != summary.Customer["id"] {
						t.Fatalf("customer account detached from identity: %+v", account)
					}
				}
				resp = authGet(t, ts.URL, "/api/customers/30", token)
				resp.Body.Close()
				if resp.StatusCode != http.StatusForbidden {
					t.Fatalf("other customer exposed: status=%d", resp.StatusCode)
				}
			}
			status, wrong := postLogin(t, ts.URL, tc.username, "incorrect-password", tc.client)
			if status != 401 || wrong["token"] != nil || wrong["v3_token"] != nil {
				t.Fatalf("wrong password accepted=%d %v", status, wrong)
			}
			logoutPath := "/api/logout"
			if tc.client == "customer" {
				logoutPath = "/v3api/api/v3/logout"
			}
			req, _ := http.NewRequest(http.MethodPost, ts.URL+logoutPath, nil)
			req.Header.Set("Authorization", "Bearer "+token)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("logout %s status=%d", logoutPath, resp.StatusCode)
			}
			for _, path := range []string{"/api/me", "/v3api/api/v3/session"} {
				resp = authGet(t, ts.URL, path, token)
				resp.Body.Close()
				if resp.StatusCode != 401 {
					t.Fatalf("logout didn't invalidate %s: status=%d", path, resp.StatusCode)
				}
			}
		})
	}
}

func TestCentralLoginUsesDatabaseIdentityAndStatus(t *testing.T) {
	e, ts := newLoginContractServer(t)
	if _, err := e.DB.Exec(`INSERT INTO users(username,password_hash,salt,display_name,role,department,permissions_json,active) SELECT 'new-trainee',password_hash,salt,'Yeni Katılımcı',role,department,permissions_json,active FROM users WHERE username='gise01'`); err != nil {
		t.Fatal(err)
	}
	status, out := postLogin(t, ts.URL, "new-trainee", "1234", "personnel")
	if status != 200 || out["v3_token"] == nil {
		t.Fatalf("new persistent identity rejected=%d %v", status, out)
	}
	status, out = postLogin(t, ts.URL, "gise01", "1234", "personnel")
	token := out["token"].(string)
	if _, err := e.DB.Exec("UPDATE users SET active=0 WHERE username='gise01'"); err != nil {
		t.Fatal(err)
	}
	status, out = postLogin(t, ts.URL, "gise01", "1234", "personnel")
	if status != 401 || out["token"] != nil {
		t.Fatalf("disabled persisted identity bypassed=%d %v", status, out)
	}
	resp := authGet(t, ts.URL, "/api/me", token)
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("disabled identity retains compatibility session=%d", resp.StatusCode)
	}
	status, out = postLogin(t, ts.URL, "gise02", "1234", "personnel")
	expiredToken := out["token"].(string)
	if _, err := e.DB.Exec("UPDATE sessions SET expires_at='2000-01-01T00:00:00Z' WHERE token=?", expiredToken); err != nil {
		t.Fatal(err)
	}
	resp = authGet(t, ts.URL, "/api/me", expiredToken)
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("expired central session accepted by compatibility endpoint=%d", resp.StatusCode)
	}
	if _, err := e.DB.Exec("UPDATE users SET password_hash='changed' WHERE username='gise02'"); err != nil {
		t.Fatal(err)
	}
	status, out = postLogin(t, ts.URL, "gise02", "1234", "personnel")
	if status != 401 || out["token"] != nil {
		t.Fatalf("hardcoded fallback bypassed changed database password=%d %v", status, out)
	}
}

func TestCentralLoginRejectsUnavailableEngineAndWrongClient(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"username":"admin","password":"EBS2026!"}`))
	w := httptest.NewRecorder()
	centralAPIHandler(nil, nil).ServeHTTP(w, req)
	if w.Code != 503 || !json.Valid(w.Body.Bytes()) || strings.Contains(w.Body.String(), `"token"`) {
		t.Fatalf("nil engine login=%d %s", w.Code, w.Body.String())
	}
	_, ts := newLoginContractServer(t)
	for _, tc := range [][3]string{{"gise01", "1234", "admin"}, {"admin", "EBS2026!", "customer"}, {"Ahmet Karaca", "1234", "personnel"}} {
		status, out := postLogin(t, ts.URL, tc[0], tc[1], tc[2])
		if status != 401 || out["token"] != nil {
			t.Fatalf("wrong client accepted=%d %v", status, out)
		}
	}
	resp, err := http.Post(ts.URL+"/api/login", "application/json", strings.NewReader(`{"username":"admin","password":"EBS2026!"} trailing`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("malformed login JSON accepted=%d", resp.StatusCode)
	}
}
