package api

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"sort"
	"strings"

	"registry/internal/digest"
	"registry/internal/registry"
)

// maxDockerfileBlob caps attestation/config reads: both are small JSON docs,
// and a corrupt size must never blow up a UI read.
const maxDockerfileBlob = 4 << 20

// dockerfileResult is what the UI shows for "where did this image come from".
type dockerfileResult struct {
	Content string
	Source  string // "attestation", "history", "" when unavailable
}

// resolveDockerfile returns the Dockerfile that built the image at ref.
// First choice is the real file from the build attestation buildx embeds in
// the push; fallback is a reconstruction from the image config history.
// Empty content with no error means neither is available.
// be is the registry backend, not the bare store: for proxy and cache
// registries blob reads fall through to the upstream and warm the local
// cache, so a Dockerfile resolves on first view even when nothing was ever
// pulled through this server before.
func resolveDockerfile(be registry.Backend, repo, ref string) dockerfileResult {
	content, _, err := be.GetManifest(repo, ref)
	if err != nil {
		return dockerfileResult{}
	}
	var idx struct {
		Manifests []struct {
			Digest      string            `json:"digest"`
			Annotations map[string]string `json:"annotations"`
		} `json:"manifests"`
		Config struct {
			Digest string `json:"digest"`
		} `json:"config"`
	}
	if err := json.Unmarshal(content, &idx); err != nil {
		return dockerfileResult{}
	}
	for _, m := range idx.Manifests {
		if m.Annotations["vnd.docker.reference.type"] != "attestation-manifest" {
			continue
		}
		if df := attestationDockerfile(be, repo, m.Digest); df != "" {
			return dockerfileResult{Content: df, Source: "attestation"}
		}
	}
	// No attestation on record: fall back to the config history. An index
	// has no config of its own, so descend into the platform manifest
	// (amd64 first — that is what single-arch buildx pushes carry).
	if df := historyDockerfile(be, repo, idx.Config.Digest); df != "" {
		return dockerfileResult{Content: df, Source: "history"}
	}
	for _, m := range platformManifests(content) {
		mc, _, err := be.GetManifest(repo, m)
		if err != nil {
			continue
		}
		var child struct {
			Config struct {
				Digest string `json:"digest"`
			} `json:"config"`
		}
		if json.Unmarshal(mc, &child) != nil || child.Config.Digest == "" {
			continue
		}
		if df := historyDockerfile(be, repo, child.Config.Digest); df != "" {
			return dockerfileResult{Content: df, Source: "history"}
		}
	}
	return dockerfileResult{}
}

// platformManifests lists the child digests of an index, platform-bearing
// (amd64 first) before platform-less ones like attestations.
func platformManifests(indexContent []byte) []string {
	var idx struct {
		Manifests []struct {
			Digest   string `json:"digest"`
			Platform *struct {
				Architecture string `json:"architecture"`
				OS           string `json:"os"`
			} `json:"platform"`
		} `json:"manifests"`
	}
	if json.Unmarshal(indexContent, &idx) != nil {
		return nil
	}
	var amd64, other, rest []string
	for _, m := range idx.Manifests {
		if m.Digest == "" {
			continue
		}
		switch {
		case m.Platform != nil && m.Platform.Architecture == "amd64":
			amd64 = append(amd64, m.Digest)
		case m.Platform != nil:
			other = append(other, m.Digest)
		default:
			rest = append(rest, m.Digest)
		}
	}
	return append(append(amd64, other...), rest...)
}

// attestationDockerfile reads the Dockerfile out of the SLSA provenance
// attached to the index: attestation manifest -> dsse blob -> statement ->
// predicate.buildDefinition.definition (base64).
func attestationDockerfile(be registry.Backend, repo, digestRef string) string {
	d, err := digest.Parse(digestRef)
	if err != nil {
		return ""
	}
	mc, _, err := be.GetManifest(repo, d.String())
	if err != nil {
		return ""
	}
	var am struct {
		Layers []struct {
			Digest string `json:"digest"`
			Size   int64  `json:"size"`
		} `json:"layers"`
	}
	if err := json.Unmarshal(mc, &am); err != nil {
		return ""
	}
	// Smallest first: SLSA provenance (kilobytes, carries the Dockerfile)
	// sorts before SBOM documents (megabytes, never does).
	layers := append([]struct {
		Digest string `json:"digest"`
		Size   int64  `json:"size"`
	}{}, am.Layers...)
	sort.Slice(layers, func(i, j int) bool { return layers[i].Size < layers[j].Size })
	for _, l := range layers {
		if df := dsseDockerfile(be, repo, l.Digest); df != "" {
			return df
		}
	}
	return ""
}

