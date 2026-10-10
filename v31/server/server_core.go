package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"ebs-v3-service/internal/sim"
)

// v3 UI is hosted centrally by the Server service. Browser clients never need to
// connect to their own 127.0.0.1 address.
//
//go:embed design_cards.json web/*
var embedded embed.FS

const legacyBase = "http://127.0.0.1:8787"

type operationalAlias struct {
	LegacyUsername string
	PublicUsername string
	FullName       string
}

var operationalLoginAliases = map[string]operationalAlias{
	"gise01":             {LegacyUsername: "ogrenci01", PublicUsername: "gise01", FullName: "Gişe Yetkilisi 1"},
	"gise02":             {LegacyUsername: "ogrenci02", PublicUsername: "gise02", FullName: "Gişe Yetkilisi 2"},
	"operasyonyetkilisi": {LegacyUsername: "supervisor", PublicUsername: "operasyonyetkilisi", FullName: "Operasyon Yetkilisi"},
}

var legacyDisplayAliases = map[string]operationalAlias{
	"ogrenci01":  operationalLoginAliases["gise01"],
	"ogrenci02":  operationalLoginAliases["gise02"],
	"supervisor": operationalLoginAliases["operasyonyetkilisi"],
}

func aliasForLogin(username string) (operationalAlias, bool) {
	a, ok := operationalLoginAliases[strings.ToLower(strings.TrimSpace(username))]
	return a, ok
}

func rewriteOperationalIdentityBytes(b []byte) []byte {
	repls := [][2]string{
		{`"username":"ogrenci01"`, `"username":"gise01"`},
		{`"username":"ogrenci02"`, `"username":"gise02"`},
		{`"username":"supervisor"`, `"username":"operasyonyetkilisi"`},
		{`"full_name":"Öğrenci 01"`, `"full_name":"Gişe Yetkilisi 1"`},
		{`"full_name":"Öğrenci 02"`, `"full_name":"Gişe Yetkilisi 2"`},
		{`"full_name":"Operasyon Yetkilisi / Supervisor"`, `"full_name":"Operasyon Yetkilisi"`},
		{`Öğrenci / Gişe`, `Gişe / Personel`},
		{`Öğrenci / Personel`, `Gişe / Personel`},
		{`Öğrenci 01`, `Gişe Yetkilisi 1`},
		{`Öğrenci 02`, `Gişe Yetkilisi 2`},
		{`ogrenci01`, `gise01`},
		{`ogrenci02`, `gise02`},
	}
	for _, r := range repls {
		b = bytes.ReplaceAll(b, []byte(r[0]), []byte(r[1]))
	}
	return b
}

func legacyAPIReady() bool {
	c := &http.Client{Timeout: 1200 * time.Millisecond}
	resp, err := c.Get(legacyBase + "/api/public/branding")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}

func stopLegacyProcesses() {
	for _, name := range []string{"EBS_Core_Server.exe", "EBS_Server.exe"} {
		_ = exec.Command("taskkill.exe", "/IM", name, "/F").Run()
	}
}

func waitLegacyAPI(d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if legacyAPIReady() {
			return true
		}
		time.Sleep(350 * time.Millisecond)
	}
	return legacyAPIReady()
}

