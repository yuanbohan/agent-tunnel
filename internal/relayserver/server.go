package relayserver

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"path"
	"strings"

	"yuanbohan/tunnel/internal/webui"
)

type HandlerConfig struct {
	Registry        *Registry
	BrowserUser     string
	BrowserPassword string
	AgentToken      string
	Files           fs.FS
}

func NewHandler(cfg HandlerConfig) http.Handler {
	registry := cfg.Registry
	if registry == nil {
		registry = NewRegistry()
	}

	files := cfg.Files
	if files == nil {
		files = webui.Files()
	}

	mux := http.NewServeMux()
	fileServer := http.FileServer(http.FS(files))

	serveRelayShell := func(w http.ResponseWriter, r *http.Request) {
		if !checkBasicAuth(r, cfg.BrowserUser, cfg.BrowserPassword) {
			w.Header().Set("WWW-Authenticate", `Basic realm="agentunnel relay"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		http.ServeFileFS(w, r, files, "relay.html")
	}

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/":
			serveRelayShell(w, r)
		case strings.HasPrefix(r.URL.Path, "/sessions/"):
			serveRelayShell(w, r)
		case strings.HasPrefix(r.URL.Path, "/assets/"):
			fileServer.ServeHTTP(w, r)
		default:
			http.NotFound(w, r)
		}
	})

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	mux.HandleFunc("/api/sessions", func(w http.ResponseWriter, r *http.Request) {
		if !checkBasicAuth(r, cfg.BrowserUser, cfg.BrowserPassword) {
			w.Header().Set("WWW-Authenticate", `Basic realm="agentunnel relay"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(registry.List())
	})

	mux.HandleFunc("/agent/ws", func(w http.ResponseWriter, r *http.Request) {
		if !checkBearer(r, cfg.AgentToken) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		http.Error(w, "not implemented yet", http.StatusNotImplemented)
	})

	mux.HandleFunc("/api/sessions/", func(w http.ResponseWriter, r *http.Request) {
		if !checkBasicAuth(r, cfg.BrowserUser, cfg.BrowserPassword) {
			w.Header().Set("WWW-Authenticate", `Basic realm="agentunnel relay"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if path.Base(r.URL.Path) != "ws" {
			http.NotFound(w, r)
			return
		}

		http.Error(w, "not implemented yet", http.StatusNotImplemented)
	})

	return mux
}
