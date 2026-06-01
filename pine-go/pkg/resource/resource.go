// Package resource provides dynamic in-memory resource management with
// background refresh. Any shell (HTTP, RPC, runner) can use it alongside
// Pine to supply periodically-updated data to operators via context.
package resource

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Fetcher loads a resource value. Called by the background refresh loop.
// A nil return value with no error means the resource is intentionally empty.
type Fetcher func(ctx context.Context) (any, error)

// ResourceHandle is a reference-counted borrow of a resource value. The holder
// must call Release exactly once when finished — idiomatically via `defer`
// immediately after a successful Get. The value returned by Value must not be
// retained or passed beyond the matching Release: once the last holder
// releases, a value implementing io.Closer is closed (on refresh or manager
// retirement), so using it afterwards is a use-after-close.
//
// This is the GC-language analogue of a C++ shared_ptr: holding a handle keeps
// the underlying value alive across a concurrent refresh/retire, and the value
// is torn down only after the final reference is dropped.
type ResourceHandle interface {
	// Value returns the borrowed resource value. Valid only until Release.
	Value() any
	// Release drops this borrow. Must be called exactly once.
	Release()
}

// ResourceProvider is the read-only interface visible to operators. Get returns
// a reference-counted handle that must be Released when the caller is done.
type ResourceProvider interface {
	// Get borrows the current value for a named resource, returning a handle the
	// caller must Release. Returns (nil, false) if the resource does not exist
	// or is not yet loaded.
	Get(name string) (ResourceHandle, bool)
}

// --- context helpers ---

type resourceCtxKey struct{}

// WithResources injects a ResourceProvider into a context.
// Shells call this once per request before engine.Execute.
func WithResources(ctx context.Context, rp ResourceProvider) context.Context {
	return context.WithValue(ctx, resourceCtxKey{}, rp)
}

// FromContext extracts the ResourceProvider from a context.
// Returns nil if none was injected.
func FromContext(ctx context.Context) ResourceProvider {
	rp, _ := ctx.Value(resourceCtxKey{}).(ResourceProvider)
	return rp
}

// --- Static provider (for testing) ---

// staticHandle is a no-op ResourceHandle: a Static provider never closes its
// values, so Release does nothing.
type staticHandle struct{ val any }

func (h staticHandle) Value() any { return h.val }
func (h staticHandle) Release()   {}

// Static is a trivial ResourceProvider backed by a fixed map.
type Static struct {
	data map[string]any
}

// NewStatic creates a Static provider from a map. Useful for tests.
func NewStatic(data map[string]any) *Static {
	return &Static{data: data}
}

func (s *Static) Get(name string) (ResourceHandle, bool) {
	v, ok := s.data[name]
	if !ok {
		return nil, false
	}
	return staticHandle{val: v}, true
}

// --- Manager ---

// refValue is a reference-counted resource value. The count starts at 1 (the
// Manager's own "live" reference). Each Get takes a borrow (+1); Release drops
// it (-1). Refresh replacing the value and Stop retiring the manager each drop
// the Manager's reference. When the count reaches zero — after the last
// borrower releases — a value implementing io.Closer is closed exactly once.
type refValue struct {
	val       any
	refs      atomic.Int64
	closeOnce sync.Once
}

func newRefValue(val any) *refValue {
	rv := &refValue{val: val}
	rv.refs.Store(1)
	return rv
}

// acquire takes a borrow if the value is still live (count > 0). Returns false
// once retirement has committed, so a stale slot can never be revived.
func (rv *refValue) acquire() bool {
	for {
		n := rv.refs.Load()
		if n <= 0 {
			return false
		}
		if rv.refs.CompareAndSwap(n, n+1) {
			return true
		}
	}
}

// release drops a borrow, closing the value when the final reference is gone.
func (rv *refValue) release() {
	if rv.refs.Add(-1) == 0 {
		rv.closeOnce.Do(func() {
			if c, ok := rv.val.(io.Closer); ok {
				if err := c.Close(); err != nil {
					log.Printf("[resource] close value: %v", err)
				}
			}
		})
	}
}

// refHandle is the ResourceHandle returned by Manager.Get.
type refHandle struct {
	rv       *refValue
	released bool
}

func (h *refHandle) Value() any { return h.rv.val }
func (h *refHandle) Release() {
	if h.released {
		return
	}
	h.released = true
	h.rv.release()
}

type managedResource struct {
	name     string
	value    atomic.Pointer[refValue] // holds the current reference-counted value
	loaded   atomic.Bool              // true after first successful load
	fetcher  Fetcher
	interval time.Duration
}

// Manager manages a set of named resources with background refresh.
// It implements ResourceProvider, so it can be injected directly into context.
type Manager struct {
	mu        sync.Mutex
	resources map[string]*managedResource
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	started   bool
	retired   bool
}

// NewManager creates an empty Manager. Register resources before calling Start.
func NewManager() *Manager {
	return &Manager{
		resources: make(map[string]*managedResource),
	}
}

// Register adds a named resource with its fetcher and refresh interval.
// Must be called before Start. Panics on duplicate name.
func (m *Manager) Register(name string, fetcher Fetcher, interval time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.started {
		panic("resource: Register called after Start")
	}
	if _, exists := m.resources[name]; exists {
		panic(fmt.Sprintf("resource: duplicate resource name %q", name))
	}
	m.resources[name] = &managedResource{
		name:     name,
		fetcher:  fetcher,
		interval: interval,
	}
}

