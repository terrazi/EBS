//go:build windows

package main

import (
	"embed"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows/svc/mgr"
)

var installerRole = "admin" // admin | personnel | customer

func hasArgument(want string) bool {
	for _, arg := range os.Args[1:] {
		if arg == want {
			return true
		}
	}
	return false
}

var unattended = hasArgument("--install-unattended")

//go:embed payload/*
var payload embed.FS

const fullName = "ERBAA BANKACILIK VE SİGORTACILIK HİZMETLERİ"
const productName = "BANKACILIK VE SİGORTACILIK EĞİTİM SİMÜLASYONU"

func u16(s string) *uint16 { p, _ := syscall.UTF16PtrFromString(s); return p }
func msg(title, text string, flags uintptr) {
	if unattended {
		fmt.Fprintln(os.Stdout, text)
		if flags&0x10 != 0 {
			os.Exit(1)
		}
		return
	}
	p := syscall.NewLazyDLL("user32.dll").NewProc("MessageBoxW")
	p.Call(0, uintptr(unsafe.Pointer(u16(text))), uintptr(unsafe.Pointer(u16(title))), flags)
}
func isAdmin() bool {
	p := syscall.NewLazyDLL("shell32.dll").NewProc("IsUserAnAdmin")
	r, _, _ := p.Call()
	return r != 0
}
func elevate() bool {
	exe, _ := os.Executable()
	p := syscall.NewLazyDLL("shell32.dll").NewProc("ShellExecuteW")
	r, _, _ := p.Call(0, uintptr(unsafe.Pointer(u16("runas"))), uintptr(unsafe.Pointer(u16(exe))), 0, 0, 1)
	return r > 32
}
func programFiles() string {
	if p := os.Getenv("ProgramFiles"); p != "" {
		return p
	}
	return `C:\Program Files`
}
func rootDir() string { return filepath.Join(programFiles(), "EBS Bankacilik ve Sigortacilik") }
func dataDir() string {
	if p := os.Getenv("ProgramData"); p != "" {
		return filepath.Join(p, "EBS Bankacilik ve Sigortacilik")
	}
	return `C:\ProgramData\EBS Bankacilik ve Sigortacilik`
}
func desktop() string {
	if p := os.Getenv("PUBLIC"); p != "" {
		return filepath.Join(p, "Desktop")
	}
	return filepath.Join(os.Getenv("USERPROFILE"), "Desktop")
}
func startMenu() string {
	return filepath.Join(os.Getenv("ProgramData"), `Microsoft\Windows\Start Menu\Programs\EBS Bankacilik ve Sigortacilik`)
}
func startupDir() string {
	return filepath.Join(os.Getenv("ProgramData"), `Microsoft\Windows\Start Menu\Programs\Startup`)
}
func logFile() string { return filepath.Join(dataDir(), "logs", "EBS_v3_1_Kurulum.log") }
func logf(f string, a ...any) {
	line := time.Now().Format("2006-01-02 15:04:05") + "  " + fmt.Sprintf(f, a...) + "\r\n"
	_ = os.MkdirAll(filepath.Dir(logFile()), 0755)
	if h, e := os.OpenFile(logFile(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644); e == nil {
		_, _ = h.WriteString(line)
		_ = h.Close()
	}
}
func exists(p string) bool { _, e := os.Stat(p); return e == nil }

func portInUse(addr string) bool {
	c, err := net.DialTimeout("tcp", addr, 450*time.Millisecond)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}
func run(name string, args ...string) error {
	logf("RUN %s %s", name, strings.Join(args, " "))
	c := exec.Command(name, args...)
	out, e := c.CombinedOutput()
	if len(out) > 0 {
		logf("OUT %s", strings.TrimSpace(string(out)))
	}
	if e != nil {
		logf("ERR %v", e)
	}
	if e != nil {
		return fmt.Errorf("%s: %w; %s", name, e, strings.TrimSpace(string(out)))
	}
	return nil
}
func psQuote(s string) string { return strings.ReplaceAll(s, "'", "''") }
func powershell(script string) error {
	c := exec.Command("powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", "$ErrorActionPreference='Stop'; "+script)
	out, err := c.CombinedOutput()
	if err != nil {
		return fmt.Errorf("PowerShell: %w; %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
func shortcut(path, target, icon, workdir, desc string) error {
	if unattended && hasArgument("--test-skip-shortcuts") {
		logf("TEST ONLY: kısayol doğrulaması açık test bayrağıyla atlandı: %s", path)
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	sc := fmt.Sprintf(`$w=New-Object -ComObject WScript.Shell;$s=$w.CreateShortcut('%s');$s.TargetPath='%s';$s.WorkingDirectory='%s';$s.IconLocation='%s,0';$s.Description='%s';$s.Save()`, psQuote(path), psQuote(target), psQuote(workdir), psQuote(icon), psQuote(desc))
	if err := powershell(sc); err != nil {
		return fmt.Errorf("kısayol oluşturulamadı (%s): %w", path, err)
	}
	if !exists(path) {
		return fmt.Errorf("kısayol kaydedilmedi: %s", path)
	}
	return nil
}
func readPayload(name string) ([]byte, error) { return payload.ReadFile("payload/" + name) }
func writePayload(name, dst string) error {
	b, e := readPayload(name)
	if e != nil {
		return e
	}
	if e = os.MkdirAll(filepath.Dir(dst), 0755); e != nil {
		return e
	}
	tmp := dst + ".new"
	if e = os.WriteFile(tmp, b, 0755); e != nil {
		return e
	}
	if e = os.Remove(dst); e != nil && !os.IsNotExist(e) {
		return fmt.Errorf("önceki kurulum dosyası değiştirilemedi (%s): %w", dst, e)
	}
	return os.Rename(tmp, dst)
}
func copyFile(src, dst string) error {
	in, e := os.Open(src)
	if e != nil {
		return e
	}
	defer in.Close()
	if e = os.MkdirAll(filepath.Dir(dst), 0755); e != nil {
		return e
	}
	out, e := os.Create(dst)
	if e != nil {
		return e
	}
	_, e = io.Copy(out, in)
	ce := out.Close()
	if e != nil {
		return e
	}
	return ce
}

func preserveLegacy(dst, role string) {
	if !exists(dst) {
		return
	}
	legacy := filepath.Join(rootDir(), "Legacy", role+"_v1.exe")
	if exists(legacy) {
		return
	}
	if e := copyFile(dst, legacy); e == nil {
		logf("Mevcut %s istemcisi Legacy klasöründe korundu", role)
	}
}

func installIcons() error {
	d := filepath.Join(rootDir(), "Icons")
	for _, n := range []string{"EBS_Admin.ico", "EBS_Personel.ico", "EBS_Musteri.ico", "EBS_Server.ico"} {
		if e := writePayload(n, filepath.Join(d, n)); e != nil {
			return e
		}
	}
	return nil
}

func installClient(role string) (string, error) {
	var dir, name, payloadName, iconName, label string
	switch role {
	case "admin":
		dir = filepath.Join(rootDir(), "Admin")
		name = "EBS_Admin.exe"
		payloadName = "EBS_Admin.exe"
		iconName = "EBS_Admin.ico"
		label = "Admin"
	case "personnel":
		dir = filepath.Join(rootDir(), "Personel")
		name = "EBS_Personel.exe"
		payloadName = "EBS_Personel.exe"
		iconName = "EBS_Personel.ico"
		label = "Personel"
	default:
		dir = filepath.Join(rootDir(), "Musteri")
		name = "EBS_Musteri.exe"
		payloadName = "EBS_Musteri.exe"
		iconName = "EBS_Musteri.ico"
		label = "Müşteri"
	}
	dst := filepath.Join(dir, name)
	if e := stopInstalledExe(dst); e != nil {
		return "", fmt.Errorf("eski %s istemcisi kapatılamadı: %w", label, e)
	}
	preserveLegacy(dst, strings.TrimSuffix(name, ".exe"))
	if e := writePayload(payloadName, dst); e != nil {
		return "", e
	}
	ico := filepath.Join(rootDir(), "Icons", iconName)
	link := fullName + " - " + label + ".lnk"
	if err := shortcut(filepath.Join(desktop(), link), dst, ico, dir, fullName+" — "+label); err != nil {
		return "", err
	}
	if err := shortcut(filepath.Join(startMenu(), link), dst, ico, dir, fullName+" — "+label); err != nil {
		return "", err
	}
	return dst, nil
}

func waitHealth(base string, d time.Duration) bool {
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		if healthOK(base) {
			return true
		}
		time.Sleep(400 * time.Millisecond)
	}
	return healthOK(base)
}
func start(path string, args ...string) {
	if !exists(path) {
		return
	}
	c := exec.Command(path, args...)
	c.Dir = filepath.Dir(path)
	_ = c.Start()
}
func localLANIP() string {
	ifs, _ := net.Interfaces()
	var fb string
	for _, it := range ifs {
		if it.Flags&net.FlagUp == 0 || it.Flags&net.FlagLoopback != 0 {
			continue
		}
		as, _ := it.Addrs()
		for _, a := range as {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			i := ip.To4()
			if i == nil || i.IsLoopback() || (i[0] == 169 && i[1] == 254) {
				continue
			}
			s := i.String()
			if i[0] == 10 || (i[0] == 192 && i[1] == 168) || (i[0] == 172 && i[1] >= 16 && i[1] <= 31) {
				return s
			}
			if fb == "" {
				fb = s
			}
		}
	}
	return fb
}

func installServer() (string, error) {
	srv := filepath.Join(rootDir(), "Server")
	if err := os.MkdirAll(srv, 0755); err != nil {
		return "", err
	}
	v3 := filepath.Join(srv, "EBS_v3_Service.exe")
	m, err := mgr.Connect()
	if err != nil {
		return "", fmt.Errorf("Windows servis yöneticisine bağlanılamadı: %w", err)
	}
	defer m.Disconnect()
	if err = stopRegisteredService(m); err != nil {
		return "", err
	}
	if err = stopInstalledExe(v3); err != nil {
		return "", fmt.Errorf("önceki merkezi Server süreci durdurulamadı: %w", err)
	}
	if portInUse("127.0.0.1:8792") {
		return "", fmt.Errorf("8792 portu başka bir uygulama tarafından kullanılıyor; mevcut süreç kapatılmadı")
	}
	if err = writePayload("EBS_v3_Service.exe", v3); err != nil {
		return "", err
	}
	if b, e := readPayload("EBS_Core_Server.exe"); e == nil && len(b) > 0 {
		if err = writePayload("EBS_Core_Server.exe", filepath.Join(srv, "EBS_Core_Server.exe")); err != nil {
			return "", err
		}
	}
	service, err := configureService(m, v3)
	if err != nil {
		return "", err
	}
	defer service.Close()
	if err = awaitService(service); err != nil {
		return "", fmt.Errorf("%w%s", err, serverLogTail())
	}
	for _, rule := range []struct{ name, protocol, port string }{{"EBS v3 STABLE 8792", "TCP", "8792"}, {"EBS v3 STABLE Discovery 8793", "UDP", "8793"}} {
		err = run("netsh.exe", "advfirewall", "firewall", "set", "rule", "name="+rule.name, "new", "dir=in", "action=allow", "protocol="+rule.protocol, "localport="+rule.port, "enable=yes")
		if err != nil {
			err = run("netsh.exe", "advfirewall", "firewall", "add", "rule", "name="+rule.name, "dir=in", "action=allow", "protocol="+rule.protocol, "localport="+rule.port, "enable=yes")
		}
		if err != nil {
			return "", fmt.Errorf("Server yerelde hazır fakat ağ erişimi güvenlik duvarına eklenemedi (%s): %w", rule.port, err)
		}
	}
	cmd := filepath.Join(srv, "EBS_Server_Baslat.cmd")
	script := "@echo off\r\nsc start EBSv3ProService\r\nif errorlevel 1 echo Servis baslatilamadi veya zaten calisiyor. EBS kurulum gunlugunu kontrol edin.\r\nexit /b\r\n"
	if err = os.WriteFile(cmd, []byte(script), 0644); err != nil {
		return "", err
	}
	// Automatic SCM startup replaces the obsolete logon console shortcut.
	if err = os.Remove(filepath.Join(startupDir(), "EBS Server v3.1 STABLE.lnk")); err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("eski başlangıç kısayolu kaldırılamadı: %w", err)
	}
	ico := filepath.Join(rootDir(), "Icons", "EBS_Server.ico")
	if err := shortcut(filepath.Join(startMenu(), "EBS Server Başlat.lnk"), cmd, ico, srv, fullName+" Server başlat"); err != nil {
		return "", err
	}
	ip := localLANIP()
	if ip == "" {
		ip = "localhost"
	}
	u := "http://" + net.JoinHostPort(ip, "8792")
	if err = os.MkdirAll(dataDir(), 0755); err != nil {
		return "", err
	}
	if err = os.WriteFile(filepath.Join(dataDir(), "v3_server_url.txt"), []byte(u+"\r\n"), 0644); err != nil {
		return "", err
	}
	info := "EBS MERKEZİ SERVER ADRESİ\r\n" + u + "\r\n\r\nAynı bilgisayardaki istemciler localhost üzerinden de bağlanabilir.\r\nUzak Personel/Müşteri bilgisayarlarında bu adresi kullanın.\r\n"
	if err = os.WriteFile(filepath.Join(desktop(), "EBS_SERVER_ADRESI.txt"), []byte(info), 0644); err != nil {
		return "", err
	}
	return u, nil
}

func writeLoginInfo() error {
	b, err := readPayload("GIRIS_BILGILERI.txt")
	if err != nil {
		return err
	}
	for _, path := range []string{filepath.Join(rootDir(), "GIRIS_BILGILERI.txt"), filepath.Join(desktop(), "EBS_GIRIS_BILGILERI.txt")} {
		if err = os.WriteFile(path, b, 0644); err != nil {
			return fmt.Errorf("giriş bilgileri yazılamadı (%s): %w", path, err)
		}
	}
	return nil
}
func writeUninstall() error {
	path := filepath.Join(rootDir(), "EBS_v3_Kaldir.cmd")
	script := "@echo off\r\n" +
		"net session >nul 2>&1 || (powershell -NoProfile -Command \"Start-Process '%~f0' -Verb RunAs\" & exit /b)\r\n" +
		"sc stop EBSv3ProService\r\n" +
		"sc delete EBSv3ProService\r\n" +
		"echo EBS servis kaydi kaldirildi. Veri dosyalari korunmustur.\r\npause\r\n"
	if err := os.WriteFile(path, []byte(script), 0644); err != nil {
		return fmt.Errorf("kaldırma dosyası yazılamadı: %w", err)
	}
	return nil
}

func verifyInstalled(role string) error {
	paths := []string{}
	if role == "admin" {
		paths = []string{filepath.Join(rootDir(), "Admin", "EBS_Admin.exe"), filepath.Join(rootDir(), "Personel", "EBS_Personel.exe"), filepath.Join(rootDir(), "Musteri", "EBS_Musteri.exe"), filepath.Join(rootDir(), "Server", "EBS_v3_Service.exe")}
	} else if role == "personnel" {
		paths = []string{filepath.Join(rootDir(), "Personel", "EBS_Personel.exe")}
	} else {
		paths = []string{filepath.Join(rootDir(), "Musteri", "EBS_Musteri.exe")}
	}
	for _, p := range paths {
		if !exists(p) {
			return fmt.Errorf("kurulum doğrulamasında dosya bulunamadı: %s", p)
		}
	}
	return nil
}

func main() {
	logf("=== v3.1 STABLE TAM KURULUM BAŞLADI | rol=%s ===", installerRole)
	if !isAdmin() {
		if unattended {
			msg(fullName, "Kurulum için yönetici izni gerekiyor.", 0x10)
			return
		}
		if elevate() {
			return
		}
		msg(fullName, "Kurulum için yönetici izni alınamadı.", 0x10)
		return
	}
	if e := installIcons(); e != nil {
		msg(fullName, "İkonlar kurulamadı:\n"+e.Error(), 0x10)
		return
	}
	var serverURL string
	var err error
	if installerRole == "admin" {
		if _, err = installClient("admin"); err == nil {
			_, err = installClient("personnel")
		}
		if err == nil {
			_, err = installClient("customer")
		}
		if err == nil {
			serverURL, err = installServer()
		}
	} else {
		_, err = installClient(installerRole)
	}
	if err != nil {
		logf("KURULUM HATASI: %v", err)
		msg(fullName, "Kurulum tamamlanamadı:\n"+err.Error()+"\n\nGünlük: "+logFile(), 0x10)
		return
	}
	if err = writeLoginInfo(); err == nil {
		err = writeUninstall()
	}
	if err != nil {
		logf("KURULUM HATASI: %v", err)
		msg(fullName, "Kurulum tamamlanamadı:\n"+err.Error()+"\n\nGünlük: "+logFile(), 0x10)
		return
	}
	if err = verifyInstalled(installerRole); err != nil {
		logf("KURULUM DOĞRULAMA HATASI: %v", err)
		msg(fullName, err.Error(), 0x10)
		return
	}
	if installerRole == "admin" && !healthOK("http://localhost:8792") {
		msg(fullName, "Dosyalar kuruldu ancak merkezi Server sağlık kontrolü geçmedi.\n\nGünlük: "+logFile(), 0x10)
		return
	}
	logf("=== v3.1 STABLE TAM KURULUM TAMAMLANDI ===")
	if installerRole == "admin" {
		msg(fullName+" — v3.1 STABLE", "Tam kurulum tamamlandı.\n\nMerkezi Server hazır: "+serverURL+"\nAdmin, Personel ve Müşteri Client kuruldu.\nGiriş bilgileri masaüstüne yazıldı.\n\nEski EBS çekirdeği yalnız uyumluluk için kullanılır; bağlantı veya giriş için zorunlu değildir.", 0x40)
	} else {
		msg(fullName+" — v3.1 STABLE", "Client kurulumu tamamlandı.\n\nProgram merkezi Server'ı otomatik arayacaktır. Bulamazsa Server IP/bilgisayar adını soracaktır.", 0x40)
	}
}
