package registry

import (
	"fmt"
	"net/url"
	"strings"

	"registry/internal/storage"
)

// Type is the strategy a registry uses to serve content.
type Type string

const (
	TypeHosted Type = "hosted"
	TypeProxy  Type = "proxy"
	TypeGroup  Type = "group"
	TypeCache  Type = "cache"
)

// Format is the artifact protocol a registry speaks.
type Format string

const (
	// FormatDocker is the OCI Distribution API. The value is "oci" because that
	// is the name of the standard; "docker" was the old spelling and is still
	// accepted on read — see NormalizeFormat.
	FormatDocker    Format = "oci"       // OCI Distribution API (images)
	FormatHelm      Format = "helm"      // Helm chart repositories (index.yaml + .tgz)
	FormatMaven     Format = "maven"     // Maven repositories (path-addressed jars/poms)
	FormatNpm       Format = "npm"       // npm registries (metadata + .tgz tarballs)
	FormatPyPI      Format = "pypi"      // Python (simple index + wheels/sdists)
	FormatGo        Format = "go"        // Go modules (GOPROXY layout)
	FormatRaw       Format = "raw"       // Generic binary store (no generated metadata)
	FormatNuGet     Format = "nuget"     // NuGet (v3 flat container + .nupkg)
	FormatRubyGems  Format = "rubygems"  // RubyGems (.gem); hosted index deferred
	FormatComposer  Format = "composer"  // PHP Composer (p2 JSON)
	FormatConda     Format = "conda"     // Conda (repodata.json)
	FormatAPT       Format = "apt"       // Debian/Ubuntu (.deb + Packages/Release)
	FormatYUM       Format = "yum"       // RPM distros (.rpm + repodata/repomd.xml)
	FormatConan     Format = "conan"     // C/C++ (Conan; store/proxy/group, no hosted API)
	FormatCocoaPods Format = "cocoapods" // iOS/macOS (podspec JSON)
	FormatCRAN      Format = "cran"      // R packages (PACKAGES index)
	FormatELPA      Format = "elpa"      // Emacs packages (archive-contents)
	FormatP2        Format = "p2"        // Eclipse p2 (store/proxy/group)
	FormatOpkg      Format = "opkg"      // embedded Linux (.ipk + Packages)
	FormatChef      Format = "chef"      // Chef cookbooks (store-only)
	FormatPuppet    Format = "puppet"    // Puppet modules (store-only)
	FormatVagrant   Format = "vagrant"   // Vagrant boxes (store-only)
	FormatSBT       Format = "sbt"       // Scala SBT (store-only)
	FormatIvy       Format = "ivy"       // Ivy (store-only)
	FormatGradle    Format = "gradle"    // Gradle (store-only)
	FormatGitLFS    Format = "git-lfs"   // Git LFS (store-only)
)

// UpstreamDef describes one upstream for a cache (pull-through) registry. The
// map key is the registry host as it appears in the image/repo path (e.g.
// "gcr.io", "myreg:5000"). When a key is absent the host is derived as
// https://<host> with no credentials (public registry).
type UpstreamDef struct {
	URL      string `json:"url"`      // base URL; empty => https://<host>
	Insecure bool   `json:"insecure"` // use http:// instead of https://
	User     string `json:"user"`     // basic-auth / bearer user
	Pass     string `json:"pass"`     // basic-auth / bearer password
	Token    string `json:"token"`    // static bearer token (overrides user/pass)
}

// DefaultCacheUpstream is the upstream used for host-less repos (Docker Hub
// official and namespaced images). It is deterministic so the cache never has to
// guess: a repo without a registry host segment is always Docker Hub.
const DefaultCacheUpstream = "https://registry-1.docker.io"

// Registry is the full configuration of one registry (hosted, proxy or group).
// Everything here is editable from the web UI except the global PostgreSQL DSN.
type Registry struct {
	Name    string `json:"name"`
	Format  string `json:"format"`  // "oci", "helm", "maven", …
	Type    string `json:"type"`    // hosted | proxy | group
	Online  bool   `json:"online"`  // offline registries reject all access
	Default bool   `json:"default"` // default target for HTTP host routing

	// Hosts are virtual hostnames that route to this registry on the shared
	// HTTP listener (e.g. "docker.internal:5000").
	Hosts []string `json:"hosts"`
	// Port is a dedicated TCP listener bound exclusively to this registry.
	Port int `json:"port"`
	// BasePath scopes a registry to a URL prefix on its hosts
	// (e.g. "maven-central" serves https://host/maven-central/...). It lets
	// several path-addressed registries share one host: the longest matching
	// prefix wins, an empty base matches everything with the lowest priority.
	// OCI clients speak /v2 at the host root, so OCI registries always keep
	// the empty base and need a host or port of their own.
	BasePath string `json:"base_path"`
	// OCI clients speak /v2 at the host root, so OCI registries always keep
	// the empty base and need a host or port of their own. Dedicated ports
	// ignore the base path: a pinned listener speaks the native protocol at
	// its root.

	// Blob storage (hosted + proxy cache).
	Blob storage.BlobConfig `json:"blob"`

	// Proxy-specific.
	RemoteURL       string `json:"remote_url"`        // upstream base, e.g. https://registry-1.docker.io
	RemoteUser      string `json:"remote_user"`       // basic-auth username for upstream
	RemotePass      string `json:"remote_pass"`       // basic-auth password for upstream
	RemoteToken     string `json:"remote_token"`      // static bearer token for upstream
	RemoteTokenURL  string `json:"remote_token_url"`  // upstream token endpoint (OIDC/Docker)
	ProxyAllowWrite bool   `json:"proxy_allow_write"` // write-through to upstream (UI toggle)

	// Cache-specific (transparent multi-upstream pull-through, K8s mirror).
	CacheDefaultUpstream string                 `json:"cache_default_upstream"` // base for host-less repos; empty => Docker Hub
	CacheUpstreams       map[string]UpstreamDef `json:"cache_upstreams"`        // host -> upstream definition

	// Group-specific.
	Members     []string `json:"members"`      // ordered member registry names (read aggregation)
	WriteMember string   `json:"write_member"` // member that receives writes
}

