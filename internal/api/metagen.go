package api

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"

	"registry/internal/registry"
)

// generateMetadataFor produces format-specific metadata documents for hosted
// registries. It returns (body, contentType, handled). When handled is false the
// request falls through to ordinary object serving (the upstream's own metadata
// is served for proxy/group/cache).
func generateMetadataFor(be registry.ArtifactBackend, format registry.Format, p string) ([]byte, string, bool) {
	switch format {
	case registry.FormatPyPI:
		return genPyPI(be, p)
	case registry.FormatGo:
		return genGo(be, p)
	case registry.FormatNuGet:
		return genNuGet(be, p)
	case registry.FormatComposer:
		return genComposer(be, p)
	case registry.FormatConda:
		return genConda(be, p)
	case registry.FormatAPT:
		return genAPT(be, p)
	case registry.FormatYUM:
		return genYUM(be, p)
	case registry.FormatCRAN:
		return genCRAN(be, p)
	case registry.FormatELPA:
		return genELPA(be, p)
	case registry.FormatCocoaPods:
		return genCocoaPods(be, p)
	case registry.FormatOpkg:
		return genOpkg(be, p)
	default:
		// conan, p2, chef, puppet, vagrant, sbt, ivy, gradle, git-lfs are
		// store-only formats (no generated metadata); the object is served as-is.
		return nil, "", false
	}
}

// ---- PyPI (simple index) ----

func genPyPI(be registry.ArtifactBackend, p string) ([]byte, string, bool) {
	p = strings.Trim(p, "/")
	switch {
	case p == "simple" || p == "simple/":
		objs, err := be.List("")
		if err != nil {
			return nil, "", false
		}
		pkgs := map[string]struct{}{}
		for _, o := range objs {
			if i := strings.Index(o, "/"); i >= 0 {
				pkgs[o[:i]] = struct{}{}
			}
		}
		var names []string
		for n := range pkgs {
			names = append(names, n)
		}
		sort.Strings(names)
		var b strings.Builder
		b.WriteString("<!DOCTYPE html><html><body>\n")
		for _, n := range names {
			b.WriteString(fmt.Sprintf(`<a href="/simple/%s/">%s</a><br/>`+"\n", n, n))
		}
		b.WriteString("</body></html>\n")
		return []byte(b.String()), "text/html", true
	case strings.HasPrefix(p, "simple/") && strings.Count(p, "/") >= 1:
		name := strings.TrimPrefix(p, "simple/")
		name = strings.Trim(name, "/")
		if name == "" || strings.Contains(name, "/") {
			return nil, "", false
		}
		objs, err := be.List(name + "/")
		if err != nil {
			return nil, "", false
		}
		var b strings.Builder
		b.WriteString("<!DOCTYPE html><html><body>\n")
		for _, o := range objs {
			base := path.Base(o)
			b.WriteString(fmt.Sprintf(`<a href="/%s">%s</a><br/>`+"\n", o, base))
		}
		b.WriteString("</body></html>\n")
		return []byte(b.String()), "text/html", true
	}
	return nil, "", false
}

// ---- Go modules (GOPROXY) ----

func genGo(be registry.ArtifactBackend, p string) ([]byte, string, bool) {
	switch {
	case strings.HasSuffix(p, "/@v/list"):
		module := strings.TrimSuffix(p, "/@v/list")
		objs, err := be.List(module + "/@v/")
		if err != nil {
			return nil, "", false
		}
		var vs []string
		for _, o := range objs {
			base := path.Base(o)
			if strings.HasSuffix(base, ".info") {
				vs = append(vs, strings.TrimSuffix(base, ".info"))
			}
		}
		sort.Strings(vs)
		return []byte(strings.Join(vs, "\n") + "\n"), "text/plain", true
	case strings.HasSuffix(p, "/@latest"):
		module := strings.TrimSuffix(p, "/@latest")
		objs, err := be.List(module + "/@v/")
		if err != nil {
			return nil, "", false
		}
		best := ""
		for _, o := range objs {
			base := path.Base(o)
			if strings.HasSuffix(base, ".info") {
				v := strings.TrimSuffix(base, ".info")
				if v > best {
					best = v
				}
			}
		}
		if best == "" {
			return nil, "", false
		}
		rc, _, _, err := be.Get(module + "/@v/" + best + ".info")
		if err != nil {
			return nil, "", false
		}
		defer rc.Close()
		body, err := io.ReadAll(rc)
		if err != nil {
			return nil, "", false
		}
		return body, "application/json", true
	}
	return nil, "", false
}

