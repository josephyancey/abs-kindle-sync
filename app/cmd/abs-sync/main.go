// abs-sync keeps a jailbroken Kindle in sync with Audiobookshelf using Amazon's own reader.
//
//	abs-sync login <server> <username> <password>   sign in (the password isn't stored)
//	abs-sync sync                                    copy new and changed books now
//	abs-sync run                                     run in the background: books + reading position
//	abs-sync status                                  show the setup and synced books
package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"sync"
	"syscall"
	"time"

	// Trusted root certificates for HTTPS servers, used when the Kindle's own are missing or old
	_ "golang.org/x/crypto/x509roots/fallback"

	"github.com/josephyancey/abs-kindle-sync/app/internal/abs"
	"github.com/josephyancey/abs-kindle-sync/app/internal/library"
	"github.com/josephyancey/abs-kindle-sync/app/internal/store"
	"github.com/josephyancey/abs-kindle-sync/app/internal/syncer"
	"github.com/josephyancey/abs-kindle-sync/app/internal/web"
)

func main() {
	// A Kindle has well under 512 MB shared with the reader and library. Collect garbage early and
	// keep the heap small, so building a big book's position map can't starve the library app.
	debug.SetGCPercent(20)
	debug.SetMemoryLimit(48 << 20)

	base := os.Getenv("ABS_SYNC_DIR")
	if base == "" {
		exe, _ := os.Executable()
		base = filepath.Dir(filepath.Dir(exe)) // <base>/bin/abs-sync
	}
	st := &store.Store{Dir: base}
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "login":
		if len(os.Args) != 5 {
			usage()
		}
		err = login(st, os.Args[2], os.Args[3], os.Args[4])
	case "sync":
		err = syncOnce(st)
	case "run":
		err = run(st, base)
	case "status":
		err = status(st)
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "abs-sync:", err)
		os.Exit(1)
	}
}

// exitStorageLeaving is a non-zero exit, so the boot job restarts abs-sync once USB drive mode ends.
const exitStorageLeaving = 75

func usage() {
	fmt.Fprintln(os.Stderr, "usage: abs-sync login <server> <username> <password> | sync | run | status")
	os.Exit(2)
}

func client(st *store.Store, cfg *store.Config) *abs.Client {
	c := abs.New(cfg.Server, cfg.Tokens)
	c.OnTokens = func(t abs.Tokens) {
		cfg.Tokens = t
		if err := st.SaveConfig(cfg); err != nil {
			log.Printf("saving login: %v", err)
		}
	}
	return c
}

func login(st *store.Store, server, username, password string) error {
	cfg, err := st.LoadConfig()
	if err != nil {
		return err
	}
	cfg.Server, cfg.Username = server, username
	c := client(st, cfg)
	if err := c.Login(username, password); err != nil {
		return err
	}
	libraries, err := c.Libraries()
	if err != nil {
		return err
	}
	fmt.Printf("Signed in to %s as %s. Book libraries:\n", server, username)
	for _, l := range libraries {
		fmt.Printf("  %s (%s)\n", l.Name, l.ID)
	}
	return st.SaveConfig(cfg)
}

func loaded(st *store.Store) (*store.Config, *store.State, error) {
	cfg, err := st.LoadConfig()
	if err != nil {
		return nil, nil, err
	}
	if cfg.Server == "" {
		return nil, nil, fmt.Errorf("not set up yet: run abs-sync login first")
	}
	state, err := st.LoadState()
	return cfg, state, err
}

func syncOnce(st *store.Store) error {
	cfg, state, err := loaded(st)
	if err != nil {
		return err
	}
	res, err := library.Sync(client(st, cfg), cfg, state, st)
	fmt.Printf("%d added, %d updated, %d position maps built, %d unchanged, %d failed\n", res.Added, res.Updated, res.Mapped, res.Unchanged, res.Failed)
	return err
}

func run(st *store.Store, base string) error {
	logFile, err := os.OpenFile(filepath.Join(base, "abs-sync.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err == nil {
		if info, _ := logFile.Stat(); info != nil && info.Size() > 1<<20 {
			logFile.Truncate(0)
		}
		log.SetOutput(io.MultiWriter(os.Stderr, logFile))
	}
	cfg, err := st.LoadConfig()
	if err != nil {
		return err
	}
	state, err := st.LoadState()
	if err != nil {
		return err
	}
	svc := &service{store: st, base: base, cfg: *cfg, state: state, syncReq: make(chan struct{}, 1)}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		if err := web.Serve(svc); err != nil {
			log.Printf("setup API: %v", err)
		}
	}()
	log.Printf("abs-sync starting (signed in: %v)", cfg.Server != "" && cfg.Username != "")
	svc.restart()
	<-ctx.Done()
	svc.stopSyncer()
	log.Printf("abs-sync stopped")
	return nil
}

// service is the running app: the reading-position and library sync, restarted whenever the
// setup screen changes the settings.
type service struct {
	store   *store.Store
	base    string
	state   *store.State
	syncReq chan struct{}

	mu         sync.Mutex
	cfg        store.Config
	cancel     context.CancelFunc
	done       chan struct{}
	syncing    bool
	lastSync   time.Time
	lastResult string
	lastError  string
}

