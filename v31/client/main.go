//go:build windows

package main

import (
	"encoding/json"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

var clientMode = "personnel" // admin | personnel | customer

// Keep the last health failure so a discovery/startup problem is actionable
// instead of always being shown as a generic "Server bulunamadı" message.
var resolveDetail string

const fullName = "ERBAA BANKACILIK VE SİGORTACILIK HİZMETLERİ"
const productName = "BANKACILIK VE SİGORTACILIK EĞİTİM SİMÜLASYONU"

func u16(s string) *uint16 { p, _ := syscall.UTF16PtrFromString(s); return p }

func messageBox(title, text string, flags uintptr) {
	p := syscall.NewLazyDLL("user32.dll").NewProc("MessageBoxW")
	p.Call(0, uintptr(unsafe.Pointer(u16(text))), uintptr(unsafe.Pointer(u16(title))), flags)
}

func programDataDir() string {
	p := os.Getenv("ProgramData")
	if p == "" {
		p = os.TempDir()
	}
	return filepath.Join(p, "EBS Bankacilik ve Sigortacilik")
}

func installRoot() string {
	p := os.Getenv("ProgramFiles")
	if p == "" {
		p = `C:\Program Files`
	}
	return filepath.Join(p, "EBS Bankacilik ve Sigortacilik")
}

var urlRe = regexp.MustCompile(`https?://[^\s\x00\r\n]+`)

func trimURL(s string) string {
	return strings.TrimRight(strings.TrimSpace(s), `/"',;}`)
}

func readURLFile(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	if m := urlRe.Find(b); len(m) > 0 {
		return trimURL(string(m))
	}
	var x map[string]any
	if json.Unmarshal(b, &x) == nil {
		for _, k := range []string{"v3_url", "server_url", "url", "public_url"} {
			if s, ok := x[k].(string); ok && strings.HasPrefix(strings.ToLower(s), "http") {
				return trimURL(s)
			}
		}
	}
	return ""
}

func userConfigDir() string {
	p := os.Getenv("LOCALAPPDATA")
	if p == "" {
		if profile := os.Getenv("USERPROFILE"); profile != "" {
			p = filepath.Join(profile, "AppData", "Local")
		}
	}
	if p == "" {
		return ""
	}
	return filepath.Join(p, "EBS Bankacilik ve Sigortacilik")
}

func savedV3URL() string {
	paths := []string{filepath.Join(programDataDir(), "v3_server_url.txt"), filepath.Join(programDataDir(), "server_v3_url.txt")}
	if user := userConfigDir(); user != "" {
		paths = append([]string{filepath.Join(user, "v3_server_url.txt"), filepath.Join(user, "server_v3_url.txt")}, paths...)
	}
	for _, p := range paths {
		if s := readURLFile(p); s != "" {
			if u, e := url.Parse(s); e == nil && u.Port() == "8792" {
				return trimURL(s)
			}
			return deriveV3(s)
		}
	}
	return ""
}

func legacySavedURL() string {
	for _, p := range []string{
		filepath.Join(programDataDir(), "EBS_Server_Baglantisi.ebs"),
		filepath.Join(programDataDir(), "server_url.txt"),
		filepath.Join(programDataDir(), "server_config.json"),
	} {
		if s := readURLFile(p); s != "" {
			return s
		}
	}
	return ""
}

func saveV3URL(s string) {
	if s == "" {
		return
	}
	// A Personel/Müşteri user generally cannot modify installer-owned ProgramData.
	// Store their selected server in a per-user directory, retain machine fallback.
	directories := []string{}
	if user := userConfigDir(); user != "" {
		directories = append(directories, user)
	}
	directories = append(directories, programDataDir())
	var last error
	for _, directory := range directories {
		if last = os.MkdirAll(directory, 0755); last != nil {
			continue
		}
		if last = os.WriteFile(filepath.Join(directory, "v3_server_url.txt"), []byte(trimURL(s)+"\r\n"), 0644); last == nil {
			return
		}
	}
	rememberHealthFailure(s, last)
}

func deriveV3(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return ""
	}
	host := u.Hostname()
	if host == "127.0.0.1" && clientMode != "admin" {
		return ""
	}
	return "http://" + net.JoinHostPort(host, "8792")
}

func waitHealth(base string, d time.Duration) bool {
	ok, _ := waitHealthReason(base, d)
	return ok
}

