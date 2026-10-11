//go:build windows

package main

import (
	"errors"
	"log"
	"os"
	"os/signal"
	"sync"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
)

const centralServiceName = "EBSv3ProService"

type centralService struct{}

// SCM stays START_PENDING while SQLite initializes. Checkpoints distinguish a
// slow cold start from a hung service; RUNNING is reported only after the
// transaction engine and HTTP listener are ready. Startup failures have a
// service-specific exit code instead of looking like a successful stop.
func (centralService) Execute(_ []string, requests <-chan svc.ChangeRequest, statuses chan<- svc.Status) (bool, uint32) {
	stop := make(chan struct{})
	var stopOnce sync.Once
	requestStop := func() { stopOnce.Do(func() { close(stop) }) }
	defer requestStop()
	ready := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		result <- runServerWithReady(stop, func() { close(ready) })
	}()
	status := svc.Status{State: svc.StartPending, CheckPoint: 1, WaitHint: 180000}
	statuses <- status
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ready:
			ready = nil
			if status.State == svc.StartPending {
				status = svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
				statuses <- status
			}
		case <-ticker.C:
			if status.State == svc.StartPending || status.State == svc.StopPending {
				status.CheckPoint++
				statuses <- status
			}
		case request, ok := <-requests:
			if !ok {
				requestStop()
				requests = nil
				continue
			}
			switch request.Cmd {
			case svc.Interrogate:
				statuses <- status
			case svc.Stop, svc.Shutdown:
				status = svc.Status{State: svc.StopPending, CheckPoint: 1, WaitHint: 10000}
				statuses <- status
				requestStop()
			}
		case err := <-result:
			if err != nil {
				log.Printf("service server: %v", err)
				return true, 1
			}
			return false, 0
		}
	}
}

func runConsole() {
	stop := make(chan struct{})
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt)
	defer signal.Stop(signals)
	go func() {
		<-signals
		close(stop)
	}()
	if err := runServer(stop); err != nil {
		log.Fatal(err)
	}
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--console" {
		runConsole()
		return
	}
	// The dispatcher handshake is authoritative. Parent-process heuristics
	// can misclassify SCM launches and leave a console process running while
	// the installer receives ERROR_SERVICE_REQUEST_TIMEOUT. Only the explicit
	// "not connected to SCM" result is eligible for interactive fallback.
	err := svc.Run(centralServiceName, centralService{})
	if errors.Is(err, windows.ERROR_FAILED_SERVICE_CONTROLLER_CONNECT) {
		runConsole()
		return
	}
	if err != nil {
		log.Fatalf("SCM registration: %v", err)
	}
}
