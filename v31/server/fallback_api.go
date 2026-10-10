package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type fallbackUser struct {
	ID         string `json:"id"`
	Username   string `json:"username"`
	Password   string `json:"-"`
	FullName   string `json:"full_name"`
	Role       string `json:"role"`
	CustomerID string `json:"customer_id,omitempty"`
}

type fallbackState struct {
	mu          sync.Mutex
	sessions    map[string]fallbackUser
	messages    []map[string]any
	assignments []map[string]any
}

var fb = newFallbackState()

func newFallbackState() *fallbackState {
	s := &fallbackState{sessions: map[string]fallbackUser{}}
	s.load()
	return s
}

func fallbackUsers() []fallbackUser {
	return []fallbackUser{
		{ID: "u-admin", Username: "admin", Password: "EBS2026!", FullName: "Sistem Yöneticisi / Eğitmen", Role: "admin"},
		{ID: "u-gise01", Username: "gise01", Password: "1234", FullName: "Gişe Yetkilisi 1", Role: "teller"},
		{ID: "u-gise02", Username: "gise02", Password: "1234", FullName: "Gişe Yetkilisi 2", Role: "teller"},
		{ID: "u-mt", Username: "musteritemsilci", Password: "1234", FullName: "Müşteri Temsilcisi", Role: "customer_service"},
		{ID: "u-op", Username: "operasyonyetkilisi", Password: "1234", FullName: "Operasyon Yetkilisi", Role: "operations"},
		{ID: "u-kr", Username: "krediyetkilisi", Password: "1234", FullName: "Kredi Yetkilisi", Role: "credit"},
		{ID: "u-sg", Username: "sigortayetkilisi", Password: "1234", FullName: "Sigorta Yetkilisi", Role: "insurance"},
		{ID: "u-hs", Username: "hasaryetkilisi", Password: "1234", FullName: "Hasar Yetkilisi", Role: "claims"},
		{ID: "u-dn", Username: "denetci", Password: "1234", FullName: "Denetçi", Role: "auditor"},
		{ID: "c-1001", Username: "Ayşe Yılmaz", Password: "1234", FullName: "Ayşe Yılmaz", Role: "customer", CustomerID: "1001"},
		{ID: "c-1002", Username: "Mehmet Kaya", Password: "1234", FullName: "Mehmet Kaya", Role: "customer", CustomerID: "1002"},
		{ID: "c-1003", Username: "Zeynep Demir", Password: "1234", FullName: "Zeynep Demir", Role: "customer", CustomerID: "1003"},
	}
}

func fallbackRoles() []map[string]any {
	return []map[string]any{
		{"id": "admin", "name": "Sistem Yöneticisi / Eğitmen", "active": true},
		{"id": "teller", "name": "Gişe Yetkilisi", "active": true},
		{"id": "customer_service", "name": "Müşteri Temsilcisi", "active": true},
		{"id": "operations", "name": "Operasyon Yetkilisi", "active": true},
		{"id": "credit", "name": "Kredi Yetkilisi", "active": true},
		{"id": "insurance", "name": "Sigorta Yetkilisi", "active": true},
		{"id": "claims", "name": "Hasar Yetkilisi", "active": true},
		{"id": "auditor", "name": "Denetçi", "active": true},
		{"id": "customer", "name": "Müşteri", "active": true},
	}
}

func fallbackCustomers() []map[string]any {
	return []map[string]any{
		{"id": "1001", "customer_number": "10010001", "name": "Ayşe", "surname": "Yılmaz", "tckn": "111******11", "phone": "05** *** ** 11", "kyc_status": "Güncel", "segment": "Bireysel"},
		{"id": "1002", "customer_number": "10010002", "name": "Mehmet", "surname": "Kaya", "tckn": "222******22", "phone": "05** *** ** 22", "kyc_status": "Güncel", "segment": "Bireysel"},
		{"id": "1003", "customer_number": "10010003", "name": "Zeynep", "surname": "Demir", "tckn": "333******33", "phone": "05** *** ** 33", "kyc_status": "Belge Kontrolü", "segment": "Bireysel"},
	}
}