func (s *service) Config() store.Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg
}

func (s *service) Configure(cfg store.Config) error {
	s.mu.Lock()
	s.cfg = cfg
	s.mu.Unlock()
	if cfg.Server != "" && cfg.Username != "" {
		account := cfg.Username + "@" + cfg.Server
		s.state.Mu.Lock()
		if s.state.Account != account {
			// Signed in to a different account: the synced books belong to the old one
			if s.state.Account != "" && len(s.state.Books) > 0 {
				log.Printf("abs-sync: signed in as %s, removing %d books from %s", account, len(s.state.Books), s.state.Account)
				s.stopSyncer()
				s.store.Forget(s.state)
			}
			s.state.Account = account
			_ = s.store.SaveState(s.state)
		}
		s.state.Mu.Unlock()
	}
	if err := s.store.SaveConfig(&cfg); err != nil {
		return err
	}
	s.restart()
	return nil
}

func (s *service) SyncNow() {
	select {
	case s.syncReq <- struct{}{}:
	default:
	}
}

func (s *service) Status() web.Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.Mu.Lock()
	books := len(s.state.Books)
	s.state.Mu.Unlock()
	st := web.Status{
		SignedIn:      s.cfg.Server != "" && (s.cfg.Tokens.Access != "" || s.cfg.Tokens.Legacy != ""),
		Server:        s.cfg.Server,
		Username:      s.cfg.Username,
		Books:         books,
		Syncing:       s.syncing,
		LastResult:    s.lastResult,
		LastError:     s.lastError,
		AskBeforeJump: s.cfg.AskBeforeJump,
	}
	if !s.lastSync.IsZero() {
		st.LastSync = s.lastSync.UnixMilli()
	}
	return st
}

func (s *service) stopSyncer() {
	s.mu.Lock()
	cancel, done := s.cancel, s.done
	s.cancel, s.done = nil, nil
	s.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
}

// restart stops the running syncer, if any, and starts one with the current settings.
func (s *service) restart() {
	s.stopSyncer()
	cfg := s.Config()
	if cfg.Server == "" || (cfg.Tokens.Access == "" && cfg.Tokens.Legacy == "") {
		log.Printf("abs-sync: not signed in; open Audiobookshelf Sync in the library to set up")
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	s.mu.Lock()
	s.cancel, s.done = cancel, done
	s.mu.Unlock()

	client := abs.New(cfg.Server, cfg.Tokens)
	client.OnTokens = func(t abs.Tokens) {
		s.mu.Lock()
		s.cfg.Tokens = t
		c := s.cfg
		s.mu.Unlock()
		if err := s.store.SaveConfig(&c); err != nil {
			log.Printf("saving login: %v", err)
		}
	}
	syncCfg := cfg
	sy := &syncer.Syncer{
		Client:       client,
		Store:        s.store,
		Config:       &syncCfg,
		State:        s.state,
		PromptPath:   filepath.Join(s.base, "bin", "abs-prompt.sh"),
		FBInk:        fbink(),
		SyncRequests: s.syncReq,
		OnStorageLeaving: func() {
			log.Printf("abs-sync: USB drive mode, exiting until it ends")
			os.Exit(exitStorageLeaving)
		},
		OnLibrarySync: func(res *library.Result, err error) {
			s.mu.Lock()
			defer s.mu.Unlock()
			if res == nil {
				s.syncing = true
				return
			}
			s.syncing = false
			s.lastSync = time.Now()
			s.lastResult = fmt.Sprintf("%d added, %d updated, %d unchanged", res.Added, res.Updated, res.Unchanged)
			if res.Removed > 0 {
				s.lastResult += fmt.Sprintf(", %d removed", res.Removed)
			}
			if res.Failed > 0 {
				s.lastResult += fmt.Sprintf(", %d failed", res.Failed)
			}
			s.lastError = ""
			if err != nil {
				s.lastError = err.Error()
			}
		},
	}
	log.Printf("abs-sync: syncing with %s as %s, %d books", cfg.Server, cfg.Username, len(s.state.Books))
	go func() {
		defer close(done)
		sy.Run(ctx)
	}()
}

func fbink() string {
	for _, p := range []string{"/mnt/us/libkh/bin/fbink", "/var/local/kmc/bin/fbink", "/usr/bin/fbink", "/mnt/us/extensions/MRInstaller/bin/KHF/fbink"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "fbink"
}

func status(st *store.Store) error {
	cfg, state, err := loaded(st)
	if err != nil {
		return err
	}
	fmt.Printf("Server: %s\nUser: %s\nFolder: %s\nAsk before jumping: %v\nBooks: %d\n", cfg.Server, cfg.Username, cfg.Folder, cfg.AskBeforeJump, len(state.Books))
	for _, b := range state.Books {
		fmt.Printf("  %s - %s (map: %v, position: %d)\n", b.Author, b.Title, b.HasMap, b.LastPosition)
	}
	return nil
}
