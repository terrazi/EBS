//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

const centralServiceName = "EBSv3ProService"

func serviceState(s svc.Status) string {
	return fmt.Sprintf("durum=%d, PID=%d, Windows hata=%d, servis hata=%d", s.State, s.ProcessId, s.Win32ExitCode, s.ServiceSpecificExitCode)
}

// Replacement only starts after SCM confirms STOPPED. Deleting the service
// and sleeping briefly does not ensure deletion has completed on Windows.
func stopRegisteredService(m *mgr.Mgr) error {
	s, err := m.OpenService(centralServiceName)
	if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("Windows servis kaydı açılamadı: %w", err)
	}
	defer s.Close()
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		st, err := s.Query()
		if err != nil {
			return fmt.Errorf("servis durumu okunamadı: %w", err)
		}
		if st.State == svc.Stopped {
			return nil
		}
		// START_PENDING cannot always accept STOP. Wait for its next state before
		// retrying, rather than killing a process while its database opens.
		if st.State != svc.StopPending {
			_, err = s.Control(svc.Stop)
			if err != nil && !errors.Is(err, windows.ERROR_SERVICE_NOT_ACTIVE) && !errors.Is(err, windows.ERROR_SERVICE_CANNOT_ACCEPT_CTRL) {
				return fmt.Errorf("servis durdurulamadı (%s): %w", serviceState(st), err)
			}
		}
		time.Sleep(300 * time.Millisecond)
	}
	st, _ := s.Query()
	return fmt.Errorf("servis 90 saniye içinde durmadı (%s)", serviceState(st))
}

func configureService(m *mgr.Mgr, executable string) (*mgr.Service, error) {
	s, err := m.OpenService(centralServiceName)
	if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
		s, err = m.CreateService(centralServiceName, executable, mgr.Config{StartType: mgr.StartAutomatic, DisplayName: fullName + " v3.1 Server", Description: productName + " merkezi Server"})
		if err != nil {
			return nil, fmt.Errorf("Windows servis kaydı oluşturulamadı: %w", err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("Windows servis kaydı açılamadı: %w", err)
	} else {
		// Change only product-owned fields. Reading/updating the whole Config
		// needlessly touches account credentials, dependencies and SID policy.
		binary, _ := windows.UTF16PtrFromString(`"` + executable + `"`)
		display, _ := windows.UTF16PtrFromString(fullName + " v3.1 Server")
		if e := windows.ChangeServiceConfig(s.Handle, windows.SERVICE_NO_CHANGE, mgr.StartAutomatic, windows.SERVICE_NO_CHANGE, binary, nil, nil, nil, nil, nil, display); e != nil {
			s.Close()
			return nil, fmt.Errorf("servis yapılandırması güncellenemedi: %w", e)
		}
		description, _ := windows.UTF16PtrFromString(productName + " merkezi Server")
		d := windows.SERVICE_DESCRIPTION{Description: description}
		if e := windows.ChangeServiceConfig2(s.Handle, windows.SERVICE_CONFIG_DESCRIPTION, (*byte)(unsafe.Pointer(&d))); e != nil {
			s.Close()
			return nil, fmt.Errorf("servis açıklaması güncellenemedi: %w", e)
		}
	}
	if err = s.SetRecoveryActions([]mgr.RecoveryAction{{Type: mgr.ServiceRestart, Delay: 5 * time.Second}, {Type: mgr.ServiceRestart, Delay: 10 * time.Second}, {Type: mgr.ServiceRestart, Delay: 30 * time.Second}}, 86400); err != nil {
		s.Close()
		return nil, fmt.Errorf("servis yeniden başlatma ilkesi kaydedilemedi: %w", err)
	}
	if err = s.SetRecoveryActionsOnNonCrashFailures(true); err != nil {
		s.Close()
		return nil, fmt.Errorf("servis başlangıç hatası yeniden başlatma ilkesi kaydedilemedi: %w", err)
	}
	return s, nil
}

func awaitService(s *mgr.Service) error {
	if err := s.Start(); err != nil && !errors.Is(err, windows.ERROR_SERVICE_ALREADY_RUNNING) {
		return fmt.Errorf("Windows servisi başlatılamadı: %w", err)
	}
	deadline := time.Now().Add(90 * time.Second)
	var healthErr error
	for time.Now().Before(deadline) {
		st, err := s.Query()
		if err != nil {
			return fmt.Errorf("servis durumu okunamadı: %w", err)
		}
		healthErr = checkHealth("http://localhost:8792")
		if healthErr == nil && st.State == svc.Running {
			return nil
		}
		if st.State == svc.Stopped {
			return fmt.Errorf("merkezi Server başlarken durdu (%s); sağlık: %v", serviceState(st), healthErr)
		}
		time.Sleep(400 * time.Millisecond)
	}
	st, _ := s.Query()
	return fmt.Errorf("merkezi v3 Server 90 saniye içinde hazır olmadı (%s); sağlık: %v", serviceState(st), healthErr)
}

// Use Windows process APIs; upgrades also work where PowerShell/CIM is
// disabled. Executable-name matches alone never authorize termination.
func exactProcessHandles(path string) ([]windows.Handle, error) {
	target, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	err = windows.Process32First(snapshot, &entry)
	var handles []windows.Handle
	for err == nil {
		if strings.EqualFold(windows.UTF16ToString(entry.ExeFile[:]), filepath.Base(target)) && entry.ProcessID != uint32(os.Getpid()) {
			h, e := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, entry.ProcessID)
			if e != nil {
				// Snapshot entries can outlive a process that SCM has just stopped.
				if errors.Is(e, windows.ERROR_INVALID_PARAMETER) || errors.Is(e, windows.ERROR_NOT_FOUND) {
					err = windows.Process32Next(snapshot, &entry)
					continue
				}
				for _, p := range handles {
					windows.CloseHandle(p)
				}
				return nil, fmt.Errorf("süreç %d açılamadı: %w", entry.ProcessID, e)
			}
			buffer := make([]uint16, 32768)
			size := uint32(len(buffer))
			e = windows.QueryFullProcessImageName(h, 0, &buffer[0], &size)
			if e != nil {
				windows.CloseHandle(h)
				if errors.Is(e, windows.ERROR_INVALID_PARAMETER) || errors.Is(e, windows.ERROR_NOT_FOUND) {
					err = windows.Process32Next(snapshot, &entry)
					continue
				}
				for _, p := range handles {
					windows.CloseHandle(p)
				}
				return nil, e
			}
			if strings.EqualFold(windows.UTF16ToString(buffer[:size]), target) {
				handles = append(handles, h)
			} else {
				windows.CloseHandle(h)
			}
		}
		err = windows.Process32Next(snapshot, &entry)
	}
	if !errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		for _, p := range handles {
			windows.CloseHandle(p)
		}
		return nil, err
	}
	return handles, nil
}

