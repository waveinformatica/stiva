package api

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"registry/internal/digest"
	"registry/internal/registry"
)

const (
	mediaTypeOCIIndex = "application/vnd.oci.image.index.v1+json"
)

// referrerDescriptor is one entry of the referrers index.
type referrerDescriptor struct {
	MediaType    string            `json:"mediaType"`
	Digest       string            `json:"digest"`
	Size         int64             `json:"size"`
	ArtifactType string            `json:"artifactType,omitempty"`
	Annotations  map[string]string `json:"annotations,omitempty"`
}

// referrersIndex is the body of GET /v2/<repo>/referrers/<digest>.
type referrersIndex struct {
	SchemaVersion int                  `json:"schemaVersion"`
	MediaType     string               `json:"mediaType"`
	Manifests     []referrerDescriptor `json:"manifests"`
}

// referrers serves the OCI 1.1 Referrers API: an image index listing the
// manifests of repo whose subject is the given digest. An unknown subject is
// not an error: it simply has no referrers.
func (h *Handler) referrers(c *gin.Context, be registry.Backend, repoSegs []string, dgst string) {
	if c.Request.Method != http.MethodGet {
		c.Status(http.StatusMethodNotAllowed)
		return
	}
	repo := repoFrom(repoSegs)
	if !validRepo(repo) {
		c.JSON(http.StatusBadRequest, errorBody("NAME_INVALID", "invalid repository name"))
		return
	}
	d, err := digest.Parse(dgst)
	if err != nil {
		c.JSON(http.StatusBadRequest, errorBody("DIGEST_INVALID", "invalid digest"))
		return
	}
	refs, err := be.Referrers(repo, d)
	if err != nil {
		c.JSON(http.StatusInternalServerError, errorBody("INTERNAL_ERROR", err.Error()))
		return
	}
	filter := c.Query("artifactType")
	idx := referrersIndex{SchemaVersion: 2, MediaType: mediaTypeOCIIndex, Manifests: []referrerDescriptor{}}
	for _, r := range refs {
		if filter != "" && r.ArtifactType != filter {
			continue
		}
		idx.Manifests = append(idx.Manifests, referrerDescriptor{
			MediaType: r.MediaType, Digest: r.Digest.String(), Size: r.Size,
			ArtifactType: r.ArtifactType, Annotations: r.Annotations,
		})
	}
	if filter != "" {
		c.Header("OCI-Filters-Applied", "artifactType")
	}
	c.Header("Content-Type", mediaTypeOCIIndex)
	c.JSON(http.StatusOK, idx)
}