// dsseDockerfile extracts the embedded Dockerfile from one attestation blob.
// BuildKit stores a DSSE envelope around an in-toto statement; a bare
// statement is accepted too.
func dsseDockerfile(be registry.Backend, repo, digestRef string) string {
	d, err := digest.Parse(digestRef)
	if err != nil {
		return ""
	}
	rc, _, err := be.GetBlob(repo, d)
	if err != nil {
		return ""
	}
	defer rc.Close()
	raw, err := io.ReadAll(io.LimitReader(rc, maxDockerfileBlob))
	if err != nil {
		return ""
	}
	return dockerfileFromStatement(raw)
}

// dockerfileFromStatement pulls the base64 Dockerfile out of a DSSE envelope
// or a bare in-toto statement. Pure: unit-tested.
func dockerfileFromStatement(raw []byte) string {
	stmt := raw
	var env struct {
		Payload string `json:"payload"`
	}
	if json.Unmarshal(raw, &env) == nil && env.Payload != "" {
		p, err := decodeB64(env.Payload)
		if err != nil {
			return ""
		}
		stmt = p
	}
	var stt struct {
		Predicate struct {
			BuildDefinition struct {
				Definition string `json:"definition"`
			} `json:"buildDefinition"`
		} `json:"predicate"`
	}
	if json.Unmarshal(stmt, &stt) != nil || stt.Predicate.BuildDefinition.Definition == "" {
		return ""
	}
	df, err := decodeB64(stt.Predicate.BuildDefinition.Definition)
	if err != nil {
		return ""
	}
	return string(df)
}

// historyDockerfile rebuilds a Dockerfile from the image config history.
// Instructions are faithful; ARG values, comments and blank lines are not.
func historyDockerfile(be registry.Backend, repo, cfgDigestRef string) string {
	d, err := digest.Parse(cfgDigestRef)
	if err != nil {
		return ""
	}
	rc, _, err := be.GetBlob(repo, d)
	if err != nil {
		return ""
	}
	defer rc.Close()
	raw, err := io.ReadAll(io.LimitReader(rc, maxDockerfileBlob))
	if err != nil {
		return ""
	}
	return dockerfileFromHistory(raw)
}

// dockerfileFromHistory renders config history entries as Dockerfile lines.
// Pure: unit-tested.
func dockerfileFromHistory(raw []byte) string {
	var cfg struct {
		History []struct {
			CreatedBy string `json:"createdBy"`
			Comment   string `json:"comment"`
		} `json:"history"`
	}
	if json.Unmarshal(raw, &cfg) != nil || len(cfg.History) == 0 {
		return ""
	}
	var b strings.Builder
	for _, h := range cfg.History {
		cmd := strings.TrimSpace(h.CreatedBy)
		if cmd == "" {
			continue
		}
		// Shell form: "/bin/sh -c #(nop)  CMD" is already an instruction,
		// "/bin/sh -c CMD" is a RUN step.
		if rest, ok := strings.CutPrefix(cmd, "/bin/sh -c "); ok {
			cmd = strings.TrimSpace(rest)
			if inner, ok := strings.CutPrefix(cmd, "#(nop) "); ok {
				cmd = strings.TrimSpace(inner)
			} else {
				cmd = "RUN " + cmd
			}
		}
		b.WriteString(cmd + "\n")
	}
	return strings.TrimSpace(b.String())
}

// decodeB64 tries the base64 flavors met in the wild (producers disagree on
// padding and alphabet).
func decodeB64(s string) ([]byte, error) {
	if p, err := base64.StdEncoding.DecodeString(s); err == nil {
		return p, nil
	}
	if p, err := base64.RawStdEncoding.DecodeString(s); err == nil {
		return p, nil
	}
	return base64.RawURLEncoding.DecodeString(s)
}
