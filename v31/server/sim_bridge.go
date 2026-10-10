package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"ebs-v3-service/internal/sim"
)

// installSimulationAPI adds the real transactional v3 vertical API alongside
// the preserved compatibility endpoints. Compatibility /api/action remains
// available for the frozen STABLE regression suite; new clients use /api/v3.
func installSimulationAPI(mux *http.ServeMux, engine *sim.Engine) {
	if engine == nil {
		return
	}
	mux.HandleFunc("/api/v3/login", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			jsonOut(w, http.StatusMethodNotAllowed, map[string]any{"error": "METHOD: Yalnız POST desteklenir."})
			return
		}
		var in struct {
			Username string `json:"username"`
			Password string `json:"password"`
			Client   string `json:"client"`
		}
		if err := decodeJSON(r, &in); err != nil {
			jsonOut(w, 400, map[string]any{"error": err.Error()})
			return
		}
		out, err := engine.Login(in.Username, in.Password, in.Client)
		if err != nil {
			jsonOut(w, 401, map[string]any{"error": err.Error()})
			return
		}
		jsonOut(w, 200, out)
	})
	mux.HandleFunc("/api/v3/logout", func(w http.ResponseWriter, r *http.Request) {
		if err := engine.Logout(bearer(r)); err != nil {
			jsonOut(w, 400, map[string]any{"error": err.Error()})
			return
		}
		jsonOut(w, 200, map[string]any{"status": "SIGNED_OUT"})
	})
	mux.HandleFunc("/api/v3/session", func(w http.ResponseWriter, r *http.Request) {
		u, err := engine.Session(bearer(r))
		if err != nil {
			jsonOut(w, 401, map[string]any{"error": err.Error()})
			return
		}
		jsonOut(w, 200, u)
	})
	mux.HandleFunc("/api/v3/operation", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			jsonOut(w, 405, map[string]any{"error": "METHOD: Yalnız POST desteklenir."})
			return
		}
		u, err := engine.Session(bearer(r))
		if err != nil {
			jsonOut(w, 401, map[string]any{"error": err.Error()})
			return
		}
		var in struct {
			Operation   string      `json:"operation"`
			Payload     sim.Payload `json:"payload"`
			Idempotency string      `json:"idempotency_key"`
			Correlation string      `json:"correlation_id"`
			Terminal    string      `json:"terminal"`
		}
		if err = decodeJSON(r, &in); err != nil {
			jsonOut(w, 400, map[string]any{"error": err.Error()})
			return
		}
		if in.Payload == nil {
			in.Payload = sim.Payload{}
		}
		if in.Idempotency == "" {
			in.Idempotency = r.Header.Get("Idempotency-Key")
		}
		in.Payload["_idempotency_key"] = in.Idempotency
		in.Payload["_correlation_id"] = in.Correlation
		in.Payload["_terminal"] = in.Terminal
		out, err := engine.Execute(u, in.Operation, in.Payload)
		if err != nil {
			status := 422
			if errors.Is(err, sim.ErrUnauthorized) {
				status = 401
			}
			if errors.Is(err, sim.ErrForbidden) {
				status = 403
			}
			jsonOut(w, status, map[string]any{"error": err.Error()})
			return
		}
		jsonOut(w, 200, out)
	})
	mux.HandleFunc("/api/v3/query", func(w http.ResponseWriter, r *http.Request) {
		u, err := engine.Session(bearer(r))
		if err != nil {
			jsonOut(w, 401, map[string]any{"error": err.Error()})
			return
		}
		resource := r.URL.Query().Get("resource")
		out, known, err := engine.BankingQuery(u, resource, r.URL.Query())
		if !known {
			out, known, err = engine.TrainingQuery(u, resource, r.URL.Query())
		}
		if !known {
			jsonOut(w, 404, map[string]any{"error": "RESOURCE: Kaynak bulunamadı."})
			return
		}
		if err != nil {
			jsonOut(w, 403, map[string]any{"error": err.Error()})
			return
		}
		jsonOut(w, 200, out)
	})
}

func decodeJSON(r *http.Request, v any) error {
	b, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
	if err != nil {
		return err
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		return errors.New("BODY: JSON gövdesi zorunludur.")
	}
	if err = json.Unmarshal(b, v); err != nil {
		return errors.New("BODY: Geçerli JSON gönderin.")
	}
	return nil
}
func bearer(r *http.Request) string {
	h := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(h) >= 7 && strings.EqualFold(h[:7], "Bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return strings.TrimSpace(r.Header.Get("X-EBS-Session"))
}