// ---- NuGet (v3 flat container) ----

func genNuGet(be registry.ArtifactBackend, p string) ([]byte, string, bool) {
	const prefix = "v3-flatcontainer/"
	if !strings.HasSuffix(p, "/index.json") || !strings.HasPrefix(p, prefix) {
		return nil, "", false
	}
	id := strings.TrimPrefix(p, prefix)
	id = strings.TrimSuffix(id, "/index.json")
	dir := prefix + id + "/"
	objs, err := be.List(dir)
	if err != nil {
		return nil, "", false
	}
	seen := map[string]struct{}{}
	var vs []string
	for _, o := range objs {
		rest := strings.TrimPrefix(o, dir)
		seg := strings.SplitN(rest, "/", 2)[0]
		if seg == "" {
			continue
		}
		if _, ok := seen[seg]; ok {
			continue
		}
		seen[seg] = struct{}{}
		vs = append(vs, seg)
	}
	sort.Strings(vs)
	doc := map[string]interface{}{"versions": vs}
	body, err := json.Marshal(doc)
	if err != nil {
		return nil, "", false
	}
	return body, "application/json", true
}

// ---- Composer (p2) ----

func genComposer(be registry.ArtifactBackend, p string) ([]byte, string, bool) {
	if !strings.HasPrefix(p, "p/") || !strings.HasSuffix(p, ".json") {
		return nil, "", false
	}
	rest := strings.TrimSuffix(strings.TrimPrefix(p, "p/"), ".json") // <vendor>/<pkg>
	if rest == "" || strings.Count(rest, "/") != 1 {
		return nil, "", false
	}
	pkg := rest
	dir := "p/" + pkg + "/"
	objs, err := be.List(dir)
	if err != nil {
		return nil, "", false
	}
	versions := map[string]interface{}{}
	for _, o := range objs {
		base := path.Base(o)
		if !strings.HasSuffix(base, ".zip") {
			continue
		}
		v := strings.TrimSuffix(base, ".zip")
		versions[v] = map[string]interface{}{
			"name":    pkg,
			"version": v,
			"dist": map[string]interface{}{
				"type": "zip",
				"url":  "/" + o,
			},
		}
	}
	doc := map[string]interface{}{"packages": map[string]interface{}{pkg: versions}}
	body, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, "", false
	}
	return body, "application/json", true
}

// ---- Conda (repodata.json) ----

func genConda(be registry.ArtifactBackend, p string) ([]byte, string, bool) {
	if !strings.HasSuffix(p, "repodata.json") {
		return nil, "", false
	}
	subdir := path.Dir(strings.Trim(p, "/"))
	objs, err := be.List(subdir + "/")
	if err != nil {
		return nil, "", false
	}
	packages := map[string]interface{}{}
	for _, o := range objs {
		base := path.Base(o)
		if !strings.HasSuffix(base, ".tar.bz2") && !strings.HasSuffix(base, ".conda") {
			continue
		}
		name, version, build := condaParts(base)
		if name == "" {
			continue
		}
		packages[base] = map[string]interface{}{
			"name":    name,
			"version": version,
			"build":   build,
			"subdir":  subdir,
			"fn":      base,
		}
	}
	doc := map[string]interface{}{
		"info":             map[string]interface{}{"subdir": subdir},
		"packages":         packages,
		"repodata_version": 1,
	}
	body, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, "", false
	}
	return body, "application/json", true
}

// condaParts parses "<name>-<version>-<build>.tar.bz2" (build may contain dashes).
func condaParts(base string) (name, version, build string) {
	base = strings.TrimSuffix(base, ".tar.bz2")
	base = strings.TrimSuffix(base, ".conda")
	i := strings.LastIndex(base, "-")
	if i < 0 {
		return "", "", ""
	}
	build = base[i+1:]
	rest := base[:i]
	i = strings.LastIndex(rest, "-")
	if i < 0 {
		return "", "", ""
	}
	version = rest[i+1:]
	name = rest[:i]
	return name, version, build
}