type Event struct {
	Time      time.Time       `json:"time"`
	Reference string          `json:"reference"`
	CardCode  string          `json:"card_code"`
	Action    string          `json:"action"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	User      string          `json:"user"`
	Client    string          `json:"client"`
	Status    string          `json:"status"`
	IdemKey   string          `json:"idempotency_key,omitempty"`
}

type Store struct {
	mu     sync.Mutex
	dir    string
	events []Event
	idem   map[string]string
}

func programData() string {
	p := os.Getenv("ProgramData")
	if p == "" {
		p = os.TempDir()
	}
	return filepath.Join(p, "EBS Bankacilik ve Sigortacilik", "v3pro")
}

func installRoot() string {
	p := os.Getenv("ProgramFiles")
	if p == "" {
		p = `C:\Program Files`
	}
	return filepath.Join(p, "EBS Bankacilik ve Sigortacilik")
}

func newStore() *Store {
	s := &Store{dir: programData(), idem: map[string]string{}}
	_ = os.MkdirAll(s.dir, 0755)
	f, err := os.Open(filepath.Join(s.dir, "events.jsonl"))
	if err == nil {
		defer f.Close()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			var x Event
			if json.Unmarshal(sc.Bytes(), &x) == nil {
				s.events = append(s.events, x)
				if x.IdemKey != "" {
					s.idem[x.IdemKey] = x.Reference
				}
			}
		}
	}
	return s
}

func newRef() string {
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	return fmt.Sprintf("EBS3-%s-%s", time.Now().Format("20060102-150405"), strings.ToUpper(hex.EncodeToString(b)))
}

func (s *Store) add(e Event) (Event, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e.IdemKey != "" {
		if r, ok := s.idem[e.IdemKey]; ok {
			for _, x := range s.events {
				if x.Reference == r {
					return x, true
				}
			}
		}
	}
	e.Time = time.Now()
	e.Reference = newRef()
	e.Status = "KAYDEDİLDİ"
	s.events = append(s.events, e)
	if e.IdemKey != "" {
		s.idem[e.IdemKey] = e.Reference
	}
	if f, err := os.OpenFile(filepath.Join(s.dir, "events.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644); err == nil {
		b, _ := json.Marshal(e)
		_, _ = f.Write(append(b, '\n'))
		_ = f.Close()
	}
	return e, false
}

func (s *Store) card(code string, limit int) []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	out := []Event{}
	for i := len(s.events) - 1; i >= 0 && len(out) < limit; i-- {
		if strings.EqualFold(s.events[i].CardCode, code) {
			out = append(out, s.events[i])
		}
	}
	return out
}

func (s *Store) recent(limit int) []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	start := len(s.events) - limit
	if start < 0 {
		start = 0
	}
	out := append([]Event(nil), s.events[start:]...)
	sort.Slice(out, func(i, j int) bool { return out[i].Time.After(out[j].Time) })
	return out
}

func jsonOut(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func tcpOpen(addr string) bool {
	c, e := net.DialTimeout("tcp", addr, 850*time.Millisecond)
	if e == nil {
		_ = c.Close()
		return true
	}
	return false
}

func startLegacyComponent(name string) {
	p := filepath.Join(installRoot(), "Server", name)
	if _, e := os.Stat(p); e != nil {
		log.Printf("legacy component missing: %s", p)
		return
	}
	cmd := exec.Command(p)
	cmd.Dir = filepath.Dir(p)
	if e := cmd.Start(); e != nil {
		log.Printf("legacy start %s: %v", name, e)
		return
	}
	log.Printf("legacy component started: %s (pid=%d)", name, cmd.Process.Pid)
}

func ensureLegacyServer() bool {
	if legacyAPIReady() {
		return true
	}
	if tcpOpen("127.0.0.1:8787") {
		log.Printf("legacy port is open but core API probe failed; stale/wrong listener will be restarted")
		stopLegacyProcesses()
		time.Sleep(650 * time.Millisecond)
	}

	// Prefer the preserved real core directly. Only fall back to the legacy
	// bootstrap executable if the core itself cannot expose the expected API.
	startLegacyComponent("EBS_Core_Server.exe")
	if waitLegacyAPI(7 * time.Second) {
		return true
	}
	startLegacyComponent("EBS_Server.exe")
	return waitLegacyAPI(10 * time.Second)
}

func superviseLegacy(stop <-chan struct{}) {
	// Immediate first-start recovery.
	ensureLegacyServer()
	t := time.NewTicker(6 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			if !legacyAPIReady() {
				log.Printf("legacy core API offline/incompatible; restart requested")
				ensureLegacyServer()
			}
		}
	}
}

func clientModeFromRequest(r *http.Request) string {
	c := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("client")))
	switch c {
	case "admin", "personnel", "customer":
		return c
	default:
		return "personnel"
	}
}

func v3APIMux(store *Store) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		jsonOut(w, 200, map[string]any{"ok": true, "service": "EBS v3.0 PRO Service", "version": "3.1 STABLE", "legacy_ok": legacyAPIReady(), "api_mode": "central-hybrid", "time": time.Now()})
	})
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		jsonOut(w, 200, map[string]any{"ok": true, "service": "EBS v3.0 PRO Service", "version": "3.1 STABLE", "legacy_ok": legacyAPIReady(), "api_mode": "central-hybrid"})
	})
	mux.HandleFunc("/api/catalog", func(w http.ResponseWriter, r *http.Request) {
		b, _ := embedded.ReadFile("design_cards.json")
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write(b)
	})
	mux.HandleFunc("/api/action", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			jsonOut(w, 405, map[string]string{"message": "Yalnız POST desteklenir."})
			return
		}
		b, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
		if err != nil {
			jsonOut(w, 400, map[string]string{"message": "İstek okunamadı."})
			return
		}
		var in struct {
			CardCode string          `json:"card_code"`
			Action   string          `json:"action"`
			Payload  json.RawMessage `json:"payload"`
			Client   string          `json:"client"`
		}
		if json.Unmarshal(b, &in) != nil || strings.TrimSpace(in.CardCode) == "" || strings.TrimSpace(in.Action) == "" {
			jsonOut(w, 400, map[string]string{"message": "Kart kodu ve aksiyon zorunludur."})
			return
		}
		e, dup := store.add(Event{CardCode: in.CardCode, Action: in.Action, Payload: in.Payload, Client: in.Client, User: r.Header.Get("X-EBS-User"), IdemKey: r.Header.Get("Idempotency-Key")})
		jsonOut(w, 200, map[string]any{"ok": true, "reference": e.Reference, "status": e.Status, "duplicate": dup})
	})
	mux.HandleFunc("/api/card/", func(w http.ResponseWriter, r *http.Request) {
		code := strings.TrimPrefix(r.URL.Path, "/api/card/")
		jsonOut(w, 200, map[string]any{"events": store.card(code, 50)})
	})
	mux.HandleFunc("/api/events", func(w http.ResponseWriter, r *http.Request) {
		jsonOut(w, 200, map[string]any{"events": store.recent(100)})
	})
	return mux
}

func legacyProxy() *httputil.ReverseProxy {
	target, _ := url.Parse(legacyBase)
	rp := httputil.NewSingleHostReverseProxy(target)
	orig := rp.Director
	rp.Director = func(r *http.Request) {
		// Operational login aliases keep training status out of the bank role identity.
		// Example: gise01 authenticates against the preserved legacy ogrenci01 account,
		// while the user sees only the operational Gişe Yetkilisi identity.
		if r.URL.Path == "/api/login" && r.Method == http.MethodPost && r.Body != nil {
			if body, err := io.ReadAll(r.Body); err == nil {
				var in map[string]any
				if json.Unmarshal(body, &in) == nil {
					if u, _ := in["username"].(string); u != "" {
						if a, ok := aliasForLogin(u); ok {
							in["username"] = a.LegacyUsername
							r.Header.Set("X-EBS-Operational-Alias", a.PublicUsername)
							if nb, e := json.Marshal(in); e == nil {
								r.Body = io.NopCloser(bytes.NewReader(nb))
								r.ContentLength = int64(len(nb))
								r.Header.Set("Content-Length", fmt.Sprint(len(nb)))
							}
						}
					}
				}
			}
		}
		token := r.URL.Query().Get("v3token")
		page := r.URL.Query().Get("v3page")
		clientMode := r.URL.Query().Get("client_mode")
		q := r.URL.Query()
		q.Del("v3token")
		q.Del("v3page")
		q.Del("client_mode")
		r.URL.RawQuery = q.Encode()

		if strings.HasPrefix(r.URL.Path, "/legacy") {
			r.URL.Path = strings.TrimPrefix(r.URL.Path, "/legacy")
			if r.URL.Path == "" {
				r.URL.Path = "/"
			}
		}
		orig(r)
		r.Host = target.Host
		r.Header.Set("Accept-Encoding", "identity")
		if clientMode == "customer" || clientMode == "staff" {
			r.Header.Set("X-EBS-Client-Mode", clientMode)
		}
		if token != "" {
			r.Header.Set("X-EBS-Bridge-Token", token)
		}
		if page != "" {
			r.Header.Set("X-EBS-Bridge-Page", page)
		}
	}
	rp.ModifyResponse = func(resp *http.Response) error {
		resp.Header.Del("X-Frame-Options")
		// Some older preserved EBS builds do not expose optional list endpoints.
		// A missing optional list must not collapse the whole screen with a raw 404.
		if resp.StatusCode == http.StatusNotFound && resp.Request.Method == http.MethodGet {
			switch resp.Request.URL.Path {
			case "/api/assignments", "/api/user-groups", "/api/message-templates", "/api/customer-requests", "/api/access-controls", "/api/workstations":
				_ = resp.Body.Close()
				b := []byte("[]")
				resp.StatusCode = http.StatusOK
				resp.Status = "200 OK"
				resp.Body = io.NopCloser(bytes.NewReader(b))
				resp.ContentLength = int64(len(b))
				resp.Header.Set("Content-Type", "application/json; charset=utf-8")
				resp.Header.Set("Content-Length", fmt.Sprint(len(b)))
			}
		}
		resp.Header.Del("Content-Security-Policy")
		if loc := resp.Header.Get("Location"); loc != "" {
			if u, err := url.Parse(loc); err == nil {
				h := strings.ToLower(u.Hostname())
				if h == "127.0.0.1" || h == "localhost" {
					if u.Path == "" {
						u.Path = "/"
					}
					resp.Header.Set("Location", u.RequestURI())
				}
			}
		}
		ct := strings.ToLower(resp.Header.Get("Content-Type"))
		isText := strings.Contains(ct, "text/html") || strings.Contains(ct, "javascript") || strings.Contains(ct, "text/css") || strings.Contains(ct, "application/json") || strings.Contains(ct, "text/plain")
		if !isText {
			return nil
		}
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		_ = resp.Body.Close()
		b = rewriteOperationalIdentityBytes(b)
		for _, dead := range [][]byte{
			[]byte("http://127.0.0.1:8787"),
			[]byte("http://localhost:8787"),
			[]byte("https://127.0.0.1:8787"),
			[]byte("https://localhost:8787"),
		} {
			b = bytes.ReplaceAll(b, dead, []byte(""))
		}
		if strings.Contains(ct, "text/html") {
			token := resp.Request.Header.Get("X-EBS-Bridge-Token")
			page := resp.Request.Header.Get("X-EBS-Bridge-Page")
			js := fmt.Sprintf(`<script>(function(){try{var t=%q;if(t){localStorage.setItem('ebs_token',t);localStorage.setItem('ebs_customer_token',t);}var p=%q;window.__EBS_REQUESTED_PAGE=p;setTimeout(function(){try{if(p&&typeof window.go==='function'){window.go(p)}else if(p&&window.S&&typeof window.renderPage==='function'){S.page=p;window.renderNav&&renderNav();renderPage(p)}}catch(e){}},900);}catch(e){}})();</script>`, token, page)
			lower := bytes.ToLower(b)
			if i := bytes.Index(lower, []byte("<head>")); i >= 0 {
				i += len("<head>")
				b = append(append(append([]byte{}, b[:i]...), []byte(js)...), b[i:]...)
			}
		}
		resp.Body = io.NopCloser(bytes.NewReader(b))
		resp.ContentLength = int64(len(b))
		resp.Header.Set("Content-Length", fmt.Sprint(len(b)))
		resp.Header.Del("Content-Encoding")
		return nil
	}
	rp.ErrorHandler = func(w http.ResponseWriter, r *http.Request, e error) {
		w.Header().Set("Cache-Control", "no-store")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusBadGateway)
			_, _ = fmt.Fprintf(w, `{"error":"Merkezi EBS işlem motoruna ulaşılamadı. Server servisinin yeniden başlatılması deneniyor.","detail":%q}`, e.Error())
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = fmt.Fprint(w, `<!doctype html><meta charset="utf-8"><style>body{font-family:Segoe UI,Arial;background:#f8fafc;color:#0f172a;padding:40px}.box{max-width:760px;margin:auto;background:#fff;border:1px solid #cbd5e1;border-radius:14px;padding:28px}h2{margin-top:0}</style><div class="box"><h2>EBS işlem motoru geçici olarak erişilemiyor</h2><p>Programın merkezi ekranı çalışıyor; ancak eski EBS işlem motoru henüz hazır değil. Server bileşeni otomatik yeniden başlatılıyor. Birkaç saniye sonra bu ekranı yeniden açın.</p><p>Bu sürüm tarayıcıyı istemci bilgisayarındaki <b>127.0.0.1</b> adresine yönlendirmez.</p></div>`)
	}
	return rp
}

