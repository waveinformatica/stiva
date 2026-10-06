package auth

import (
	"strings"

	"github.com/gin-gonic/gin"
)

// RequestScheme reports the scheme the client used to reach us.
//
// Behind a TLS-terminating proxy (an Ingress, an Istio gateway) the request
// arrives in cleartext, so r.TLS is nil and we would describe an https
// deployment as http. Two things the registry hands back are absolute URLs the
// client then follows — the upload Location of a push, and the realm of an
// authentication challenge — and both break, or worse, when the scheme is
// wrong: a realm advertised as http invites the client to send its Basic
// credentials over an unencrypted connection.
//
// Only the scheme is taken from the forwarded header. The host stays as
// received, so a spoofed X-Forwarded-Host cannot redirect an upload or point a
// credential-bearing token request at someone else.
func RequestScheme(c *gin.Context) string {
	scheme := "http"
	if c.Request.TLS != nil {
		scheme = "https"
	}
	if fp := c.GetHeader("X-Forwarded-Proto"); fp != "" {
		if i := strings.IndexByte(fp, ','); i >= 0 {
			fp = fp[:i] // may be a proxy chain: take the outermost
		}
		switch strings.TrimSpace(fp) {
		case "https":
			scheme = "https"
		case "http":
			scheme = "http"
		}
	}
	return scheme
}