// ---- APT (Debian: Packages + Release) ----

func genAPT(be registry.ArtifactBackend, p string) ([]byte, string, bool) {
	switch {
	case strings.HasSuffix(p, "Packages") || strings.HasSuffix(p, "Packages.gz"):
		objs, err := be.List("")
		if err != nil {
			return nil, "", false
		}
		var stanzas []string
		for _, o := range objs {
			if !strings.HasSuffix(o, ".deb") {
				continue
			}
			ctrl, cerr := debControl(be, o)
			if cerr != nil || ctrl["Package"] == "" {
				continue
			}
			ctrl["Filename"] = "/" + o
			stanzas = append(stanzas, ctrlStanza(ctrl))
		}
		sort.Strings(stanzas)
		body := strings.Join(stanzas, "")
		if strings.HasSuffix(p, ".gz") {
			var buf bytes.Buffer
			gw := gzip.NewWriter(&buf)
			gw.Write([]byte(body))
			gw.Close()
			return buf.Bytes(), "application/gzip", true
		}
		return []byte(body), "text/plain", true
	case strings.HasSuffix(p, "Release") || strings.HasSuffix(p, "InRelease"):
		// Build a minimal unsigned Release from the Packages content.
		pkgs, _, ok := genAPT(be, strings.TrimSuffix(p, path.Base(p))+"Packages")
		if !ok {
			return nil, "", false
		}
		h := sha256.Sum256(pkgs)
		rel := fmt.Sprintf("SHA256:\n %x %d Packages\n", h, len(pkgs))
		return []byte(rel), "text/plain", true
	}
	return nil, "", false
}

func ctrlStanza(c map[string]string) string {
	order := []string{"Package", "Version", "Architecture", "Section", "Priority",
		"Maintainer", "Description", "Filename", "Size"}
	var b strings.Builder
	for _, k := range order {
		if v, ok := c[k]; ok && v != "" {
			b.WriteString(k + ": " + v + "\n")
		}
	}
	// include any extra keys
	for k, v := range c {
		found := false
		for _, o := range order {
			if o == k {
				found = true
				break
			}
		}
		if !found && v != "" {
			b.WriteString(k + ": " + v + "\n")
		}
	}
	return b.String() + "\n"
}

// debControl reads the control file of a .deb and returns its fields.
func debControl(be registry.ArtifactBackend, p string) (map[string]string, error) {
	rc, _, _, err := be.Get(p)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		return nil, err
	}
	ctrl, err := readDebControl(data)
	if err != nil {
		return nil, err
	}
	return ctrl, nil
}

// readDebControl parses a .deb (ar archive) and returns the control tar fields.
func readDebControl(data []byte) (map[string]string, error) {
	if len(data) < 8 || string(data[:8]) != "!<arch>\n" {
		return nil, fmt.Errorf("not a deb")
	}
	off := 8
	var control []byte
	for off+60 <= len(data) {
		hdr := data[off : off+60]
		name := strings.TrimRight(string(hdr[0:16]), "\x00/")
		sizeField := strings.TrimSpace(string(hdr[48:58]))
		var size int
		_, _ = fmt.Sscanf(sizeField, "%d", &size)
		off += 60
		if off+size > len(data) {
			break
		}
		content := data[off : off+size]
		off += size
		if size%2 == 1 {
			off++
		}
		if name == "control.tar.gz" || name == "control.tar.xz" || name == "control.tar.zst" {
			if name == "control.tar.gz" {
				gz, err := gzip.NewReader(bytes.NewReader(content))
				if err != nil {
					return nil, err
				}
				control, err = io.ReadAll(gz)
				if err != nil {
					return nil, err
				}
			}
			// xz/zst not handled; treat as empty.
			break
		}
	}
	if control == nil {
		return nil, fmt.Errorf("no control tar")
	}
	return parseControlTar(tar.NewReader(bytes.NewReader(control))), nil
}

func parseControlTar(tr *tar.Reader) map[string]string {
	out := map[string]string{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return out
		}
		if path.Base(hdr.Name) == "control" {
			body, err := io.ReadAll(tr)
			if err != nil {
				return out
			}
			for _, line := range strings.Split(string(body), "\n") {
				if i := strings.Index(line, ":"); i > 0 {
					k := strings.TrimSpace(line[:i])
					v := strings.TrimSpace(line[i+1:])
					if k != "" {
						out[k] = v
					}
				}
			}
			return out
		}
	}
	return out
}