func localIPFor(remote *net.UDPAddr) string {
	c, err := net.DialUDP("udp4", nil, remote)
	if err == nil {
		defer c.Close()
		if a, ok := c.LocalAddr().(*net.UDPAddr); ok && a.IP != nil && !a.IP.IsLoopback() {
			return a.IP.String()
		}
	}
	ifaces, _ := net.Interfaces()
	for _, it := range ifaces {
		addrs, _ := it.Addrs()
		for _, a := range addrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip4 := ip.To4(); ip4 != nil && !ip4.IsLoopback() {
				return ip4.String()
			}
		}
	}
	return "127.0.0.1"
}

func discoveryResponder(stop <-chan struct{}) {
	addr := &net.UDPAddr{IP: net.IPv4zero, Port: 8793}
	conn, err := net.ListenUDP("udp4", addr)
	if err != nil {
		log.Printf("v3 discovery bind: %v", err)
		return
	}
	defer conn.Close()
	buf := make([]byte, 2048)
	for {
		_ = conn.SetReadDeadline(time.Now().Add(1 * time.Second))
		n, remote, err := conn.ReadFromUDP(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				select {
				case <-stop:
					return
				default:
					continue
				}
			}
			continue
		}
		if strings.Contains(strings.ToUpper(string(buf[:n])), "EBS_V3_DISCOVER") {
			ip := localIPFor(remote)
			msg := []byte("EBS_V3_URL http://" + net.JoinHostPort(ip, "8792"))
			_, _ = conn.WriteToUDP(msg, remote)
		}
	}
}

