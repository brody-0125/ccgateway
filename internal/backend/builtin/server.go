package builtin

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
)

func Serve(configPath string) error {
	cfg, err := LoadConfig(configPath)
	if err != nil {
		return fmt.Errorf("failed to load builtin gateway config: %w", err)
	}

	s := &http.Server{
		Addr:    cfg.Listen,
		Handler: Handler(cfg),
	}
	log.Printf("[ccgateway builtin] listen=%s config=%s pid=%d\n", cfg.Listen, configPath, os.Getpid())
	return s.ListenAndServe()
}

func Handler(cfg ServeConfig) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data": []map[string]any{
				{
					"id":       cfg.Model,
					"object":   "model",
					"created":  0,
					"owned_by": "ccgateway-builtin",
				},
			},
		})
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v1/") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotImplemented)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]any{
					"type":    "not_implemented",
					"message": "builtin backend currently supports health endpoint only (/v1/models); use gateway_backend=cliproxyapi for full LLM routing",
				},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	return mux
}
