package network

import (
	"context"
	"errors"
	"log"
	"path/filepath"
	"sync"
	"time"

	"github.com/aldrin-isaac/newtron/pkg/newtron/audit"
	"github.com/fsnotify/fsnotify"
)

// DefaultWatchDebounce is the settle interval the spec-file watcher
// uses when an operator save produces a rapid burst of events
// (write + rename + write is typical for editor save flows). 1s is
// short enough to keep revocation latency low and long enough to
// absorb editor noise.
const DefaultWatchDebounce = time.Second

// ReloadFunc is the callback the watcher invokes for a watched
// path's networkID after the debounce window settles. The watcher
// runs the callback in its own goroutine; the callback is
// responsible for any state synchronization it needs.
type ReloadFunc func(networkID string) error

// SpecWatcher watches one or more network directories for changes and
// invokes a reload callback after debouncing rapid events
// (auth-design.md L6). Without it, an operator who edits
// network.json must POST /reload for the change to take effect;
// with it, the file save alone suffices within the debounce SLA.
//
// One watcher serves many networks: Add associates an absolute
// path with a networkID; events under that path debounce per path
// and fire one reload(networkID) call when the window settles.
type SpecWatcher struct {
	fsw      *fsnotify.Watcher
	logger   *log.Logger
	debounce time.Duration
	reload   ReloadFunc

	mu      sync.Mutex
	paths   map[string]string      // absolute path → networkID
	pending map[string]*time.Timer // absolute path → debounce timer

	stopCh chan struct{}
	doneCh chan struct{}
}

// NewSpecWatcher constructs a watcher. The returned watcher is
// inactive until Start runs. Zero debounce defaults to
// DefaultWatchDebounce; zero logger defaults to log.Default.
func NewSpecWatcher(logger *log.Logger, debounce time.Duration, reload ReloadFunc) (*SpecWatcher, error) {
	if reload == nil {
		return nil, errors.New("reload callback is required")
	}
	if logger == nil {
		logger = log.Default()
	}
	if debounce <= 0 {
		debounce = DefaultWatchDebounce
	}
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	return &SpecWatcher{
		fsw:      fsw,
		logger:   logger,
		debounce: debounce,
		reload:   reload,
		paths:    make(map[string]string),
		pending:  make(map[string]*time.Timer),
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
	}, nil
}

// Add begins watching specDir for the given networkID. The watcher
// monitors the directory itself plus the nodeSpecs/ subdirectory if
// present (where NodeSpec JSON files live; deletes there are
// part of revocation in the same way as grant edits to network.json).
//
// Returns an error if the watcher fails to register the path with
// the kernel (typically because the directory doesn't exist or the
// process is out of inotify watches).
func (w *SpecWatcher) Add(specDir, networkID string) error {
	abs, err := filepath.Abs(specDir)
	if err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, dup := w.paths[abs]; dup {
		return nil
	}
	if err := w.fsw.Add(abs); err != nil {
		return err
	}
	nodesDir := filepath.Join(abs, "nodes")
	if err := w.fsw.Add(nodesDir); err != nil {
		// NodeSpec dir is optional — log and continue. A network dir
		// without nodeSpecs/ is valid (every node-spec JSON
		// lives directly in specDir in some operator layouts).
		w.logger.Printf("spec-watcher: skip nodes subdir %s: %v", nodesDir, err)
	}
	w.paths[abs] = networkID
	return nil
}

// Remove stops watching specDir.
func (w *SpecWatcher) Remove(specDir string) error {
	abs, err := filepath.Abs(specDir)
	if err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, ok := w.paths[abs]; !ok {
		return nil
	}
	_ = w.fsw.Remove(abs)
	_ = w.fsw.Remove(filepath.Join(abs, "nodes"))
	delete(w.paths, abs)
	if timer, ok := w.pending[abs]; ok {
		timer.Stop()
		delete(w.pending, abs)
	}
	return nil
}

// Start runs the event loop until ctx is canceled or Stop is called.
// Returns immediately; the loop runs in a background goroutine.
// Safe to call once per watcher; multiple Start calls panic.
func (w *SpecWatcher) Start(ctx context.Context) {
	go w.loop(ctx)
}

// Stop terminates the event loop and closes the underlying watcher.
// Safe to call more than once; subsequent calls are no-ops.
func (w *SpecWatcher) Stop() {
	select {
	case <-w.stopCh:
		return
	default:
		close(w.stopCh)
	}
	<-w.doneCh
	_ = w.fsw.Close()

	w.mu.Lock()
	for path, timer := range w.pending {
		timer.Stop()
		delete(w.pending, path)
	}
	w.mu.Unlock()
}

// loop is the watcher's event-consumption goroutine. It reads events
// from the inotify channel and routes each one to the watched path
// the event's name falls under, scheduling a debounced reload.
func (w *SpecWatcher) loop(ctx context.Context) {
	defer close(w.doneCh)
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.stopCh:
			return
		case event, ok := <-w.fsw.Events:
			if !ok {
				return
			}
			w.handle(event)
		case err, ok := <-w.fsw.Errors:
			if !ok {
				return
			}
			w.logger.Printf("spec-watcher: %v", err)
		}
	}
}

// handle routes one fsnotify event to the watched path it belongs
// to. Events on the watched directory itself, on the nodeSpecs/
// subdirectory, and on files inside either all map to the same
// reload — the operator either edited the grant table or rotated
// a node spec, and both reasons mean "re-read the network dir".
//
// CHMOD-only events are ignored: editors sometimes set permissions
// on save without changing content, and a reload over chmod-only
// noise would burn cycles.
func (w *SpecWatcher) handle(event fsnotify.Event) {
	if event.Op == fsnotify.Chmod {
		return
	}
	dir := filepath.Dir(event.Name)
	w.mu.Lock()
	defer w.mu.Unlock()
	for path, networkID := range w.paths {
		// The network's own audit/ subtree is runtime output the server
		// writes into the network folder — not a spec change. Ignore it so
		// a logged mutation (or the audit/ dir's creation) never triggers a
		// reload. Checked before the reload match: the audit dir's creation
		// (event.Name == <path>/audit) would otherwise satisfy dir == path,
		// and a file within it (dir == <path>/audit) would satisfy
		// filepath.Dir(dir) == path.
		auditDir := filepath.Join(path, audit.AuditDirName)
		if event.Name == auditDir || dir == auditDir {
			return
		}
		// Event fires on the watched dir itself OR a subdirectory
		// of it (nodeSpecs/, etc.). Match by prefix to cover both.
		if dir == path || filepath.Dir(dir) == path {
			w.scheduleReload(path, networkID)
			return
		}
	}
}

// scheduleReload arms (or resets) the debounce timer for path. When
// the timer fires, the reload callback runs with networkID. Caller
// holds w.mu.
func (w *SpecWatcher) scheduleReload(path, networkID string) {
	if timer, ok := w.pending[path]; ok {
		timer.Reset(w.debounce)
		return
	}
	w.pending[path] = time.AfterFunc(w.debounce, func() {
		w.mu.Lock()
		delete(w.pending, path)
		w.mu.Unlock()
		if err := w.reload(networkID); err != nil {
			w.logger.Printf("spec-watcher: reload network '%s' after change at %s: %v", networkID, path, err)
		} else {
			w.logger.Printf("spec-watcher: reloaded network '%s' after change at %s", networkID, path)
		}
	})
}