func waitHealthReason(base string, d time.Duration) (bool, error) {
	deadline := time.Now().Add(d)
	var last error
	for time.Now().Before(deadline) {
		if err := checkHealth(base); err == nil {
			return true, nil
		} else {
			last = err
		}
		time.Sleep(350 * time.Millisecond)
	}
	if err := checkHealth(base); err == nil {
		return true, nil
	} else if last != nil {
		return false, last
	} else {
		return false, err
	}
}

func rememberHealthFailure(base string, err error) {
	if err != nil && strings.TrimSpace(base) != "" {
		resolveDetail = base + " — " + healthErrorText(err)
	}
}

func localServerURL() string {
	// Prefer a real LAN address so the browser never depends on a loopback page.
	ifaces, _ := net.Interfaces()
	var fallback string
	for _, it := range ifaces {
		if it.Flags&net.FlagUp == 0 || it.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := it.Addrs()
		for _, a := range addrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			ip4 := ip.To4()
			if ip4 == nil || ip4.IsLoopback() || ip4[0] == 169 && ip4[1] == 254 {
				continue
			}
			u := "http://" + net.JoinHostPort(ip4.String(), "8792")
			if ip4[0] == 10 || ip4[0] == 192 && ip4[1] == 168 || ip4[0] == 172 && ip4[1] >= 16 && ip4[1] <= 31 {
				return u
			}
			if fallback == "" {
				fallback = u
			}
		}
	}
	if fallback != "" {
		return fallback
	}
	return "http://localhost:8792"
}

func startIfExists(path string) {
	if _, err := os.Stat(path); err == nil {
		cmd := exec.Command(path)
		cmd.Dir = filepath.Dir(path)
		_ = cmd.Start()
	}
}

func discover(port int, probe string, timeout time.Duration) string {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		return ""
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	_ = conn.SetWriteBuffer(2048)
	_ = conn.SetReadBuffer(4096)
	_, _ = conn.WriteToUDP([]byte(probe), &net.UDPAddr{IP: net.IPv4bcast, Port: port})
	buf := make([]byte, 4096)
	for {
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			return ""
		}
		msg := strings.TrimSpace(string(buf[:n]))
		if m := urlRe.FindString(msg); m != "" {
			return trimURL(m)
		}
	}
}

func normalizeServerInput(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return ""
	}
	host := u.Hostname()
	if clientMode != "admin" && (host == "127.0.0.1" || strings.EqualFold(host, "localhost")) {
		return ""
	}
	return "http://" + net.JoinHostPort(host, "8792")
}

func promptServerAddress() string {
	if clientMode == "admin" {
		return ""
	}
	script := `$ErrorActionPreference='SilentlyContinue'; Add-Type -AssemblyName Microsoft.VisualBasic; $x=[Microsoft.VisualBasic.Interaction]::InputBox('EBS Server otomatik bulunamadı. Server bilgisayarının IP adresini veya bilgisayar adını girin. Örnek: 192.168.1.25','EBS Server Bağlantısı',''); if($x){[Console]::OutputEncoding=[System.Text.Encoding]::UTF8; Write-Output $x}`
	out, err := exec.Command("powershell.exe", "-NoProfile", "-STA", "-ExecutionPolicy", "Bypass", "-Command", script).Output()
	if err != nil {
		return ""
	}
	candidate := normalizeServerInput(strings.TrimSpace(string(out)))
	if candidate != "" && waitHealth(candidate, 7*time.Second) {
		saveV3URL(candidate)
		return candidate
	}
	if candidate != "" {
		detail := ""
		if err := checkHealth(candidate); err != nil {
			rememberHealthFailure(candidate, err)
			detail = "\n\nDoğrulama: " + err.Error()
		}
		messageBox(fullName+" — Server Bağlantısı", "Girilen Server adresi doğrulanamadı:\n"+candidate+detail+"\n\nServer bilgisayarında güncel EBS_Admin_Installer.exe kurulumunu tamamlayın ve TCP 8792 portunu kontrol edin.", 0x10)
	}
	return ""
}