// Validate checks a registry definition for internal consistency.
// NormalizeFormat maps historical spellings onto the current ones. A stored
// definition written before the rename says "docker"; rather than depending on
// a data migration having run everywhere, the value is normalised on the way
// in, so an un-migrated row still resolves to the right format.
func NormalizeFormat(f string) string {
	if f == "docker" {
		return string(FormatDocker)
	}
	return f
}

func (r *Registry) Validate() error {
	if r.Name == "" {
		return fmt.Errorf("registry name is required")
	}
	if !validName(r.Name) {
		return fmt.Errorf("registry name %q is invalid (use [a-z0-9-]+)", r.Name)
	}
	r.BasePath = normalizeBasePath(r.BasePath)
	if !validBasePath(r.BasePath) {
		return fmt.Errorf("base path %q is invalid (use URL path segments, no parent escapes, first segment must not be v2 or api)", r.BasePath)
	}
	if r.BasePath != "" && Format(NormalizeFormat(r.Format)) == FormatDocker {
		return fmt.Errorf("base path %q needs a path-addressed format: OCI clients speak /v2 at the host root, so OCI registries take a host or a port instead", r.BasePath)
	}
	switch Type(r.Type) {
	case TypeHosted:
		if !validFormat(r.Format) {
			return fmt.Errorf("unsupported format %q", r.Format)
		}
	case TypeProxy:
		if r.RemoteURL == "" {
			return fmt.Errorf("proxy registry requires remote_url")
		}
	case TypeCache:
		// The cache type is the Kubernetes/containerd pull-through image cache:
		// docker (OCI) only. Other formats use proxy/hosted/group instead.
		if Format(r.Format) != FormatDocker {
			return fmt.Errorf("cache registries support the docker (OCI) format only")
		}
	case TypeGroup:
		if len(r.Members) == 0 {
			return fmt.Errorf("group registry requires at least one member")
		}
		if r.WriteMember != "" {
			found := false
			for _, m := range r.Members {
				if m == r.WriteMember {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("write_member %q is not in members", r.WriteMember)
			}
		}
	default:
		return fmt.Errorf("unknown registry type %q", r.Type)
	}
	return nil
}

// HostMatch reports whether the given Host header (host or host:port) routes to
// this registry. Matching is exact and case-insensitive on the host portion.
func (r *Registry) HostMatch(host string) bool {
	if host == "" {
		return false
	}
	h := strings.ToLower(strings.Split(host, ":")[0])
	for _, cand := range r.Hosts {
		if strings.ToLower(strings.Split(cand, ":")[0]) == h {
			return true
		}
	}
	return false
}

func validName(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z') && !(c >= '0' && c <= '9') && c != '-' {
			return false
		}
	}
	return true
}

// reservedBaseFirst blocks prefixes that would swallow the server's own
// roots: /v2 is the OCI API, /api serves the UI and its backend.
var reservedBaseFirst = map[string]bool{"v2": true, "api": true}

// normalizeBasePath canonicalizes a configured base path: no leading or
// trailing slashes, no empty segments. The stored form is what routing
// matches against, so "​/maven-central/" and "maven-central" are one prefix.
func normalizeBasePath(s string) string {
	segs := make([]string, 0, 4)
	for _, seg := range strings.Split(s, "/") {
		if seg == "" || seg == "." {
			continue
		}
		segs = append(segs, seg)
	}
	return strings.Join(segs, "/")
}

// validBasePath reports whether s is a usable base path. The empty base is
// always valid: it is the legacy catch-all mode with the lowest routing
// priority.
func validBasePath(s string) bool {
	s = normalizeBasePath(s)
	if s == "" {
		return true
	}
	if len(s) > 128 {
		return false
	}
	for _, seg := range strings.Split(s, "/") {
		if seg == ".." {
			return false
		}
		for _, c := range seg {
			ok := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
				c == '-' || c == '_' || c == '.' || c == '~'
			if !ok {
				return false
			}
		}
	}
	if reservedBaseFirst[strings.ToLower(strings.Split(s, "/")[0])] {
		return false
	}
	return true
}