// ---- YUM (RPM: repodata/repomd.xml) ----

func genYUM(be registry.ArtifactBackend, p string) ([]byte, string, bool) {
	if !strings.HasSuffix(p, "repodata/repomd.xml") {
		return nil, "", false
	}
	objs, err := be.List("")
	if err != nil {
		return nil, "", false
	}
	var pkgs []rpmPkg
	for _, o := range objs {
		if !strings.HasSuffix(o, ".rpm") {
			continue
		}
		if r, ok := rpmFromFilename(path.Base(o)); ok {
			r.location = "/" + o
			pkgs = append(pkgs, r)
		}
	}
	sort.Slice(pkgs, func(i, j int) bool { return pkgs[i].name < pkgs[j].name })
	var b strings.Builder
	b.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n")
	b.WriteString("<repomd xmlns=\"http://linux.duke.edu/metadata/repo\">\n")
	b.WriteString("  <data type=\"primary\">\n")
	b.WriteString("    <package>\n")
	for _, r := range pkgs {
		b.WriteString("      <name>" + xmlEscape(r.name) + "</name>\n")
		b.WriteString("      <version>" + xmlEscape(r.version) + "-" + xmlEscape(r.release) + "</version>\n")
		b.WriteString("      <arch>" + xmlEscape(r.arch) + "</arch>\n")
		b.WriteString("      <location href=\"" + xmlEscape(r.location) + "\"/>\n")
	}
	b.WriteString("    </package>\n")
	b.WriteString("  </data>\n")
	b.WriteString("</repomd>\n")
	return []byte(b.String()), "application/xml", true
}

type rpmPkg struct {
	name     string
	version  string
	release  string
	arch     string
	location string
}

// rpmFromFilename parses "<name>-<version>-<release>.<arch>.rpm".
func rpmFromFilename(base string) (rpmPkg, bool) {
	base = strings.TrimSuffix(base, ".rpm")
	i := strings.LastIndex(base, ".")
	if i < 0 {
		return rpmPkg{}, false
	}
	arch := base[i+1:]
	rest := base[:i]
	i = strings.LastIndex(rest, "-")
	if i < 0 {
		return rpmPkg{}, false
	}
	release := rest[i+1:]
	rest = rest[:i]
	i = strings.LastIndex(rest, "-")
	if i < 0 {
		return rpmPkg{}, false
	}
	version := rest[i+1:]
	name := rest[:i]
	if name == "" || version == "" {
		return rpmPkg{}, false
	}
	return rpmPkg{name: name, version: version, release: release, arch: arch}, true
}

// ---- CRAN (R packages: PACKAGES index) ----

func genCRAN(be registry.ArtifactBackend, p string) ([]byte, string, bool) {
	if p != "PACKAGES" && p != "PACKAGES.gz" {
		return nil, "", false
	}
	objs, err := be.List("")
	if err != nil {
		return nil, "", false
	}
	type pkg struct{ name, version, path string }
	var pkgs []pkg
	for _, o := range objs {
		base := path.Base(o)
		if !strings.HasSuffix(base, ".tar.gz") {
			continue
		}
		n := strings.TrimSuffix(base, ".tar.gz")
		i := strings.Index(n, "_") // CRAN uses <name>_<version>.tar.gz
		if i < 0 {
			continue
		}
		pkgs = append(pkgs, pkg{name: n[:i], version: n[i+1:], path: "/" + o})
	}
	sort.Slice(pkgs, func(i, j int) bool { return pkgs[i].name < pkgs[j].name })
	var b strings.Builder
	for _, pk := range pkgs {
		b.WriteString("Package: " + pk.name + "\n")
		b.WriteString("Version: " + pk.version + "\n")
		b.WriteString("Path: " + pk.path + "\n\n")
	}
	if p == "PACKAGES.gz" {
		var buf bytes.Buffer
		gw := gzip.NewWriter(&buf)
		gw.Write([]byte(b.String()))
		gw.Close()
		return buf.Bytes(), "application/gzip", true
	}
	return []byte(b.String()), "text/plain", true
}

// ---- ELPA (Emacs: archive-contents) ----

