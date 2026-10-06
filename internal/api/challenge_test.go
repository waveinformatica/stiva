package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"registry/internal/auth"
	"registry/internal/authz"
)

// TestDenialChallengesTheUnauthenticated pins the distinction between "you may
// not" and "who are you?".
//
// An anonymous principal is resolved for every request that carries no
// credentials, so a caller who has simply not authenticated yet reaches the
// permission check looking like a known principal who is out of permissions.
// Answering 403 there breaks every client that waits to be asked: `docker push`
// opens with a HEAD on the blob, and a 403 with no WWW-Authenticate stops it
// before it ever sends the credentials it is holding.
func TestDenialChallengesTheUnauthenticated(t *testing.T) {
	h := &Handler{authz: authz.NewInMemory(nil, nil), authMgr: &auth.Manager{}}

	cases := []struct {
		name      string
		user      *auth.User
		status    int
		challenge bool
	}{
		{"no credentials at all", nil, http.StatusUnauthorized, true},
		{"matched an anonymous identity", &auth.User{Name: "anonymous", Anonymous: true}, http.StatusUnauthorized, true},
		{"signed in, but not permitted", &auth.User{Name: "bob"}, http.StatusForbidden, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			gin.SetMode(gin.TestMode)
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest("HEAD", "https://reg.example.com/v2/kosmos/app/blobs/sha256:abc", nil)
			if tc.user != nil {
				c.Set("user", tc.user)
			}

			h.denyAccess(c, "access denied to registry docker")

			if rec.Code != tc.status {
				t.Errorf("status = %d, atteso %d", rec.Code, tc.status)
			}
			got := rec.Header().Get("WWW-Authenticate") != ""
			if got != tc.challenge {
				t.Errorf("WWW-Authenticate presente = %v, atteso %v", got, tc.challenge)
			}
		})
	}
}

// TestChallengeRealmKeepsTheScheme guards the header that carries credentials.
// The registry sits behind a TLS-terminating gateway, so the request arrives in
// cleartext; advertising realm="http://..." would invite the client to send its
// Basic credentials over an unencrypted connection.
func TestChallengeRealmKeepsTheScheme(t *testing.T) {
	h := &Handler{authz: authz.NewInMemory(nil, nil), authMgr: &auth.Manager{}}
	rec := httptest.NewRecorder()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("HEAD", "http://reg.example.com/v2/kosmos/app/blobs/sha256:abc", nil)
	c.Request.Header.Set("X-Forwarded-Proto", "https")

	h.denyAccess(c, "denied")

	if got := rec.Header().Get("WWW-Authenticate"); !strings.Contains(got, `realm="https://`) {
		t.Errorf("realm non su https: %s", got)
	}
}
