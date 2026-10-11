//go:build !windows

package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"
)

// The same central engine can run in a cloud/Linux environment for real HTTP
// integration checks. Windows keeps the SCM entry point in service_windows.go.
func main() {
	stop := make(chan struct{})
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	go func() {
		<-signals
		close(stop)
	}()
	if err := runServer(stop); err != nil {
		log.Fatal(err)
	}
}
