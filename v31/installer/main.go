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
)

var installerRole = "admin" // admin | personnel | customer

//go:embed payload/*
var payload embed.FS

const fullName = "ERBAA BANKACILIK VE SİGORTACILIK HİZMETLERİ"
const productName = "BANKACILIK VE SİGORTACILIK EĞİTİM SİMÜLASYONU"

func u16(s string) *uint16 { p, _ := syscall.UTF16PtrFromString(s); return p }
func msg(title, text string, flags uintptr) {
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

// stopInstalledExe stops only a process whose executable path is the file we
// are about to replace. Matching by image name (taskkill /IM) could terminate
// an unrelated program with the same filename on a shared training PC.
func stopInstalledExe(path string) error {
	target, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	script := fmt.Sprintf("$target='%s'; Get-CimInstance Win32_Process -Filter \"Name='%s'\" | Where-Object { $_.ExecutablePath -and ([IO.Path]::GetFullPath($_.ExecutablePath) -ieq $target) } | ForEach-Object { Stop-Process -Id $_.ProcessId -Force -ErrorAction Stop }", psQuote(target), psQuote(filepath.Base(target)))
	if err := powershell(script); err != nil {
		return err
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !installedProcess(path) {
			return nil
		}
		time.Sleep(150 * time.Millisecond)
	}
	return fmt.Errorf("çalışan süreç durdurulamadı: %s", path)
}

func installedProcess(path string) bool {
	target, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	script := fmt.Sprintf("$target='%s'; $p=Get-CimInstance Win32_Process -Filter \"Name='%s'\" | Where-Object { $_.ExecutablePath -and ([IO.Path]::GetFullPath($_.ExecutablePath) -ieq $target) }; if($p){ exit 0 } else { exit 1 }", psQuote(target), psQuote(filepath.Base(target)))
	return powershell(script) == nil
}

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
	return e
}
func psQuote(s string) string { return strings.ReplaceAll(s, "'", "''") }
func powershell(script string) error {
	return exec.Command("powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", script).Run()
}
func shortcut(path, target, icon, workdir, desc string) {
	_ = os.MkdirAll(filepath.Dir(path), 0755)
	sc := fmt.Sprintf(`$w=New-Object -ComObject WScript.Shell;$s=$w.CreateShortcut('%s');$s.TargetPath='%s';$s.WorkingDirectory='%s';$s.IconLocation='%s,0';$s.Description='%s';$s.Save()`, psQuote(path), psQuote(target), psQuote(workdir), psQuote(icon), psQuote(desc))
	_ = powershell(sc)
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
	if e := stopInstalledExe(dst); e != nil && installedProcess(dst) {
		return "", fmt.Errorf("eski %s istemcisi kapatılamadı: %w", label, e)
	}
	preserveLegacy(dst, strings.TrimSuffix(name, ".exe"))
	if e := writePayload(payloadName, dst); e != nil {
		return "", e
	}
	ico := filepath.Join(rootDir(), "Icons", iconName)
	link := fullName + " - " + label + ".lnk"
	shortcut(filepath.Join(desktop(), link), dst, ico, dir, fullName+" — "+label)
	shortcut(filepath.Join(startMenu(), link), dst, ico, dir, fullName+" — "+label)
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
	_ = os.MkdirAll(srv, 0755)
	v3 := filepath.Join(srv, "EBS_v3_Service.exe")
	core := filepath.Join(srv, "EBS_Core_Server.exe")
	_ = run("sc.exe", "stop", "EBSv3ProService")
	if e := stopInstalledExe(v3); e != nil && installedProcess(v3) {
		return "", fmt.Errorf("eski merkezi Server kapatılamadı: %w", e)
	}
	// Do not kill an unrelated listener. If 8792 is occupied by another
	// application, report it before replacing files or registering the service.
	if portInUse("127.0.0.1:8792") && !healthOK("http://localhost:8792") {
		return "", fmt.Errorf("8792 portu başka bir uygulama tarafından kullanılıyor; mevcut süreç kapatılmadı")
	}
	if e := writePayload("EBS_v3_Service.exe", v3); e != nil {
		return "", e
	}
	// Eski EBS çekirdeği artık zorunlu değildir; bulunuyorsa uyumluluk için kullanılır.
	if b, e := readPayload("EBS_Core_Server.exe"); e == nil && len(b) > 0 {
		_ = writePayload("EBS_Core_Server.exe", core)
	}
	_ = run("netsh.exe", "advfirewall", "firewall", "delete", "rule", "name=EBS v3 STABLE 8792")
	_ = run("netsh.exe", "advfirewall", "firewall", "add", "rule", "name=EBS v3 STABLE 8792", "dir=in", "action=allow", "protocol=TCP", "localport=8792")
	_ = run("netsh.exe", "advfirewall", "firewall", "delete", "rule", "name=EBS v3 STABLE Discovery 8793")
	_ = run("netsh.exe", "advfirewall", "firewall", "add", "rule", "name=EBS v3 STABLE Discovery 8793", "dir=in", "action=allow", "protocol=UDP", "localport=8793")

	_ = run("sc.exe", "delete", "EBSv3ProService")
	time.Sleep(800 * time.Millisecond)
	quoted := `"` + v3 + `"`
	_ = run("sc.exe", "create", "EBSv3ProService", "binPath=", quoted, "start=", "auto", "DisplayName=", fullName+" v3.1 STABLE Server")
	_ = run("sc.exe", "description", "EBSv3ProService", productName+" merkezi Server")
	_ = run("sc.exe", "failure", "EBSv3ProService", "reset=", "86400", "actions=", "restart/5000/restart/10000/restart/30000")
	_ = run("sc.exe", "start", "EBSv3ProService")
	if !waitHealth("http://localhost:8792", 7*time.Second) {
		start(v3, "--console")
	}
	if !waitHealth("http://localhost:8792", 8*time.Second) {
		return "", fmt.Errorf("merkezi v3 Server 8792 portunda başlatılamadı")
	}
	// Legacy core yalnız uyumluluk köprüsü için denenir; başarısızlığı kurulumu bozmaz.
	if exists(core) {
		start(core)
	}

	cmd := filepath.Join(srv, "EBS_Server_Baslat.cmd")
	script := "@echo off\r\n" +
		"cd /d \"%~dp0\"\r\n" +
		"start \"\" /min \"%~dp0EBS_v3_Service.exe\" --console\r\n" +
		"if exist \"%~dp0EBS_Core_Server.exe\" start \"\" /min \"%~dp0EBS_Core_Server.exe\"\r\n" +
		"exit /b 0\r\n"
	_ = os.WriteFile(cmd, []byte(script), 0644)
	ico := filepath.Join(rootDir(), "Icons", "EBS_Server.ico")
	shortcut(filepath.Join(startupDir(), "EBS Server v3.1 STABLE.lnk"), cmd, ico, srv, fullName+" Server başlangıcı")
	shortcut(filepath.Join(startMenu(), "EBS Server Başlat.lnk"), cmd, ico, srv, fullName+" Server başlat")
	ip := localLANIP()
	if ip == "" {
		ip = "localhost"
	}
	u := "http://" + net.JoinHostPort(ip, "8792")
	_ = os.MkdirAll(dataDir(), 0755)
	_ = os.WriteFile(filepath.Join(dataDir(), "v3_server_url.txt"), []byte(u+"\r\n"), 0644)
	info := "EBS MERKEZİ SERVER ADRESİ\r\n" + u + "\r\n\r\nAynı bilgisayardaki istemciler otomatik olarak localhost üzerinden de bağlanabilir.\r\nUzak Personel/Müşteri bilgisayarlarında bu adresi kullanın.\r\n"
	_ = os.WriteFile(filepath.Join(desktop(), "EBS_SERVER_ADRESI.txt"), []byte(info), 0644)
	return u, nil
}

func writeLoginInfo() {
	b, e := readPayload("GIRIS_BILGILERI.txt")
	if e != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(rootDir(), "GIRIS_BILGILERI.txt"), b, 0644)
	_ = os.WriteFile(filepath.Join(desktop(), "EBS_GIRIS_BILGILERI.txt"), b, 0644)
}
func writeUninstall() {
	p := filepath.Join(rootDir(), "EBS_v3_Kaldir.cmd")
	s := "@echo off\r\n" +
		"net session >nul 2>&1 || (powershell -NoProfile -Command \"Start-Process '%~f0' -Verb RunAs\" & exit /b)\r\n" +
		"sc stop EBSv3ProService >nul 2>&1\r\n" +
		"sc delete EBSv3ProService >nul 2>&1\r\n" +
		"taskkill /F /IM EBS_v3_Service.exe >nul 2>&1\r\n" +
		"echo EBS v3 servis kaydi kaldirildi. Legacy klasoru ve veri dosyalari korunmustur.\r\n" +
		"pause\r\n"
	_ = os.WriteFile(p, []byte(s), 0644)
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
	writeLoginInfo()
	writeUninstall()
	if err = verifyInstalled(installerRole); err != nil {
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
