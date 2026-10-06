package registry

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"registry/internal/storage"
)

// PinnedContextKey carries the registry name bound to a dedicated TCP listener
// (set by the server before handing the request to the router).
var PinnedContextKey = ctxKey("pinnedRegistry")

type ctxKey string

// Manager owns the set of registries and resolves incoming requests to the
// correct backend. Definitions live in PostgreSQL and can be edited from the UI;
// the in-memory view is rebuilt on every change so configuration takes effect
// without a restart.
type Manager struct {
	meta storage.MetadataStore
	// blobResolver maps a registry to the storage configuration of the named
	// blob store it references, with credentials resolved from the vault. It is
	// the only source of storage configuration: a registry that resolves to
	// nothing fails to load rather than falling back to a process-wide default.
	blobResolver BlobResolver
	uploadsDir   string

	mu        sync.RWMutex
	defs      map[string]*Registry
	stores    map[string]*storage.Store
	upstreams map[string]*Upstream
}

// BlobResolver returns the storage configuration a registry should use. The
// boolean reports whether the registry references a named store; false means it
// references none, which is an error rather than a cue to use a default.
type BlobResolver func(registry string) (storage.BlobConfig, bool, error)

// SetBlobResolver installs the resolver. It must be called before Load.
func (m *Manager) SetBlobResolver(r BlobResolver) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.blobResolver = r
}

// NewManager builds a registry manager. Storage is not configured here: each
// registry references a named blob store, resolved through SetBlobResolver.
func NewManager(meta storage.MetadataStore, uploadsDir string) *Manager {
	return &Manager{
		meta:       meta,
		uploadsDir: uploadsDir,
		defs:       make(map[string]*Registry),
		stores:     make(map[string]*storage.Store),
		upstreams:  make(map[string]*Upstream),
	}
}

// Load reads all registry definitions from the database. If none exist, a
// default hosted registry named "docker" is seeded so the server keeps working
// out of the box.
func (m *Manager) Load() error {
	recs, err := m.meta.ListRegistries()
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	// A fresh installation starts empty: no registry, no store, no credential.
	// Nothing is invented on the operator's behalf — everything is created
	// deliberately from the admin interface.
	return m.rebuildLocked(recs)
}

func (m *Manager) rebuildLocked(recs []storage.RegistryRecord) error {
	defs := make(map[string]*Registry)
	for _, rec := range recs {
		var r Registry
		if err := json.Unmarshal(rec.Config, &r); err != nil {
			return fmt.Errorf("registry %q: invalid config: %w", rec.Name, err)
		}
		r.Name = rec.Name
		if r.Format == "" {
			r.Format = string(FormatDocker)
		}
		r.Format = NormalizeFormat(r.Format)
		if err := r.Validate(); err != nil {
			return fmt.Errorf("registry %q: %w", rec.Name, err)
		}
		defs[r.Name] = &r
	}
	m.defs = defs

	// (Re)build per-registry stores and upstream clients.
	m.stores = make(map[string]*storage.Store)
	m.upstreams = make(map[string]*Upstream)
	for name, r := range defs {
		switch Type(r.Type) {
		case TypeHosted, TypeProxy, TypeCache:
			// Storage comes from the named store this registry references, and
			// from nowhere else. There is deliberately no fallback to a
			// process-wide configuration: a silent default is how credentials
			// end up somewhere nobody is looking.
			if m.blobResolver == nil {
				return fmt.Errorf("registry %q: no blob store resolver configured", name)
			}
			blob, ok, err := m.blobResolver(name)
			if err != nil {
				return fmt.Errorf("registry %q: blob store: %w", name, err)
			}
			if !ok {
				return fmt.Errorf("registry %q: references no blob store", name)
			}
			bs, err := storage.NewBlobStore(blob)
			if err != nil {
				return fmt.Errorf("registry %q: blob store: %w", name, err)
			}
			st, err := storage.NewStoreForRegistry(m.meta, bs, m.uploadsDir, name)
			if err != nil {
				return fmt.Errorf("registry %q: store: %w", name, err)
			}
			m.stores[name] = st
			if Type(r.Type) == TypeProxy {
				m.upstreams[name] = NewUpstream(r.RemoteURL, r.RemoteUser, r.RemotePass, r.RemoteToken, r.RemoteTokenURL)
			}
		case TypeGroup:
			// No local store; delegates to members.
		}
	}
	return nil
}