// resourceConfig is the JSON schema for a single resource entry.
type resourceConfig struct {
	Type     string         `json:"type"`
	Interval int            `json:"interval"` // seconds
	Params   map[string]any `json:"params"`
}

// LoadFromRootConfig extracts resource_config from a unified Pine JSON config
// and registers each resource using the globally registered FetcherFactory.
// If resource_config is absent or empty, this is a no-op (pipeline may not use resources).
// Must be called before Start.
func (m *Manager) LoadFromRootConfig(data []byte) error {
	var root struct {
		ResourceConfig map[string]resourceConfig `json:"resource_config"`
	}
	if err := json.Unmarshal(data, &root); err != nil {
		return fmt.Errorf("resource: failed to parse config: %w", err)
	}
	if len(root.ResourceConfig) == 0 {
		return nil
	}
	return m.loadResources(root.ResourceConfig)
}

// loadResources is the shared implementation for registering resources from config.
func (m *Manager) loadResources(configs map[string]resourceConfig) error {
	for name, cfg := range configs {
		factory := lookupFactory(cfg.Type)
		if factory == nil {
			return fmt.Errorf("resource: unknown fetcher type %q for resource %q", cfg.Type, name)
		}
		fetcher, err := factory(cfg.Params)
		if err != nil {
			return fmt.Errorf("resource: failed to create fetcher for %q: %w", name, err)
		}
		interval := time.Duration(cfg.Interval) * time.Second
		switch {
		case cfg.Interval < 0:
			// Negative interval means "never refresh": fetch once at Start and
			// hold the value until retirement. Used by long-lived resources such
			// as connection pools that have no meaningful refresh.
			interval = -1
		case cfg.Interval == 0:
			interval = 10 * time.Minute // default
		}
		m.Register(name, fetcher, interval)
	}
	return nil
}

// Names returns the names of all registered resources, sorted alphabetically.
func (m *Manager) Names() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	names := make([]string, 0, len(m.resources))
	for name := range m.resources {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Start performs a synchronous initial load for all resources, then launches
// background refresh goroutines. Returns an error if any initial load fails.
func (m *Manager) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.started {
		return fmt.Errorf("resource: already started")
	}

	// Synchronous initial load
	for _, r := range m.resources {
		val, err := r.fetcher(ctx)
		if err != nil {
			return fmt.Errorf("resource: initial load of %q failed: %w", r.name, err)
		}
		r.value.Store(newRefValue(val))
		r.loaded.Store(true)
	}

	// Launch background refresh
	refreshCtx, cancel := context.WithCancel(ctx)
	m.cancel = cancel
	m.started = true

	for _, r := range m.resources {
		m.wg.Add(1)
		go m.refreshLoop(refreshCtx, r)
	}

	return nil
}

// Stop cancels all background refresh goroutines, waits for them to exit, then
// drops the Manager's reference on each current resource value. A value that
// implements io.Closer is closed once its last borrow (in-flight Get) is
// released — so teardown never races an in-flight request. Safe to call
// multiple times; only the first call drops the baseline references.
//
// Note: because closing is deferred to the last borrower, Stop cannot
// synchronously surface per-value Close errors; they are logged instead.
func (m *Manager) Stop() {
	m.mu.Lock()
	if !m.started {
		m.mu.Unlock()
		return
	}
	cancel := m.cancel
	alreadyRetired := m.retired
	m.retired = true
	m.mu.Unlock()

	cancel()
	m.wg.Wait()

	if alreadyRetired {
		return
	}
	for _, r := range m.resources {
		if rv := r.value.Load(); rv != nil {
			rv.release()
		}
	}
}

// Get implements ResourceProvider. It borrows the current value with a
// reference held; the caller must Release the returned handle. The acquire
// loop retries if a concurrent refresh swaps the value between the load and
// the reference bump.
func (m *Manager) Get(name string) (ResourceHandle, bool) {
	r, ok := m.resources[name]
	if !ok || !r.loaded.Load() {
		return nil, false
	}
	for {
		rv := r.value.Load()
		if rv == nil {
			return nil, false
		}
		if rv.acquire() {
			return &refHandle{rv: rv}, true
		}
		// The slot was retired between load and acquire; reload and retry.
		// If the manager itself was retired, the stored value stays put but its
		// count cannot be revived, so guard against an infinite spin.
		if m.isRetired() {
			return nil, false
		}
	}
}

func (m *Manager) isRetired() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.retired
}

func (m *Manager) refreshLoop(ctx context.Context, r *managedResource) {
	defer m.wg.Done()
	// A non-positive interval means "never refresh": the value fetched at Start
	// is held until the manager is stopped. This also defends time.NewTicker,
	// which panics on a non-positive duration.
	if r.interval <= 0 {
		<-ctx.Done()
		return
	}
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			val, err := r.fetcher(ctx)
			if err != nil {
				log.Printf("[resource] refresh %q failed: %v (keeping old value)", r.name, err)
				continue
			}
			old := r.value.Swap(newRefValue(val))
			if old != nil {
				// Drop the Manager's reference on the superseded value; it is
				// closed once any in-flight borrow of it is released.
				old.release()
			}
		}
	}
}
