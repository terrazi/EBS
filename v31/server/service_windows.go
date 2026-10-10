//go:build windows

package main

import (
	"log"
	"os"
	"syscall"
	"unsafe"
)

const (
	serviceWin32OwnProcess              = 0x00000010
	serviceStopped                      = 0x00000001
	serviceStartPending                 = 0x00000002
	serviceStopPending                  = 0x00000003
	serviceRunning                      = 0x00000004
	serviceAcceptStop                   = 0x00000001
	serviceAcceptShutdown               = 0x00000004
	serviceControlStop                  = 0x00000001
	serviceControlShutdown              = 0x00000005
	errorFailedServiceControllerConnect = 1063
)

type serviceStatus struct {
	ServiceType             uint32
	CurrentState            uint32
	ControlsAccepted        uint32
	Win32ExitCode           uint32
	ServiceSpecificExitCode uint32
	CheckPoint              uint32
	WaitHint                uint32
}
type serviceTableEntry struct {
	ServiceName *uint16
	ServiceProc uintptr
}

var (
	advapi              = syscall.NewLazyDLL("advapi32.dll")
	procStartDispatcher = advapi.NewProc("StartServiceCtrlDispatcherW")
	procRegisterHandler = advapi.NewProc("RegisterServiceCtrlHandlerExW")
	procSetStatus       = advapi.NewProc("SetServiceStatus")
	statusHandle        uintptr
	stopCh              chan struct{}
)

func u16(s string) *uint16 { p, _ := syscall.UTF16PtrFromString(s); return p }
func setStatus(state, accepted, wait uint32) {
	st := serviceStatus{ServiceType: serviceWin32OwnProcess, CurrentState: state, ControlsAccepted: accepted, WaitHint: wait}
	procSetStatus.Call(statusHandle, uintptr(unsafe.Pointer(&st)))
}
func serviceHandler(ctrl, eventType, eventData, context uintptr) uintptr {
	if ctrl == serviceControlStop || ctrl == serviceControlShutdown {
		setStatus(serviceStopPending, 0, 5000)
		select {
		case <-stopCh:
		default:
			close(stopCh)
		}
	}
	return 0
}
func serviceMain(argc, argv uintptr) uintptr {
	stopCh = make(chan struct{})
	h, _, _ := procRegisterHandler.Call(uintptr(unsafe.Pointer(u16("EBSv3ProService"))), syscall.NewCallback(serviceHandler), 0)
	statusHandle = h
	if statusHandle == 0 {
		return 0
	}
	setStatus(serviceStartPending, 0, 3000)
	setStatus(serviceRunning, serviceAcceptStop|serviceAcceptShutdown, 0)
	if err := runServer(stopCh); err != nil {
		log.Printf("service server: %v", err)
	}
	setStatus(serviceStopped, 0, 0)
	return 0
}
func runAsService() bool {
	entries := [2]serviceTableEntry{{ServiceName: u16("EBSv3ProService"), ServiceProc: syscall.NewCallback(serviceMain)}, {}}
	r, _, e := procStartDispatcher.Call(uintptr(unsafe.Pointer(&entries[0])))
	if r != 0 {
		return true
	}
	if errno, ok := e.(syscall.Errno); ok && uint32(errno) == errorFailedServiceControllerConnect {
		return false
	}
	return false
}
func main() {
	if len(os.Args) > 1 && os.Args[1] == "--console" {
		ch := make(chan struct{})
		if err := runServer(ch); err != nil {
			log.Fatal(err)
		}
		return
	}
	if !runAsService() {
		ch := make(chan struct{})
		if err := runServer(ch); err != nil {
			log.Fatal(err)
		}
	}
}
