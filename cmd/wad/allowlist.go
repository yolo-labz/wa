package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/fsnotify/fsnotify"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// allowlistFile is the TOML schema for allowlist.toml.
type allowlistFile struct {
	Rules []allowlistRule `toml:"rules"`
}

// allowlistRule is a single entry in the allowlist TOML.
type allowlistRule struct {
	JID     string   `toml:"jid"`
	Actions []string `toml:"actions"`
}

// loadAllowlist reads allowlist.toml and builds a domain.Allowlist.
// If the file does not exist, it returns an empty allowlist.
func loadAllowlist(path string) (*domain.Allowlist, error) {
	al := domain.NewAllowlist()

	data, err := os.ReadFile(path) //nolint:gosec // path is from XDG config, validated at startup
	if os.IsNotExist(err) {
		return al, nil
	}
	if err != nil {
		return nil, fmt.Errorf("loadAllowlist: read %s: %w", path, err)
	}

	return parseAllowlistBytes(data, al)
}

// parseAllowlistBytes parses TOML data into the given Allowlist, returning
// it on success or an error on parse/validation failure.
func parseAllowlistBytes(data []byte, al *domain.Allowlist) (*domain.Allowlist, error) {
	var af allowlistFile
	if err := toml.Unmarshal(data, &af); err != nil {
		return nil, fmt.Errorf("loadAllowlist: parse: %w", err)
	}

	for _, r := range af.Rules {
		jid, err := domain.Parse(r.JID)
		if err != nil {
			return nil, fmt.Errorf("loadAllowlist: invalid JID %q: %w", r.JID, err)
		}
		actions := make([]domain.Action, 0, len(r.Actions))
		for _, s := range r.Actions {
			a, err := domain.ParseAction(s)
			if err != nil {
				return nil, fmt.Errorf("loadAllowlist: JID %q: %w", r.JID, err)
			}
			actions = append(actions, a)
		}
		al.Grant(jid, actions...)
	}
	return al, nil
}

// saveAllowlist atomically writes the allowlist to TOML via write-then-rename.
func saveAllowlist(path string, al *domain.Allowlist) error {
	entries := al.Entries()
	af := allowlistFile{Rules: make([]allowlistRule, 0, len(entries))}

	for jid, actions := range entries {
		strs := make([]string, len(actions))
		for i, a := range actions {
			strs[i] = a.String()
		}
		af.Rules = append(af.Rules, allowlistRule{
			JID:     jid.String(),
			Actions: strs,
		})
	}

	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600) //nolint:gosec // tmp path derived from validated config path
	if err != nil {
		return fmt.Errorf("saveAllowlist: create tmp: %w", err)
	}

	enc := toml.NewEncoder(f)
	if err := enc.Encode(af); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("saveAllowlist: encode: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("saveAllowlist: close tmp: %w", err)
	}

	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("saveAllowlist: rename: %w", err)
	}
	return nil
}

// watchAllowlist watches the allowlist file for changes via fsnotify on
// the parent directory (D3: atomic rename deletes the watched inode on
// macOS/kqueue) and SIGHUP as fallback. On change, it reloads the file
// into al. On parse error, it logs and keeps the previous valid state.
//
// The function blocks until ctx is cancelled.
func watchAllowlist(ctx context.Context, path string, al *domain.Allowlist, mu *sync.RWMutex, log *slog.Logger) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("watchAllowlist: %w", err)
	}
	defer func() { _ = watcher.Close() }()

	dir := filepath.Dir(path)
	base := filepath.Base(path)
	if err := watcher.Add(dir); err != nil {
		return fmt.Errorf("watchAllowlist: watch %s: %w", dir, err)
	}

	sighup := make(chan os.Signal, 1)
	signal.Notify(sighup, syscall.SIGHUP)
	defer signal.Stop(sighup)

	// Spec 016 H-014 / H-017 / T021: debouncer is now an explicit type
	// with deferred Stop, so a ctx-cancellation exit can never leak the
	// underlying time.Timer. Pre-T021 the timer was a bare local with
	// no deferred Stop — on a long-lived daemon ctx that never cancels
	// before exit, this was harmless, but on rapid restart loops the
	// drained-but-armed timer could outlive watchAllowlist briefly.
	deb := newAllowlistDebouncer(100 * time.Millisecond)
	defer deb.Stop()

	reload := func() {
		newAL, err := loadAllowlist(path)
		if err != nil {
			log.Error("allowlist reload failed, keeping previous", "err", err)
			return
		}
		mu.Lock()
		// Replace entries in-place: clear then re-grant.
		for jid, actions := range al.Entries() {
			al.Revoke(jid, actions...)
		}
		for jid, actions := range newAL.Entries() {
			al.Grant(jid, actions...)
		}
		mu.Unlock()
		log.Info("allowlist reloaded", "entries", newAL.Size())
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			if filepath.Base(ev.Name) != base {
				continue
			}
			deb.Trigger()
		case <-deb.C():
			reload()
		case <-sighup:
			reload()
		case err, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			log.Error("fsnotify error", "err", err)
		}
	}
}

// allowlistDebouncer wraps a single time.Timer with the
// stop-then-drain pattern needed by Go's pre-1.23 timer semantics
// (preserved for max compat with the project's go 1.22 toolchain
// floor). Trigger arms the timer to fire after `d`; if it was already
// armed, the firing is reset (the latest event wins, classic
// debounce). Stop is idempotent and drains the channel so a deferred
// Stop on Watch exit cannot leak.
type allowlistDebouncer struct {
	d time.Duration
	t *time.Timer
}

func newAllowlistDebouncer(d time.Duration) *allowlistDebouncer {
	t := time.NewTimer(d)
	if !t.Stop() {
		<-t.C
	}
	return &allowlistDebouncer{d: d, t: t}
}

func (b *allowlistDebouncer) C() <-chan time.Time { return b.t.C }

func (b *allowlistDebouncer) Trigger() {
	if !b.t.Stop() {
		select {
		case <-b.t.C:
		default:
		}
	}
	b.t.Reset(b.d)
}

func (b *allowlistDebouncer) Stop() {
	if !b.t.Stop() {
		select {
		case <-b.t.C:
		default:
		}
	}
}