func resolveV3URL() string {
	resolveDetail = ""
	if s := os.Getenv("EBS_V3_URL"); s != "" {
		s = trimURL(s)
		if waitHealth(s, 5*time.Second) {
			saveV3URL(s)
			return s
		}
		if err := checkHealth(s); err != nil {
			rememberHealthFailure(s, err)
		}
	}

	ensureAdminServices()
	if clientMode == "admin" {
		localHealth := "http://localhost:8792"
		if healthOK(localHealth) {
			publicLocal := localServerURL()
			if healthOK(publicLocal) {
				saveV3URL(publicLocal)
				return publicLocal
			}
			saveV3URL(localHealth)
			return localHealth
		}
		if resolveDetail == "" {
			rememberHealthFailure(localHealth, checkHealth(localHealth))
		}
		return ""
	}

	if s := savedV3URL(); s != "" {
		if waitHealth(s, 6*time.Second) {
			return s
		}
		if err := checkHealth(s); err != nil {
			rememberHealthFailure(s, err)
		}
	}
	if s := legacySavedURL(); s != "" {
		if v := deriveV3(s); v != "" {
			if waitHealth(v, 6*time.Second) {
				saveV3URL(v)
				return v
			}
			if err := checkHealth(v); err != nil {
				rememberHealthFailure(v, err)
			}
		}
	}

	// Aynı bilgisayarda Server çalışıyorsa rol ne olursa olsun doğrudan kullan.
	// Bu deneme uzak istemcide yalnız kısa bir timeout maliyetine sahiptir ve
	// Admin ile birlikte kurulan Personel/Müşteri Client'ların bağlantısını kararlı yapar.
	local := "http://localhost:8792"
	if waitHealth(local, 3*time.Second) {
		saveV3URL(local)
		return local
	}
	if err := checkHealth(local); err != nil {
		rememberHealthFailure(local, err)
	}

	// New v3 discovery channel.
	if s := discover(8793, "EBS_V3_DISCOVER", 3500*time.Millisecond); s != "" {
		// Discovery starts before SQLite schema initialization. Wait for this
		// same candidate instead of rejecting a valid server during boot.
		if waitHealth(s, 7*time.Second) {
			saveV3URL(s)
			return s
		}
		if err := checkHealth(s); err != nil {
			rememberHealthFailure(s, err)
		}
	}

	// Compatibility: discover the old 8787 gateway and convert its host to the v3 service.
	if s := discover(8788, "EBS_DISCOVER", 2500*time.Millisecond); s != "" {
		if v := deriveV3(s); v != "" {
			if waitHealth(v, 6*time.Second) {
				saveV3URL(v)
				return v
			}
			if err := checkHealth(v); err != nil {
				rememberHealthFailure(v, err)
			}
		}
	}
	if s := promptServerAddress(); s != "" {
		return s
	}
	return ""
}

func edgePath() string {
	candidates := []string{
		filepath.Join(os.Getenv("ProgramFiles(x86)"), "Microsoft", "Edge", "Application", "msedge.exe"),
		filepath.Join(os.Getenv("ProgramFiles"), "Microsoft", "Edge", "Application", "msedge.exe"),
	}
	for _, p := range candidates {
		if p != "" {
			if _, e := os.Stat(p); e == nil {
				return p
			}
		}
	}
	if p, e := exec.LookPath("msedge.exe"); e == nil {
		return p
	}
	return ""
}

func openApp(base string) error {
	mode := url.QueryEscape(clientMode)
	appURL := trimURL(base) + "/ui/?client=" + mode
	if p := edgePath(); p != "" {
		return exec.Command(p, "--app="+appURL, "--start-maximized", "--no-first-run", "--disable-features=msEdgeSidebarV2").Start()
	}
	return exec.Command("cmd.exe", "/c", "start", "", appURL).Start()
}

func main() {
	base := resolveV3URL()
	if base == "" {
		role := map[string]string{"admin": "Admin", "personnel": "Personel", "customer": "Müşteri"}[clientMode]
		detail := ""
		if clientMode == "admin" {
			detail = "\n\nAdmin bilgisayarında güncel EBS Server servisinin çalışması ve işlem motorunun hazır olması gerekir. Kurulum paketindeki “EBS_Admin_Installer.exe” dosyasını yönetici olarak çalıştırın."
			if err := checkHealth("http://localhost:8792"); err != nil {
				detail += "\n\nDoğrulama: " + err.Error()
			}
		} else {
			detail = "\n\nBu bilgisayar Server bilgisayarı değildir. Aynı yerel ağa bağlı olduğunuzu ve Server bilgisayarında Admin tam kurulumunun yapıldığını kontrol edin. Program artık bağlantı bulunamadığında 127.0.0.1 adresini açmaz."
		}
		if resolveDetail != "" {
			detail += "\n\nSon doğrulama: " + resolveDetail
		}
		messageBox(fullName+" — "+role, "EBS merkezi Server bulunamadı."+detail, 0x10)
		return
	}
	if err := openApp(base); err != nil {
		messageBox(fullName, "Program ekranı açılamadı:\n"+err.Error()+"\n\nServer: "+base, 0x10)
	}
}