func newHTTPServer(stop <-chan struct{}) *http.Server {
	store := newStore()
	v3mux := v3APIMux(store)
	// The durable simulator engine is initialized once for the central service.
	// The preserved v3 event API remains mounted for compatibility, while new
	// clients use the authenticated transactional /v3api/api/v3 endpoints.
	if engine, err := sim.Open(filepath.Join(programData(), "ebs-v31.sqlite3")); err != nil {
		log.Printf("v3 simulation engine unavailable: %v", err)
	} else {
		installSimulationAPI(v3mux, engine)
	}
	legacy := legacyProxy()
	webFS, _ := fs.Sub(embedded, "web")
	files := http.FileServer(http.FS(webFS))

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		jsonOut(w, 200, map[string]any{"ok": true, "service": "EBS v3.0 PRO Service", "version": "3.1 STABLE", "legacy_ok": legacyAPIReady(), "api_mode": "central-hybrid"})
	})
	mux.HandleFunc("/runtime.json", func(w http.ResponseWriter, r *http.Request) {
		jsonOut(w, 200, map[string]any{
			"client":       clientModeFromRequest(r),
			"version":      "3.1 STABLE",
			"full_name":    "ERBAA BANKACILIK VE SİGORTACILIK HİZMETLERİ",
			"product_name": "BANKACILIK VE SİGORTACILIK EĞİTİM SİMÜLASYONU",
			"upstream":     "merkezi v3 işlem motoru",
			"upstream_ok":  true,
			"legacy_ok":    legacyAPIReady(),
			"v3":           "merkezi v3 servis",
			"v3_ok":        true,
			"legacy_url":   "/legacy/",
		})
	})
	mux.HandleFunc("/design_cards.json", func(w http.ResponseWriter, r *http.Request) {
		b, _ := embedded.ReadFile("web/design_cards.json")
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(b)
	})
	mux.HandleFunc("/styles.css", func(w http.ResponseWriter, r *http.Request) {
		b, _ := embedded.ReadFile("web/styles.css")
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		_, _ = w.Write(b)
	})
	mux.HandleFunc("/app.js", func(w http.ResponseWriter, r *http.Request) {
		b, _ := embedded.ReadFile("web/app.js")
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(b)
	})
	mux.HandleFunc("/icon.ico", func(w http.ResponseWriter, r *http.Request) {
		c := clientModeFromRequest(r)
		name := "web/EBS_Personel.ico"
		if c == "admin" {
			name = "web/EBS_Admin.ico"
		} else if c == "customer" {
			name = "web/EBS_Musteri.ico"
		}
		b, e := embedded.ReadFile(name)
		if e != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/x-icon")
		_, _ = w.Write(b)
	})
	mux.HandleFunc("/ui", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/ui/?"+r.URL.RawQuery, http.StatusFound)
	})
	mux.Handle("/ui/", http.StripPrefix("/ui/", files))

	// v3-only events/actions.
	mux.Handle("/v3api/", http.StripPrefix("/v3api", v3mux))

	// The preserved v1 core remains the real banking/sigorta transaction source.
	// It is proxied by the central Server so remote clients never see 127.0.0.1.
	mux.Handle("/api/", fb.apiHandler(legacy))
	mux.Handle("/legacy/", legacy)
	mux.Handle("/web/", legacy)

	// Any legacy route not owned above is still proxied, while bare root opens v3.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/ui/", http.StatusFound)
			return
		}
		legacy.ServeHTTP(w, r)
	})

	addr := os.Getenv("EBS_V3_LISTEN")
	if addr == "" {
		addr = ":8792"
	}
	logPath := filepath.Join(programData(), "service.log")
	_ = os.MkdirAll(filepath.Dir(logPath), 0755)
	if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644); err == nil {
		log.SetOutput(f)
	}
	go superviseLegacy(stop)
	go discoveryResponder(stop)
	return &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 120 * time.Second}
}

func runServer(stop <-chan struct{}) error {
	srv := newHTTPServer(stop)
	errCh := make(chan error, 1)
	go func() {
		err := srv.ListenAndServe()
		if err != nil && err != http.ErrServerClosed {
			errCh <- err
		} else {
			errCh <- nil
		}
	}()
	select {
	case <-stop:
		ctx, cancel := context.WithTimeout(context.Background(), 7*time.Second)
		defer cancel()
		return srv.Shutdown(ctx)
	case err := <-errCh:
		return err
	}
}
