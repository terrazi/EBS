//go:build windows

package main

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

// SCM owns the local server: clients never kill a warming service or launch
// a second console process. A cold SQLite start may take longer than HTTP bind.
func ensureAdminServices() {
	if clientMode != "admin" {
		return
	}
	local := "http://localhost:8792"
	if checkHealth(local) == nil {
		return
	}
	manager, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		rememberHealthFailure(local, fmt.Errorf("Windows servis yöneticisi açılamadı: %w", err))
		return
	}
	defer windows.CloseServiceHandle(manager)
	name, _ := windows.UTF16PtrFromString("EBSv3ProService")
	handle, err := windows.OpenService(manager, name, windows.SERVICE_QUERY_STATUS)
	if err != nil {
		rememberHealthFailure(local, fmt.Errorf("EBS Windows servisi bulunamadı/açılamadı: %w; Admin kurulumunu tamamlayın", err))
		return
	}
	service := &mgr.Service{Name: "EBSv3ProService", Handle: handle}
	defer service.Close()
	status, err := service.Query()
	if err != nil {
		rememberHealthFailure(local, fmt.Errorf("servis durumu okunamadı: %w", err))
		return
	}
	if status.State == svc.Stopped {
		startHandle, e := windows.OpenService(manager, name, windows.SERVICE_START)
		if e != nil {
			rememberHealthFailure(local, fmt.Errorf("servis başlatma izni alınamadı: %w; Admin kurulumunu yönetici olarak çalıştırın", e))
			return
		}
		starter := &mgr.Service{Name: "EBSv3ProService", Handle: startHandle}
		e = starter.Start()
		starter.Close()
		if e != nil && !errors.Is(e, windows.ERROR_SERVICE_ALREADY_RUNNING) {
			rememberHealthFailure(local, fmt.Errorf("EBS servisi başlatılamadı: %w", e))
			return
		}
	}
	deadline := time.Now().Add(90 * time.Second)
	var last error
	for time.Now().Before(deadline) {
		status, err = service.Query()
		if err != nil {
			rememberHealthFailure(local, fmt.Errorf("servis durumu okunamadı: %w", err))
			return
		}
		last = checkHealth(local)
		if last == nil {
			return
		}
		if status.State == svc.Stopped {
			rememberHealthFailure(local, fmt.Errorf("EBS servisi başlarken durdu (Windows hata=%d, servis hata=%d); %v", status.Win32ExitCode, status.ServiceSpecificExitCode, last))
			return
		}
		if status.State == svc.Running && (strings.Contains(last.Error(), "sürüm uyumsuz") || strings.Contains(last.Error(), "protokol uyumsuz")) {
			rememberHealthFailure(local, last)
			return
		}
		time.Sleep(400 * time.Millisecond)
	}
	rememberHealthFailure(local, fmt.Errorf("Server 90 saniyede hazır olmadı (servis durumu=%d); %v", status.State, last))
}
