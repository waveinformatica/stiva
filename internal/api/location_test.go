package api

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestLocationScheme covers the upload URL handed to OCI clients. Behind a
// TLS-terminating gateway the request is cleartext, so without honouring
// X-Forwarded-Proto every push would be pointed at http:// and fail.
func TestLocationScheme(t *testing.T) {
	cases := []struct {
		name, forwarded, want string
		tls                   bool
	}{
		{name: "plaintext, nessun header", want: "http://reg.example.com/v2/repo/blobs/uploads/abc"},
		{name: "dietro gateway TLS", forwarded: "https", want: "https://reg.example.com/v2/repo/blobs/uploads/abc"},
		{name: "catena di proxy", forwarded: "https, http", want: "https://reg.example.com/v2/repo/blobs/uploads/abc"},
		{name: "header spazioso", forwarded: " https ", want: "https://reg.example.com/v2/repo/blobs/uploads/abc"},
		{name: "valore non valido ignorato", forwarded: "gopher", want: "http://reg.example.com/v2/repo/blobs/uploads/abc"},
	}

	h := &Handler{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "http://reg.example.com/v2/repo/blobs/uploads/", nil)
			c.Request.Host = "reg.example.com"
			if tc.forwarded != "" {
				c.Request.Header.Set("X-Forwarded-Proto", tc.forwarded)
			}
			if got := h.location(c, "repo", "blobs/uploads/abc"); got != tc.want {
				t.Fatalf("ottenuto %q, atteso %q", got, tc.want)
			}
		})
	}
}

// TestLocationIgnoresForwardedHost pins that the host is never taken from a
// caller-supplied header: an attacker must not be able to redirect an upload.
func TestLocationIgnoresForwardedHost(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "http://reg.example.com/v2/repo/blobs/uploads/", nil)
	c.Request.Host = "reg.example.com"
	c.Request.Header.Set("X-Forwarded-Host", "evil.example.net")

	h := &Handler{}
	if got := h.location(c, "repo", "blobs/uploads/abc"); got != "http://reg.example.com/v2/repo/blobs/uploads/abc" {
		t.Fatalf("host preso da header non fidato: %q", got)
	}
}
