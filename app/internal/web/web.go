// Package web is the local API the setup screen talks to. It listens on 127.0.0.1 only; the
// screen is a Kindle web app (mesquite) with local-port-access.
package web

import (
	"encoding/json"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/josephyancey/abs-kindle-sync/app/internal/abs"
	"github.com/josephyancey/abs-kindle-sync/app/internal/store"
)

// Address is where the setup screen finds the service.
const Address = "127.0.0.1:47823"

// App is what the API needs from the running service.
type App interface {
	// Config returns a copy of the current settings.
	Config() store.Config
	// Configure saves new settings and (re)starts syncing with them.
	Configure(cfg store.Config) error
	// SyncNow starts a library sync in the background.
	SyncNow()
	// Status describes what the service is doing.
	Status() Status
}

// Status is shown on the setup screen.
type Status struct {
	SignedIn      bool   `json:"signed_in"`
	Server        string `json:"server"`
	Username      string `json:"username"`
	Books         int    `json:"books"`
	Syncing       bool   `json:"syncing"`
	LastSync      int64  `json:"last_sync"` // ms, 0 when never
	LastResult    string `json:"last_result"`
	LastError     string `json:"last_error"`
	AskBeforeJump bool   `json:"ask_before_jump"`
	Version       string `json:"version"`
}

type library struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Selected bool   `json:"selected"`
}

// Serve runs the API until the listener fails.
func Serve(app App) error {
	ln, err := net.Listen("tcp", Address)
	if err != nil {
		return err
	}
	h := &handler{app: app}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/status", h.status)
	mux.HandleFunc("/api/login", h.login)
	mux.HandleFunc("/api/libraries", h.libraries)
	mux.HandleFunc("/api/settings", h.settings)
	mux.HandleFunc("/api/sync", h.sync)
	mux.HandleFunc("/api/logout", h.logout)
	srv := &http.Server{Handler: cors(mux), ReadHeaderTimeout: 10 * time.Second}
	return srv.Serve(ln)
}

type handler struct {
	app App
	mu  sync.Mutex // one login at a time
}

// cors lets the screen, loaded from file://, call the API.
func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			return
		}
		next.ServeHTTP(w, r)
	})
}

func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, status int, msg string) {
	reply(w, status, map[string]string{"error": msg})
}

func (h *handler) status(w http.ResponseWriter, r *http.Request) {
	reply(w, http.StatusOK, h.app.Status())
}

type loginRequest struct {
	Server   string `json:"server"`
	Username string `json:"username"`
	Password string `json:"password"`
}

// login signs in and returns the server's book libraries. The password is used once and dropped.
func (h *handler) login(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		fail(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fail(w, http.StatusBadRequest, "bad request")
		return
	}
	server := normalizeServer(req.Server)
	if server == "" || req.Username == "" {
		fail(w, http.StatusBadRequest, "Enter the server address and your username.")
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	client := abs.New(server, abs.Tokens{})
	if err := client.Login(req.Username, req.Password); err != nil {
		log.Printf("setup: sign-in to %s failed: %v", server, err)
		fail(w, http.StatusUnauthorized, friendly(err))
		return
	}
	libs, err := client.Libraries()
	if err != nil {
		fail(w, http.StatusBadGateway, friendly(err))
		return
	}
	cfg := h.app.Config()
	sameAccount := cfg.Server == server && cfg.Username == req.Username
	cfg.Server, cfg.Username = server, req.Username
	cfg.Tokens = tokensOf(client)
	if !sameAccount {
		cfg.Libraries = nil
	}
	if err := h.app.Configure(cfg); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	reply(w, http.StatusOK, map[string]any{"libraries": libraryList(libs, cfg.Libraries)})
}

func tokensOf(c *abs.Client) abs.Tokens { return c.Tokens() }

// libraries lists the server's book libraries (GET) or saves which to sync (POST {"ids": [...]}).
func (h *handler) libraries(w http.ResponseWriter, r *http.Request) {
	cfg := h.app.Config()
	if cfg.Server == "" {
		fail(w, http.StatusConflict, "Sign in first.")
		return
	}
	if r.Method == http.MethodPost {
		var req struct {
			IDs []string `json:"ids"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			fail(w, http.StatusBadRequest, "bad request")
			return
		}
		cfg.Libraries = req.IDs
		if err := h.app.Configure(cfg); err != nil {
			fail(w, http.StatusInternalServerError, err.Error())
			return
		}
		h.app.SyncNow()
	}
	client := abs.New(cfg.Server, cfg.Tokens)
	libs, err := client.Libraries()
	if err != nil {
		fail(w, http.StatusBadGateway, friendly(err))
		return
	}
	reply(w, http.StatusOK, map[string]any{"libraries": libraryList(libs, cfg.Libraries)})
}

func (h *handler) settings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		fail(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var req struct {
		AskBeforeJump *bool `json:"ask_before_jump"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fail(w, http.StatusBadRequest, "bad request")
		return
	}
	cfg := h.app.Config()
	if req.AskBeforeJump != nil {
		cfg.AskBeforeJump = *req.AskBeforeJump
	}
	if err := h.app.Configure(cfg); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	reply(w, http.StatusOK, h.app.Status())
}

func (h *handler) sync(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		fail(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	h.app.SyncNow()
	reply(w, http.StatusOK, h.app.Status())
}

func (h *handler) logout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		fail(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	cfg := h.app.Config()
	cfg.Tokens = abs.Tokens{}
	cfg.Server, cfg.Username, cfg.Libraries = "", "", nil
	if err := h.app.Configure(cfg); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	reply(w, http.StatusOK, h.app.Status())
}

func libraryList(libs []abs.Library, selected []string) []library {
	chosen := map[string]bool{}
	for _, id := range selected {
		chosen[id] = true
	}
	out := make([]library, 0, len(libs))
	for _, l := range libs {
		out = append(out, library{ID: l.ID, Name: l.Name, Selected: len(selected) == 0 || chosen[l.ID]})
	}
	return out
}

// normalizeServer accepts "audiobooks.example.com" or "http://host:13378/" and returns a base URL.
func normalizeServer(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	return strings.TrimRight(s, "/")
}

func friendly(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "wrong username or password"):
		return "Wrong username or password."
	case strings.Contains(msg, "no such host"), strings.Contains(msg, "connection refused"), strings.Contains(msg, "i/o timeout"), strings.Contains(msg, "network is unreachable"):
		return "Couldn't reach the server. Check the address and that Wi-Fi is on."
	case strings.Contains(msg, "certificate"):
		return "The server's HTTPS certificate wasn't accepted."
	default:
		return msg
	}
}