func stopInstalledExe(path string) error {
	handles, err := exactProcessHandles(path)
	if err != nil {
		return err
	}
	defer func() {
		for _, h := range handles {
			windows.CloseHandle(h)
		}
	}()
	for _, h := range handles {
		if state, e := windows.WaitForSingleObject(h, 0); e == nil && state == windows.WAIT_OBJECT_0 {
			continue
		}
		if err := windows.TerminateProcess(h, 0); err != nil {
			// A collected process may have exited between the preceding check and
			// TerminateProcess. A live AccessDenied remains a genuine failure.
			if state, e := windows.WaitForSingleObject(h, 0); e == nil && state == windows.WAIT_OBJECT_0 {
				continue
			}
			return err
		}
		state, err := windows.WaitForSingleObject(h, 5000)
		if err != nil {
			return err
		}
		if state != windows.WAIT_OBJECT_0 {
			return fmt.Errorf("çalışan süreç durdurulamadı: %s", path)
		}
	}
	return nil
}

func installedProcess(path string) bool {
	hs, err := exactProcessHandles(path)
	if err != nil {
		return false
	}
	for _, h := range hs {
		windows.CloseHandle(h)
	}
	return len(hs) > 0
}

func serverLogTail() string {
	path := filepath.Join(dataDir(), "logs", "EBS_v3_Service.log")
	// The server's canonical startup log is named service.log.
	for _, candidate := range []string{filepath.Join(dataDir(), "service.log"), filepath.Join(dataDir(), "v3pro", "service.log"), filepath.Join(dataDir(), "logs", "service.log"), path} {
		b, err := os.ReadFile(candidate)
		if err != nil {
			continue
		}
		if len(b) > 1800 {
			b = b[len(b)-1800:]
		}
		return "\n\nServer günlüğü: " + candidate + "\n" + strings.TrimSpace(string(b))
	}
	return ""
}