// persistLocked writes a registry definition to the database.
func (m *Manager) persistLocked(r *Registry) error {
	rec := storage.RegistryRecord{
		Name:   r.Name,
		Format: r.Format,
		Type:   r.Type,
		Config: mustJSON(r),
	}
	return m.meta.UpsertRegistry(rec)
}

// Get returns a registry definition by name.
func (m *Manager) Get(name string) (*Registry, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	r, ok := m.defs[name]
	return r, ok
}

// List returns all registry definitions, sorted by name.
func (m *Manager) List() []*Registry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*Registry, 0, len(m.defs))
	for _, r := range m.defs {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// StoreFor returns the per-registry store (hosted/proxy). Groups have no store.
func (m *Manager) StoreFor(name string) *storage.Store {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.stores[name]
}

// UpstreamFor returns the upstream client for a proxy registry.
func (m *Manager) UpstreamFor(name string) *Upstream {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.upstreams[name]
}

// BackendFor returns the OCI backend for a registry, building group delegation.
func (m *Manager) BackendFor(name string) (Backend, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	r, ok := m.defs[name]
	if !ok {
		return nil, fmt.Errorf("registry %q not found", name)
	}
	switch Type(r.Type) {
	case TypeHosted:
		st, ok := m.stores[name]
		if !ok {
			return nil, fmt.Errorf("registry %q store not ready", name)
		}
		return &hostedBackend{Store: st}, nil
	case TypeProxy:
		st, ok := m.stores[name]
		up, ok2 := m.upstreams[name]
		if !ok || !ok2 {
			return nil, fmt.Errorf("registry %q proxy not ready", name)
		}
		return newProxyBackend(st, up), nil
	case TypeCache:
		st, ok := m.stores[name]
		if !ok {
			return nil, fmt.Errorf("registry %q store not ready", name)
		}
		return newCacheBackend(st, m.resolveCache(r)), nil
	case TypeGroup:
		var members []Backend
		for _, mn := range r.Members {
			mb, err := m.backendLocked(mn)
			if err != nil {
				return nil, err
			}
			members = append(members, mb)
		}
		write := members[0]
		if r.WriteMember != "" {
			wb, err := m.backendLocked(r.WriteMember)
			if err != nil {
				return nil, err
			}
			write = wb
		}
		return &groupBackend{members: members, write: write}, nil
	default:
		return nil, fmt.Errorf("unknown registry type %q", r.Type)
	}
}

// ArtifactBackendFor returns the path-based backend for a registry (helm/maven/npm
// and other file formats). It mirrors BackendFor but speaks the ArtifactBackend
// interface instead of the OCI Backend.
func (m *Manager) ArtifactBackendFor(name string) (ArtifactBackend, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	r, ok := m.defs[name]
	if !ok {
		return nil, fmt.Errorf("registry %q not found", name)
	}
	switch Type(r.Type) {
	case TypeHosted:
		st, ok := m.stores[name]
		if !ok {
			return nil, fmt.Errorf("registry %q store not ready", name)
		}
		return &hostedArtifactBackend{Store: st}, nil
	case TypeProxy:
		st, ok := m.stores[name]
		up, ok2 := m.upstreams[name]
		if !ok || !ok2 {
			return nil, fmt.Errorf("registry %q proxy not ready", name)
		}
		return &proxyArtifactBackend{Store: st, up: up, allowWrite: r.ProxyAllowWrite}, nil
	case TypeCache:
		// Cache registries are docker/OCI only and served by the OCI API, not the
		// path-based artifact surface.
		return nil, fmt.Errorf("cache registries are docker/OCI only")
	case TypeGroup:
		var members []ArtifactBackend
		for _, mn := range r.Members {
			mb, err := m.artifactBackendLocked(mn)
			if err != nil {
				return nil, err
			}
			members = append(members, mb)
		}
		write := members[0]
		if r.WriteMember != "" {
			wb, err := m.artifactBackendLocked(r.WriteMember)
			if err != nil {
				return nil, err
			}
			write = wb
		}
		return &groupArtifactBackend{members: members, write: write}, nil
	default:
		return nil, fmt.Errorf("unknown registry type %q", r.Type)
	}
}

// artifactBackendLocked builds an artifact backend without re-locking.
func (m *Manager) artifactBackendLocked(name string) (ArtifactBackend, error) {
	r, ok := m.defs[name]
	if !ok {
		return nil, fmt.Errorf("registry %q not found", name)
	}
	switch Type(r.Type) {
	case TypeHosted:
		st, ok := m.stores[name]
		if !ok {
			return nil, fmt.Errorf("registry %q store not ready", name)
		}
		return &hostedArtifactBackend{Store: st}, nil
	case TypeProxy:
		st, ok := m.stores[name]
		up, ok2 := m.upstreams[name]
		if !ok || !ok2 {
			return nil, fmt.Errorf("registry %q proxy not ready", name)
		}
		return &proxyArtifactBackend{Store: st, up: up, allowWrite: r.ProxyAllowWrite}, nil
	case TypeCache:
		// Cache registries are docker/OCI only and served by the OCI API.
		return nil, fmt.Errorf("cache registries are docker/OCI only")
	case TypeGroup:
		return nil, fmt.Errorf("nested groups are not supported: %q", name)
	default:
		return nil, fmt.Errorf("unknown registry type %q", r.Type)
	}
}

// backendLocked builds a backend without re-locking (caller holds the lock).
func (m *Manager) backendLocked(name string) (Backend, error) {
	r, ok := m.defs[name]
	if !ok {
		return nil, fmt.Errorf("registry %q not found", name)
	}
	switch Type(r.Type) {
	case TypeHosted:
		st, ok := m.stores[name]
		if !ok {
			return nil, fmt.Errorf("registry %q store not ready", name)
		}
		return &hostedBackend{Store: st}, nil
	case TypeProxy:
		st, ok := m.stores[name]
		up, ok2 := m.upstreams[name]
		if !ok || !ok2 {
			return nil, fmt.Errorf("registry %q proxy not ready", name)
		}
		return newProxyBackend(st, up), nil
	case TypeCache:
		st, ok := m.stores[name]
		if !ok {
			return nil, fmt.Errorf("registry %q store not ready", name)
		}
		return newCacheBackend(st, m.resolveCache(r)), nil
	case TypeGroup:
		return nil, fmt.Errorf("nested groups are not supported: %q", name)
	default:
		return nil, fmt.Errorf("unknown registry type %q", r.Type)
	}
}

// Resolve maps an incoming request to a registry and to the registry-local
// path (the request path without the registry's base prefix). pinned, if
// non-empty, is the registry bound to the dedicated TCP listener that received
// the request: it wins unconditionally and speaks its native protocol at the
// listener root, ignoring any base path. Otherwise the candidates are the
// registries whose virtual hosts match, ordered by specificity: the longest
// matching base path wins, an empty base matches everything with the lowest
// priority. When no host matches, the single default registry catches the
// request. Resolution is deterministic: the same (host, path) always maps to
// the same registry — never a fallback chain, never a coin toss.
func (m *Manager) Resolve(host, pinned, reqPath string) (*Registry, string, error) {
	if pinned != "" {
		if r, ok := m.Get(pinned); ok {
			return r, reqPath, nil
		}
		return nil, "", fmt.Errorf("pinned registry %q not found", pinned)
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	reqPath = strings.Trim(reqPath, "/")
	var best *Registry
	bestLen := -1
	if host != "" {
		for _, r := range m.defs {
			if !r.HostMatch(host) {
				continue
			}
			if r.BasePath != "" && !basePathMatch(r.BasePath, reqPath) {
				continue
			}
			if len(r.BasePath) > bestLen {
				best, bestLen = r, len(r.BasePath)
			}
		}
	}
	if best != nil {
		return best, stripBasePath(best.BasePath, reqPath), nil
	}
	for _, r := range m.defs {
		if r.Default {
			return r, stripBasePath(r.BasePath, reqPath), nil
		}
	}
	return nil, "", fmt.Errorf("no registry matched host %q and no default is configured", host)
}

// checkRouteConflicts rejects definitions that would make routing ambiguous:
// two registries claiming the same (host, base path), or two defaults.
// Overlapping prefixes are fine (the longest wins); only identical routes
// collide, and only one registry may catch unmatched hosts.
func (m *Manager) checkRouteConflicts(r *Registry) error {
	for name, other := range m.defs {
		if name == r.Name {
			continue
		}
		if r.Default && other.Default {
			return fmt.Errorf("registry %q is already the default; only one registry may catch unmatched hosts", name)
		}
		if r.BasePath != other.BasePath {
			continue
		}
		for _, h := range r.Hosts {
			if other.HostMatch(h) {
				return fmt.Errorf("host %q with base path %q is already routed to registry %q", h, r.BasePath, name)
			}
		}
	}
	return nil
}

// resolveCache returns the upstream-resolution function for a cache registry.
// Given a cache repo path it produces the upstream client and the repo path as
// the upstream expects it (the registry host segment is stripped for non-Docker
// Hub upstreams). Resolution is deterministic:
//   - an explicit CacheUpstreams entry for the host segment wins (with its URL and
//     credentials);
//   - otherwise a host segment containing "." or ":" maps to https://<host>;
//   - otherwise the repo is host-less and resolves to CacheDefaultUpstream
//     (Docker Hub by default).
func (m *Manager) resolveCache(r *Registry) func(repo string) (*Upstream, string, error) {
	def := r.CacheDefaultUpstream
	if def == "" {
		def = DefaultCacheUpstream
	}
	def = strings.TrimRight(def, "/")
	ups := r.CacheUpstreams
	return func(repo string) (*Upstream, string, error) {
		first := repo
		if i := strings.Index(repo, "/"); i >= 0 {
			first = repo[:i]
		}
		if def2, ok := ups[first]; ok {
			scheme := "https"
			if def2.Insecure {
				scheme = "http"
			}
			base := def2.URL
			if base == "" {
				base = scheme + "://" + first
			}
			base = strings.TrimRight(base, "/")
			up := NewUpstream(base, def2.User, def2.Pass, def2.Token, "")
			return up, upstreamRepoName(base, strings.TrimPrefix(repo, first+"/")), nil
		}
		if strings.Contains(first, ".") || strings.Contains(first, ":") {
			base := "https://" + first
			up := NewUpstream(base, "", "", "", "")
			return up, upstreamRepoName(base, strings.TrimPrefix(repo, first+"/")), nil
		}
		up := NewUpstream(def, "", "", "", "")
		return up, upstreamRepoName(def, repo), nil
	}
}

// Create adds a new registry definition.
// Create persists a registry definition.
//
// link, when non-nil, runs after the definition is stored and before the
// manager reloads. That order is forced by design: a registry that references
// no blob store fails to load, so the store has to be attached in between the
// two writes rather than after them.
func (m *Manager) Create(r *Registry, link func(name string) error) error {
	if err := r.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.defs[r.Name]; exists {
		return fmt.Errorf("registry %q already exists", r.Name)
	}
	if err := m.checkRouteConflicts(r); err != nil {
		return err
	}
	if err := m.persistLocked(r); err != nil {
		return err
	}
	if link != nil {
		if err := link(r.Name); err != nil {
			return err
		}
	}
	return m.reloadLocked()
}

// Update replaces an existing registry definition.
func (m *Manager) Update(r *Registry, link func(name string) error) error {
	if err := r.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.defs[r.Name]; !exists {
		return fmt.Errorf("registry %q does not exist", r.Name)
	}
	if err := m.checkRouteConflicts(r); err != nil {
		return err
	}
	if err := m.persistLocked(r); err != nil {
		return err
	}
	if link != nil {
		if err := link(r.Name); err != nil {
			return err
		}
	}
	return m.reloadLocked()
}

// Delete removes a registry definition.
func (m *Manager) Delete(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.defs[name]; !exists {
		return fmt.Errorf("registry %q does not exist", name)
	}
	if err := m.meta.DeleteRegistry(name); err != nil {
		return err
	}
	return m.reloadLocked()
}

// reloadLocked re-reads all definitions after a mutation.
func (m *Manager) reloadLocked() error {
	recs, err := m.meta.ListRegistries()
	if err != nil {
		return err
	}
	return m.rebuildLocked(recs)
}

func mustJSON(v interface{}) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