func genELPA(be registry.ArtifactBackend, p string) ([]byte, string, bool) {
	if p != "archive-contents" {
		return nil, "", false
	}
	objs, err := be.List("")
	if err != nil {
		return nil, "", false
	}
	type epkg struct{ name, version string }
	var pkgs []epkg
	for _, o := range objs {
		base := path.Base(o)
		var n string
		switch {
		case strings.HasSuffix(base, ".tar"):
			n = strings.TrimSuffix(base, ".tar")
		case strings.HasSuffix(base, ".el"):
			n = strings.TrimSuffix(base, ".el")
		default:
			continue
		}
		if i := strings.LastIndex(n, "-"); i > 0 {
			pkgs = append(pkgs, epkg{name: n[:i], version: n[i+1:]})
		}
	}
	sort.Slice(pkgs, func(i, j int) bool { return pkgs[i].name < pkgs[j].name })
	var b strings.Builder
	b.WriteString("(1\n")
	for _, pk := range pkgs {
		b.WriteString(fmt.Sprintf(" (%q %q)\n", pk.name, pk.version))
	}
	b.WriteString(")\n")
	return []byte(b.String()), "text/plain", true
}

// ---- CocoaPods (podspec JSON index) ----

func genCocoaPods(be registry.ArtifactBackend, p string) ([]byte, string, bool) {
	if p != "pods.json" && p != "all_pods.txt" {
		return nil, "", false
	}
	objs, err := be.List("")
	if err != nil {
		return nil, "", false
	}
	versions := map[string][]string{}
	for _, o := range objs {
		if !strings.HasSuffix(o, ".podspec.json") {
			continue
		}
		// path layout: <name>/<version>/<name>.podspec.json
		dir := path.Dir(o)
		version := path.Base(dir)
		name := path.Base(path.Dir(dir))
		if name == "" || version == "" {
			continue
		}
		versions[name] = append(versions[name], version)
	}
	for k := range versions {
		sort.Strings(versions[k])
	}
	if p == "all_pods.txt" {
		var names []string
		for n := range versions {
			names = append(names, n)
		}
		sort.Strings(names)
		return []byte(strings.Join(names, "\n") + "\n"), "text/plain", true
	}
	doc := map[string]interface{}{"pods": versions}
	body, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, "", false
	}
	return body, "application/json", true
}

// ---- Opkg (embedded Linux: Packages index from .ipk) ----

func genOpkg(be registry.ArtifactBackend, p string) ([]byte, string, bool) {
	if p != "Packages" && p != "Packages.gz" {
		return nil, "", false
	}
	objs, err := be.List("")
	if err != nil {
		return nil, "", false
	}
	var stanzas []string
	for _, o := range objs {
		if !strings.HasSuffix(o, ".ipk") {
			continue
		}
		ctrl, cerr := extractControl(be, o)
		if cerr != nil || ctrl["Package"] == "" {
			continue
		}
		ctrl["Filename"] = "/" + o
		stanzas = append(stanzas, ctrlStanza(ctrl))
	}
	sort.Strings(stanzas)
	body := strings.Join(stanzas, "")
	if p == "Packages.gz" {
		var buf bytes.Buffer
		gw := gzip.NewWriter(&buf)
		gw.Write([]byte(body))
		gw.Close()
		return buf.Bytes(), "application/gzip", true
	}
	return []byte(body), "text/plain", true
}

// extractControlBytes returns the control-file fields of a .deb (ar+control.tar.gz)
// or .ipk (concatenated gzip tarballs, or plain tar).
func extractControlBytes(data []byte) (map[string]string, error) {
	if len(data) >= 8 && string(data[:8]) == "!<arch>\n" {
		return readDebControl(data)
	}
	if gz, err := gzip.NewReader(bytes.NewReader(data)); err == nil {
		if m := parseControlTar(tar.NewReader(gz)); m != nil {
			return m, nil
		}
	}
	if m := parseControlTar(tar.NewReader(bytes.NewReader(data))); m != nil {
		return m, nil
	}
	return nil, fmt.Errorf("no control file")
}

func extractControl(be registry.ArtifactBackend, p string) (map[string]string, error) {
	rc, _, _, err := be.Get(p)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		return nil, err
	}
	return extractControlBytes(data)
}
