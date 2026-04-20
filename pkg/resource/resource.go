// Package resource provides dynamic in-memory resource management with
// background refresh. Any shell (HTTP, RPC, runner) can use it alongside
// Pine to supply periodically-updated data to operators via context.
package resource

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Fetcher loads a resource value. Called by the background refresh loop.
// A nil return value with no error means the resource is intentionally empty.
type Fetcher func(ctx context.Context) (any, error)

// ResourceProvider is the read-only interface visible to operators.
type ResourceProvider interface {
	// Get returns the current value for a named resource.
	// Returns (nil, false) if the resource does not exist or is not yet loaded.
	Get(name string) (any, bool)
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

// Static is a trivial ResourceProvider backed by a fixed map.
type Static struct {
	data map[string]any
}

// NewStatic creates a Static provider from a map. Useful for tests.
func NewStatic(data map[string]any) *Static {
	return &Static{data: data}
}

func (s *Static) Get(name string) (any, bool) {
	v, ok := s.data[name]
	return v, ok
}

// --- Manager ---

type managedResource struct {
	name     string
	value    atomic.Value // holds the latest value (any)
	loaded   atomic.Bool  // true after first successful load
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
		if interval <= 0 {
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
		r.value.Store(val)
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

// Stop cancels all background refresh goroutines and waits for them to exit.
func (m *Manager) Stop() {
	m.mu.Lock()
	if !m.started {
		m.mu.Unlock()
		return
	}
	cancel := m.cancel
	m.mu.Unlock()

	cancel()
	m.wg.Wait()
}

// Get implements ResourceProvider. Lock-free read via atomic.Value.
func (m *Manager) Get(name string) (any, bool) {
	r, ok := m.resources[name]
	if !ok || !r.loaded.Load() {
		return nil, false
	}
	return r.value.Load(), true
}

func (m *Manager) refreshLoop(ctx context.Context, r *managedResource) {
	defer m.wg.Done()
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
			r.value.Store(val)
		}
	}
}
