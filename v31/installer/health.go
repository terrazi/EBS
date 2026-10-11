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
	healthBuild    = "20261011-startup-fix-1"
)

type healthDocument struct {
	Protocol    string `json:"protocol"`
	Build       string `json:"build"`
	Version     string `json:"version"`
	EngineReady bool   `json:"engine_ready"`
	EngineError string `json:"engine_error"`
	Error       string `json:"error"`
}

// Read the readiness document even on HTTP503, so engine startup failures are
// actionable instead of being reduced to an anonymous HTTP status.
func checkHealth(base string) error {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		return errors.New("Server adresi boş")
	}
	c := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	r, err := c.Get(base + "/health")
	if err != nil {
		return fmt.Errorf("erişilemiyor: %w", err)
	}
	defer r.Body.Close()
	b, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	if err != nil {
		return fmt.Errorf("yanıt okunamadı: %w", err)
	}
	if len(b) >= 64<<10 {
		return errors.New("yanıt sınırı aşıldı")
	}
	var h healthDocument
	if err := json.Unmarshal(b, &h); err != nil {
		return fmt.Errorf("HTTP %d, geçersiz JSON: %w", r.StatusCode, err)
	}
	if h.Protocol != healthProtocol {
		return fmt.Errorf("HTTP %d, protokol uyumsuz (%q)", r.StatusCode, h.Protocol)
	}
	if h.Build != healthBuild {
		return fmt.Errorf("sürüm uyumsuz (%q); merkezi Server güncellenmelidir", h.Build)
	}
	if !strings.HasPrefix(h.Version, "3.1") {
		return fmt.Errorf("runtime sürümü uyumsuz (%q)", h.Version)
	}
	if h.EngineError != "" {
		return fmt.Errorf("işlem motoru başlatılamadı: %s", h.EngineError)
	}
	if !h.EngineReady {
		return errors.New("işlem motoru hazırlanıyor (henüz hazır değil)")
	}
	if r.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d: %s", r.StatusCode, h.Error)
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
