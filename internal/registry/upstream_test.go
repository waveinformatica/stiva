package registry

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestParseBearerChallenge(t *testing.T) {
	ch := parseBearerChallenge(`Bearer realm="https://auth.docker.io/token",service="registry.docker.io",scope="repository:library/ubuntu:pull"`)
	if ch == nil {
		t.Fatal("expected a challenge, got nil")
	}
	if ch.realm != "https://auth.docker.io/token" || ch.service != "registry.docker.io" || ch.scope != "repository:library/ubuntu:pull" {
		t.Fatalf("unexpected challenge: %+v", ch)
	}
	if parseBearerChallenge("") != nil {
		t.Fatal("empty header must yield no challenge")
	}
	if parseBearerChallenge(`Basic realm="registry"`) != nil {
		t.Fatal("non-Bearer header must yield no challenge")
	}
	if parseBearerChallenge(`Bearer service="registry.docker.io"`) != nil {
		t.Fatal("Bearer without realm must yield no challenge")
	}
}

func TestUpstreamRepoName(t *testing.T) {
	cases := []struct{ base, repo, want string }{
		{"https://registry-1.docker.io", "ubuntu", "library/ubuntu"},
		{"https://registry-1.docker.io/", "ubuntu", "library/ubuntu"},
		{"https://registry.docker.io", "alpine", "library/alpine"},
		{"https://registry-1.docker.io", "myteam/myapp", "myteam/myapp"},
		{"https://registry-1.docker.io", "library/ubuntu", "library/ubuntu"},
		{"https://REGISTRY-1.DOCKER.IO", "ubuntu", "library/ubuntu"},
		{"https://gcr.io", "ubuntu", "ubuntu"},
		{"https://quay.io", "ubuntu", "ubuntu"},
		{"https://myreg:5000", "ubuntu", "ubuntu"},
		{"://bad-url", "ubuntu", "ubuntu"},
		{"", "ubuntu", "ubuntu"},
	}
	for _, c := range cases {
		if got := upstreamRepoName(c.base, c.repo); got != c.want {
			t.Errorf("upstreamRepoName(%q, %q) = %q, want %q", c.base, c.repo, got, c.want)
		}
	}
}

// TestBearerChallengeFlow replays the Docker Hub handshake against fakes: the
// registry answers 401 with a Bearer challenge until it sees a token, the
// token endpoint mints one. A second call must reuse the cached token.
func TestBearerChallengeFlow(t *testing.T) {
	const manifest = `{"schemaVersion":2,"mediaType":"application/vnd.docker.distribution.manifest.v2+json","config":{"digest":"sha256:abc"}}`
	var tokenHits atomic.Int64

	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenHits.Add(1)
		if r.URL.Query().Get("scope") != "repository:library/ubuntu:pull" {
			t.Errorf("token requested for scope %q", r.URL.Query().Get("scope"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"token":"fake-token","expires_in":300}`))
	}))
	defer tokenSrv.Close()

	var regHits atomic.Int64
	regSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		regHits.Add(1)
		if r.URL.Path != "/v2/library/ubuntu/manifests/latest" {
			t.Errorf("unexpected upstream path %q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer fake-token" {
			w.Header().Set("Www-Authenticate", `Bearer realm="`+tokenSrv.URL+`",service="test",scope="repository:library/ubuntu:pull"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/vnd.docker.distribution.manifest.v2+json")
		w.Write([]byte(manifest))
	}))
	defer regSrv.Close()

	up := NewUpstream(regSrv.URL, "", "", "", "")
	for i := 0; i < 2; i++ {
		body, mt, err := up.GetManifest("library/ubuntu", "latest")
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		if string(body) != manifest {
			t.Fatalf("call %d: unexpected body %q", i, body)
		}
		if mt != "application/vnd.docker.distribution.manifest.v2+json" {
			t.Fatalf("call %d: unexpected media type %q", i, mt)
		}
	}
	if got := tokenHits.Load(); got != 1 {
		t.Fatalf("token endpoint hit %d times, want 1 (second call must use the cache)", got)
	}
	// Each call starts unauthenticated (the scope is only known from the 401),
	// then replays with the token: 401 + retry, twice. The token itself is
	// fetched once, which is the expensive, rate-limited step.
	if got := regHits.Load(); got != 4 {
		t.Fatalf("registry hit %d times, want 4 (two 401 + two retries)", got)
	}
}

// TestUnauthorizedWithoutChallenge makes sure a plain 401 (no Bearer realm)
// still surfaces as an error exactly like before the handshake existed.
func TestUnauthorizedWithoutChallenge(t *testing.T) {
	regSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer regSrv.Close()

	up := NewUpstream(regSrv.URL, "", "", "", "")
	_, _, err := up.GetManifest("repo", "latest")
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("expected a 401 error, got %v", err)
	}
}

// TestStaticTokenSkipsChallenge ensures explicitly configured credentials keep
// working untouched: no challenge is answered when the first attempt succeeds.
func TestStaticTokenSkipsChallenge(t *testing.T) {
	var hits atomic.Int64
	regSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Header.Get("Authorization") != "Bearer static" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/vnd.docker.distribution.manifest.v2+json")
		w.Write([]byte(`{}`))
	}))
	defer regSrv.Close()

	up := NewUpstream(regSrv.URL, "", "", "static", "")
	if _, _, err := up.GetManifest("repo", "latest"); err != nil {
		t.Fatalf("static token request failed: %v", err)
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("registry hit %d times, want exactly 1", got)
	}
}