// basePathMatch reports whether reqPath falls under base: an exact match or a
// full-segment prefix. "maven" matches "maven" and "maven/a.jar" but never
// "mavenx/a.jar". Both sides are slash-trimmed request paths; matching is
// case-sensitive because artifact coordinates are. An empty base matches
// everything.
func basePathMatch(base, reqPath string) bool {
	if base == "" {
		return true
	}
	if reqPath == base {
		return true
	}
	return strings.HasPrefix(reqPath, base+"/")
}

// stripBasePath removes a matching base prefix, returning the registry-local
// path the backends store and serve. It only strips after basePathMatch, so a
// non-matching path is returned unchanged rather than mangled.
func stripBasePath(base, reqPath string) string {
	if base == "" {
		return reqPath
	}
	if reqPath == base {
		return ""
	}
	if strings.HasPrefix(reqPath, base+"/") {
		return strings.TrimPrefix(reqPath, base+"/")
	}
	return reqPath
}

// validFormat reports whether the registry supports the given artifact format.
func validFormat(f string) bool {
	switch Format(f) {
	case FormatDocker, FormatHelm, FormatMaven, FormatNpm,
		FormatPyPI, FormatGo, FormatRaw, FormatNuGet, FormatRubyGems,
		FormatComposer, FormatConda, FormatAPT, FormatYUM,
		FormatConan, FormatCocoaPods, FormatCRAN, FormatELPA, FormatP2,
		FormatOpkg, FormatChef, FormatPuppet, FormatVagrant, FormatSBT,
		FormatIvy, FormatGradle, FormatGitLFS:
		return true
	}
	return false
}

// NormalizeImage converts a container image reference (as used in Kubernetes
// Pod specs) into the (repo, reference) pair the OCI API and the cache expect.
// This is the single source of truth shared by the cache resolver and the
// pre-warm controller so an image string always maps to exactly one repo path.
//
// Rules (deterministic, no fallback chains):
//   - a registry host is present when the first path segment contains "." or ":"
//     or equals "localhost"/"docker.io"/"registry.k8s.io";
//   - Docker Hub official images (no namespace, e.g. "alpine") get the
//     "library/" prefix; "docker.io" is stripped from the path;
//   - any other host is kept verbatim in the repo path (e.g. "gcr.io/x/y");
//   - a tag defaults to "latest" when only a digest or nothing is given.
func NormalizeImage(image string) (repo, ref string, err error) {
	if image == "" {
		return "", "", fmt.Errorf("empty image reference")
	}
	s := image
	if i := strings.Index(s, "@"); i >= 0 {
		ref = s[i+1:]
		if !strings.HasPrefix(ref, "sha256:") {
			return "", "", fmt.Errorf("unsupported digest %q (only sha256 supported)", ref)
		}
		s = s[:i]
	}
	if ref == "" {
		if i := strings.LastIndex(s, ":"); i >= 0 {
			if !strings.Contains(s[i+1:], "/") {
				ref = s[i+1:]
				s = s[:i]
			}
		}
	}
	if ref == "" {
		ref = "latest"
	}
	parts := strings.SplitN(s, "/", 2)
	first := parts[0]
	if isRegistryHost(first) {
		host := first
		rest := ""
		if len(parts) > 1 {
			rest = parts[1]
		}
		if host == "docker.io" {
			repo = dockerRepo(rest)
		} else {
			repo = host + "/" + rest
		}
	} else {
		repo = dockerRepo(s)
	}
	return repo, ref, nil
}

func isRegistryHost(seg string) bool {
	if seg == "" {
		return false
	}
	if seg == "localhost" || seg == "docker.io" || seg == "registry.k8s.io" {
		return true
	}
	return strings.Contains(seg, ".") || strings.Contains(seg, ":")
}

func dockerRepo(s string) string {
	if s == "" {
		return "library"
	}
	if strings.Contains(s, "/") {
		return s
	}
	return "library/" + s
}

// isDockerHubHost reports whether a registry host serves Docker Hub, where
// official images live under the implicit "library/" namespace.
func isDockerHubHost(host string) bool {
	switch strings.ToLower(host) {
	case "registry-1.docker.io", "registry.docker.io", "docker.io", "index.docker.io":
		return true
	}
	return false
}

// upstreamRepoName maps a client-side repo path onto the name the upstream
// registry expects. Only Docker Hub renames repositories (a host-less name
// gains the implicit "library/" namespace); every other upstream is addressed
// verbatim, so proxies stay transparent for registries that need no mapping.
func upstreamRepoName(base, repo string) string {
	u, err := url.Parse(base)
	if err != nil {
		return repo
	}
	if isDockerHubHost(u.Hostname()) && !strings.Contains(repo, "/") {
		return "library/" + repo
	}
	return repo
}