func fallbackAccounts() []map[string]any {
	return []map[string]any{
		{"id": "A-1001-TRY", "customer_id": "1001", "iban": "TR00 0000 0000 0000 0000 0000 01", "account_type": "Vadesiz TL", "currency": "TRY", "balance": 48250.75, "available_balance": 48250.75, "status": "active"},
		{"id": "A-1001-USD", "customer_id": "1001", "iban": "TR00 0000 0000 0000 0000 0000 02", "account_type": "Vadesiz USD", "currency": "USD", "balance": 1250.00, "available_balance": 1250.00, "status": "active"},
		{"id": "A-1002-TRY", "customer_id": "1002", "iban": "TR00 0000 0000 0000 0000 0000 03", "account_type": "Vadesiz TL", "currency": "TRY", "balance": 126800.40, "available_balance": 126800.40, "status": "active"},
		{"id": "A-1003-TRY", "customer_id": "1003", "iban": "TR00 0000 0000 0000 0000 0000 04", "account_type": "Vadesiz TL", "currency": "TRY", "balance": 18950.00, "available_balance": 18950.00, "status": "active"},
	}
}

func fallbackCards() []map[string]any {
	return []map[string]any{
		{"id": "K-1001-01", "customer_id": "1001", "card_type": "Kredi Kartı", "masked_card": "5400 **** **** 1026", "limit": 50000, "available_limit": 37200, "status": "active"},
		{"id": "K-1002-01", "customer_id": "1002", "card_type": "Banka Kartı", "masked_card": "4508 **** **** 2044", "limit": 0, "available_limit": 0, "status": "active"},
	}
}

func fallbackLoans() []map[string]any {
	return []map[string]any{
		{"id": "KR-1001-01", "customer_id": "1001", "product": "İhtiyaç Kredisi", "principal": 120000, "remaining_principal": 78500, "installment": 7450, "status": "active"},
		{"id": "KR-1002-01", "customer_id": "1002", "product": "Taşıt Kredisi", "principal": 450000, "remaining_principal": 318000, "installment": 21800, "status": "active"},
	}
}

func fallbackPolicies() []map[string]any {
	return []map[string]any{
		{"id": "P-1001-01", "customer_id": "1001", "policy_number": "POL-2026-001245", "product_name": "Kasko", "branch": "Kara Araçları", "coverage_limit": 500000, "premium": 18650, "status": "active", "period": "2026-2027"},
		{"id": "P-1002-01", "customer_id": "1002", "policy_number": "POL-2026-002116", "product_name": "Konut", "branch": "Yangın", "coverage_limit": 1250000, "premium": 7950, "status": "active", "period": "2026-2027"},
	}
}

func fallbackClaims() []map[string]any {
	return []map[string]any{
		{"id": "H-2026-00077", "customer_id": "1001", "policy_id": "P-1001-01", "claim_type": "Kasko - Çarpma", "loss_date": "2026-09-18", "reserve": 42000, "status": "in_progress"},
	}
}

func fallbackTransfers() []map[string]any {
	return []map[string]any{
		{"id": "TR-2026-01021", "customer_id": "1001", "source_account": "A-1001-TRY", "recipient": "Mehmet Kaya", "amount": 2500, "currency": "TRY", "type": "FAST", "status": "completed"},
	}
}

func fallbackPayments() []map[string]any {
	return []map[string]any{
		{"id": "OD-2026-00441", "customer_id": "1001", "institution": "Elektrik", "amount": 1180.50, "currency": "TRY", "status": "completed"},
	}
}

func fallbackScenarios() []map[string]any {
	return []map[string]any{
		{"id": "S-001", "title": "Gişe: Nakit Yatırma ve Limit Kontrolü", "active": true},
		{"id": "S-002", "title": "Kredi: Başvuru ve Yetkili Onayı", "active": true},
		{"id": "S-003", "title": "Sigorta: Kasko Tekliften Poliçeye", "active": true},
		{"id": "S-004", "title": "Hasar: İhbar, Belge ve Karar Süreci", "active": true},
	}
}

