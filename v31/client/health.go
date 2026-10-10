//go:build windows

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	healthProtocol = "ebs-v31"
	healthBuild    = "20261010-login-fix-6"
)

type healthDocument struct {
	Protocol    string `json:"protocol"`
	Build       string `json:"build"`
	Version     string `json:"version"`
	EngineReady bool   `json:"engine_ready"`
}

// checkHealth accepts only the current EBS v3 protocol/build and a ready
// transaction engine. A generic HTTP 200 page or an old service is rejected.
// Redirects are disabled so a captive portal/login page cannot be accepted.
func checkHealth(base string) error {
	base = trimURL(base)
	if base == "" {
		return errors.New("Server adresi boş")
	}
	c := &http.Client{
		Timeout: 1300 * time.Millisecond,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	r, err := c.Get(base + "/health")
	if err != nil {
		return fmt.Errorf("erişilemiyor: %w", err)
	}
	defer r.Body.Close()
	if r.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", r.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	if err != nil {
		return fmt.Errorf("yanıt okunamadı: %w", err)
	}
	// Reject oversized responses instead of parsing a truncated document.
	if len(b) >= 64<<10 {
		return errors.New("yanıt sınırı aşıldı")
	}
	var h healthDocument
	if err := json.Unmarshal(b, &h); err != nil {
		return fmt.Errorf("geçersiz JSON: %w", err)
	}
	if h.Protocol != healthProtocol {
		return fmt.Errorf("protokol uyumsuz (%q)", h.Protocol)
	}
	if h.Build != healthBuild {
		return fmt.Errorf("sürüm uyumsuz (%q)", h.Build)
	}
	if !strings.HasPrefix(h.Version, "3.1") {
		return fmt.Errorf("runtime sürümü uyumsuz (%q)", h.Version)
	}
	if !h.EngineReady {
		return errors.New("işlem motoru hazır değil")
	}
	return nil
}

func healthOK(base string) bool { return checkHealth(base) == nil }

func healthErrorText(err error) string {
	if err == nil {
		return ""
	}
	return strings.TrimSpace(err.Error())
}
