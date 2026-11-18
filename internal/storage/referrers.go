package storage

import (
	"encoding/json"
	"errors"
	"fmt"

	"registry/internal/digest"
)

// ErrManifestInvalid is returned for a manifest that is not a JSON document or
// whose subject descriptor does not name a valid digest.
var ErrManifestInvalid = errors.New("manifest invalid")

// Media types of a single-platform image manifest.
const (
	MediaTypeOCIManifest    = "application/vnd.oci.image.manifest.v1+json"
	MediaTypeDockerManifest = "application/vnd.docker.distribution.manifest.v2+json"
)

// imageLayerTypes are the media types of filesystem layers. A manifest whose
// layers are all of these is an image; anything else (a cosign signature, an
// SBOM, a Helm chart) is an artifact that only looks like a manifest.
var imageLayerTypes = map[string]bool{
	"application/vnd.oci.image.layer.v1.tar":                       true,
	"application/vnd.oci.image.layer.v1.tar+gzip":                  true,
	"application/vnd.oci.image.layer.v1.tar+zstd":                  true,
	"application/vnd.docker.image.rootfs.diff.tar.gzip":            true,
	"application/vnd.oci.image.layer.nondistributable.v1.tar+gzip": true,
	"application/vnd.docker.image.rootfs.foreign.diff.tar.gzip":    true,
}

// ManifestInfo is what a manifest says about itself: the manifest it refers to
// (Subject, empty when none, OCI 1.1), the kind of artifact it is, and whether
// it is a container image that can be scanned.
type ManifestInfo struct {
	Subject      digest.Digest
	ArtifactType string
	Image        bool
}

// ParseManifestInfo reads a manifest stored with the given media type.
// The artifact type is the manifest's artifactType or, when that is absent,
// its config media type, as the Distribution spec requires.
func ParseManifestInfo(mediaType string, content []byte) (ManifestInfo, error) {
	var m struct {
		ArtifactType string `json:"artifactType"`
		Config       *struct {
			MediaType string `json:"mediaType"`
		} `json:"config"`
		Layers []struct {
			MediaType string `json:"mediaType"`
		} `json:"layers"`
		Subject *struct {
			Digest string `json:"digest"`
		} `json:"subject"`
	}
	if err := json.Unmarshal(content, &m); err != nil {
		return ManifestInfo{}, fmt.Errorf("%w: %v", ErrManifestInvalid, err)
	}
	var info ManifestInfo
	if m.Subject != nil {
		d, err := digest.Parse(m.Subject.Digest)
		if err != nil {
			return ManifestInfo{}, fmt.Errorf("%w: subject: %v", ErrManifestInvalid, err)
		}
		info.Subject = d
	}
	info.ArtifactType = m.ArtifactType
	if info.ArtifactType == "" && m.Config != nil {
		info.ArtifactType = m.Config.MediaType
	}
	if (mediaType == MediaTypeOCIManifest || mediaType == MediaTypeDockerManifest) && len(m.Layers) > 0 {
		info.Image = true
		for _, l := range m.Layers {
			if !imageLayerTypes[l.MediaType] {
				info.Image = false
				break
			}
		}
	}
	return info, nil
}

// Referrer is one manifest that refers to another through its subject: an
// entry of the referrers index.
type Referrer struct {
	Digest       digest.Digest
	MediaType    string
	ArtifactType string
	Size         int64
	Annotations  map[string]string
}

// manifestAnnotations reads the top-level annotations of a stored manifest.
// Only manifests with a subject are listed as referrers, and those were parsed
// as JSON when they were stored: a decoding error is a corrupted row.
func manifestAnnotations(content []byte) (map[string]string, error) {
	var m struct {
		Annotations map[string]string `json:"annotations"`
	}
	if err := json.Unmarshal(content, &m); err != nil {
		return nil, fmt.Errorf("stored manifest is not JSON: %w", err)
	}
	return m.Annotations, nil
}