func fallbackDataPath(name string) string { return filepath.Join(programData(), name) }

func (s *fallbackState) load() {
	_ = os.MkdirAll(programData(), 0755)
	for name, dst := range map[string]*[]map[string]any{
		"fallback_messages.json":    &s.messages,
		"fallback_assignments.json": &s.assignments,
	} {
		b, err := os.ReadFile(fallbackDataPath(name))
		if err == nil {
			_ = json.Unmarshal(b, dst)
		}
	}
}

func (s *fallbackState) saveLocked(name string, v any) {
	b, _ := json.MarshalIndent(v, "", "  ")
	_ = os.WriteFile(fallbackDataPath(name), b, 0644)
}

func randomToken() string { return newRef() + "-" + newRef() }

func (s *fallbackState) login(username, password string) (string, fallbackUser, bool) {
	ukey := strings.ToLower(strings.TrimSpace(username))
	for _, u := range fallbackUsers() {
		if strings.ToLower(u.Username) == ukey && u.Password == password {
			tok := randomToken()
			s.mu.Lock()
			s.sessions[tok] = u
			s.mu.Unlock()
			return tok, u, true
		}
	}
	return "", fallbackUser{}, false
}

func bearerToken(r *http.Request) string {
	h := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(h) > 7 && strings.EqualFold(h[:7], "Bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

func (s *fallbackState) session(r *http.Request) (fallbackUser, bool) {
	t := bearerToken(r)
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.sessions[t]
	return u, ok
}

func publicUser(u fallbackUser) map[string]any {
	return map[string]any{"id": u.ID, "username": u.Username, "full_name": u.FullName, "role": u.Role, "active": true}
}

func authResponse(token string, u fallbackUser) map[string]any {
	return map[string]any{
		"token":       token,
		"user":        publicUser(u),
		"permissions": []string{"read", "write"},
		"config":      map[string]any{"branch_code": "1001", "branch_name": "Erbaa Eğitim Şubesi", "business_date": time.Now().Format("2006-01-02")},
	}
}

func filterCustomer(rows []map[string]any, cid string) []map[string]any {
	if cid == "" {
		return rows
	}
	out := make([]map[string]any, 0)
	for _, x := range rows {
		if fmt.Sprint(x["customer_id"]) == cid {
			out = append(out, x)
		}
	}
	return out
}

func fallbackCustomerByID(id string) (map[string]any, bool) {
	for _, c := range fallbackCustomers() {
		if fmt.Sprint(c["id"]) == id {
			return c, true
		}
	}
	return nil, false
}

func fallbackListFor(path string, u fallbackUser) any {
	cid := ""
	if u.Role == "customer" {
		cid = u.CustomerID
	}
	switch path {
	case "/api/users":
		out := []map[string]any{}
		for _, x := range fallbackUsers() {
			if x.Role != "customer" {
				out = append(out, publicUser(x))
			}
		}
		return out
	case "/api/roles":
		return fallbackRoles()
	case "/api/customers":
		return fallbackCustomers()
	case "/api/accounts":
		return filterCustomer(fallbackAccounts(), cid)
	case "/api/cards":
		return filterCustomer(fallbackCards(), cid)
	case "/api/loans":
		return filterCustomer(fallbackLoans(), cid)
	case "/api/policies":
		return filterCustomer(fallbackPolicies(), cid)
	case "/api/claims":
		return filterCustomer(fallbackClaims(), cid)
	case "/api/transfers":
		return filterCustomer(fallbackTransfers(), cid)
	case "/api/payments":
		return filterCustomer(fallbackPayments(), cid)
	case "/api/scenarios":
		return fallbackScenarios()
	case "/api/approvals":
		return []map[string]any{{"id": "ON-001", "customer_id": "1001", "type": "Kredi Limit Onayı", "status": "pending", "approver_id": "u-op"}}
	case "/api/customer-requests":
		return []map[string]any{{"id": "MT-001", "customer_id": "1001", "request_type": "Poliçe güncellemesi", "status": "pending", "assigned_user_id": "u-sg"}}
	case "/api/user-groups":
		return []map[string]any{{"id": "G-001", "name": "Gişe Ekibi"}, {"id": "G-002", "name": "Sigorta Ekibi"}}
	case "/api/message-templates":
		return []map[string]any{{"id": "M-001", "name": "İşlem Kontrolü", "subject": "İşlem kontrolü gerekli"}, {"id": "M-002", "name": "Eksik Belge", "subject": "Eksik belge bildirimi"}}
	case "/api/cashbox":
		return []map[string]any{{"cashbox": "Gişe 1", "currency": "TRY", "balance": 250000, "status": "open"}}
	case "/api/commercial":
		return []map[string]any{}
	case "/api/documents":
		return []map[string]any{{"id": "B-001", "customer_id": "1001", "document_type": "Kasko Poliçesi", "status": "available"}}
	case "/api/access-controls":
		return []map[string]any{}
	case "/api/workstations":
		return []map[string]any{}
	case "/api/audit":
		return []map[string]any{}
	case "/api/ledger":
		return []map[string]any{}
	}
	return nil
}

func (s *fallbackState) apiHandler(legacy http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if p == "/api/public/branding" {
			jsonOut(w, 200, map[string]any{"name": "ERBAA BANKACILIK VE SİGORTACILIK HİZMETLERİ", "product": "BANKACILIK VE SİGORTACILIK EĞİTİM SİMÜLASYONU", "version": "3.0 PRO", "server": "central", "legacy_available": legacyAPIReady()})
			return
		}
		if p == "/api/login" && r.Method == http.MethodPost {
			var in struct{ Username, Password string }
			_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in)
			tok, u, ok := s.login(in.Username, in.Password)
			if !ok {
				jsonOut(w, http.StatusUnauthorized, map[string]string{"message": "Kullanıcı adı veya şifre hatalı."})
				return
			}
			jsonOut(w, 200, authResponse(tok, u))
			return
		}
		u, ok := s.session(r)
		if !ok {
			jsonOut(w, http.StatusUnauthorized, map[string]string{"message": "Oturum bulunamadı veya süresi doldu."})
			return
		}
		if p == "/api/me" {
			jsonOut(w, 200, authResponse(bearerToken(r), u))
			return
		}

		if p == "/api/dashboard" {
			jsonOut(w, 200, map[string]any{"customers": len(fallbackCustomers()), "accounts": len(fallbackAccounts()), "cards": len(fallbackCards()), "loans": len(fallbackLoans()), "policies": len(fallbackPolicies()), "claims": len(fallbackClaims()), "pending_approvals": 1, "server_mode": "v3 merkezi"})
			return
		}
		if p == "/api/settings" {
			jsonOut(w, 200, map[string]any{"branch_code": "1001", "branch_name": "Erbaa Eğitim Şubesi", "currency": "TRY", "server_mode": "v3 merkezi", "legacy_available": legacyAPIReady()})
			return
		}
		if p == "/api/reports/summary" {
			jsonOut(w, 200, map[string]any{"active_customers": 3, "active_loans": 2, "active_policies": 2, "open_claims": 1})
			return
		}

		if p == "/api/customer-portal/summary" {
			cid := u.CustomerID
			if cid == "" {
				cid = "1001"
			}
			c, _ := fallbackCustomerByID(cid)
			jsonOut(w, 200, map[string]any{"customer": c, "accounts": filterCustomer(fallbackAccounts(), cid), "cards": filterCustomer(fallbackCards(), cid), "loans": filterCustomer(fallbackLoans(), cid), "policies": filterCustomer(fallbackPolicies(), cid), "claims": filterCustomer(fallbackClaims(), cid), "transfers": filterCustomer(fallbackTransfers(), cid), "payments": filterCustomer(fallbackPayments(), cid)})
			return
		}

		if strings.HasPrefix(p, "/api/customers/") && r.Method == http.MethodGet {
			id := strings.TrimPrefix(p, "/api/customers/")
			if c, exists := fallbackCustomerByID(id); exists {
				jsonOut(w, 200, map[string]any{"customer": c, "accounts": filterCustomer(fallbackAccounts(), id), "cards": filterCustomer(fallbackCards(), id), "loans": filterCustomer(fallbackLoans(), id), "policies": filterCustomer(fallbackPolicies(), id), "claims": filterCustomer(fallbackClaims(), id), "transfers": filterCustomer(fallbackTransfers(), id), "payments": filterCustomer(fallbackPayments(), id)})
				return
			}
			jsonOut(w, 404, map[string]string{"message": "Müşteri bulunamadı."})
			return
		}

		if p == "/api/customers" && r.Method == http.MethodGet {
			rows := fallbackCustomers()
			q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
			if q != "" {
				out := []map[string]any{}
				for _, c := range rows {
					hay := strings.ToLower(fmt.Sprint(c["name"], " ", c["surname"], " ", c["customer_number"], " ", c["tckn"]))
					if strings.Contains(hay, q) {
						out = append(out, c)
					}
				}
				rows = out
			}
			jsonOut(w, 200, rows)
			return
		}

		if p == "/api/messages" {
			if r.Method == http.MethodGet {
				s.mu.Lock()
				out := append([]map[string]any(nil), s.messages...)
				s.mu.Unlock()
				jsonOut(w, 200, out)
				return
			}
			if r.Method == http.MethodPost {
				var body map[string]any
				_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body)
				s.mu.Lock()
				body["id"] = "MSG-" + strconv.FormatInt(time.Now().UnixNano(), 10)
				body["created_at"] = time.Now().Format(time.RFC3339)
				body["sender"] = u.FullName
				s.messages = append([]map[string]any{body}, s.messages...)
				s.saveLocked("fallback_messages.json", s.messages)
				s.mu.Unlock()
				jsonOut(w, 200, map[string]any{"ok": true, "id": body["id"]})
				return
			}
		}

		if p == "/api/assignments" && r.Method == http.MethodGet {
			s.mu.Lock()
			out := append([]map[string]any(nil), s.assignments...)
			s.mu.Unlock()
			jsonOut(w, 200, out)
			return
		}
		if strings.HasPrefix(p, "/api/scenarios/") && strings.HasSuffix(p, "/assign-bulk") && r.Method == http.MethodPost {
			id := strings.TrimSuffix(strings.TrimPrefix(p, "/api/scenarios/"), "/assign-bulk")
			var body map[string]any
			_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body)
			body["id"] = "ASG-" + strconv.FormatInt(time.Now().UnixNano(), 10)
			body["scenario_id"] = id
			body["status"] = "pending"
			body["created_at"] = time.Now().Format(time.RFC3339)
			assigned := 1
			if xs, ok := body["user_ids"].([]any); ok && len(xs) > 0 {
				assigned = len(xs)
			}
			s.mu.Lock()
			s.assignments = append([]map[string]any{body}, s.assignments...)
			s.saveLocked("fallback_assignments.json", s.assignments)
			s.mu.Unlock()
			jsonOut(w, 200, map[string]any{"ok": true, "assigned": assigned})
			return
		}

		// All list endpoints used by the v3 shell have a built-in central implementation.
		basePath := p
		if i := strings.Index(basePath, "?"); i >= 0 {
			basePath = basePath[:i]
		}
		if v := fallbackListFor(basePath, u); v != nil && r.Method == http.MethodGet {
			jsonOut(w, 200, v)
			return
		}

		// Unknown/legacy API endpoints remain available when the preserved engine is healthy.
		if legacy != nil && legacyAPIReady() {
			legacy.ServeHTTP(w, r)
			return
		}
		jsonOut(w, http.StatusNotImplemented, map[string]string{"message": "Bu işlem eski EBS uyumluluk motoruna aittir. Merkezi v3 Server çalışıyor; ilgili işlem v3 çalışma alanından yürütülmelidir."})
	})
}
