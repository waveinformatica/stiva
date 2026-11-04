package registry

import (
	"path"
	"regexp"
	"strings"
)

// ParsePackagePath maps a stored artifact path to the package and version it
// belongs to, so retention can keep "the newest N versions" of each package
// whatever the format. ok is false for files that are not part of a version:
// generated or cached indexes and signatures of indexes. They are never
// deleted by a cleanup run.
//
// Checksum and signature companions (.sha1, .asc …) resolve to the version of
// the file they accompany, so a version is always removed whole.
func ParsePackagePath(format Format, p string) (pkg, version string, ok bool) {
	p = strings.Trim(p, "/")
	if p == "" || isIndexPath(p) {
		return "", "", false
	}
	p = stripCompanion(p)
	base := path.Base(p)

	switch format {
	case FormatMaven, FormatGradle, FormatSBT, FormatIvy:
		if pkg, version, ok := parseMaven(p); ok {
			return pkg, version, true
		}
	case FormatNpm:
		if pkg, version, ok := parseNpm(p); ok {
			return pkg, version, true
		}
	case FormatGo:
		if i := strings.Index(p, "/@v/"); i > 0 {
			f := p[i+4:]
			for _, ext := range []string{".info", ".mod", ".zip"} {
				if strings.HasSuffix(f, ext) {
					return p[:i], strings.TrimSuffix(f, ext), true
				}
			}
			return "", "", false // list, @latest …
		}
	case FormatNuGet:
		parts := strings.Split(p, "/")
		if len(parts) >= 3 && versionLike.MatchString(parts[1]) {
			return strings.ToLower(parts[0]), strings.ToLower(parts[1]), true
		}
	case FormatPyPI:
		if pkg, version, ok := parsePyPI(base); ok {
			return pkg, version, true
		}
	case FormatAPT, FormatOpkg:
		if m := debLike.FindStringSubmatch(base); m != nil {
			return m[1], m[2], true
		}
	case FormatYUM:
		if m := rpmLike.FindStringSubmatch(base); m != nil {
			return m[1], m[2] + "-" + m[3], true
		}
	}
	return parseGeneric(p)
}

var (
	versionLike = regexp.MustCompile(`^v?\d+(\.\d+)*([-+._~][0-9A-Za-z.+~_-]*)?$`)
	debLike     = regexp.MustCompile(`^([a-z0-9][a-z0-9+.-]*)_([^_]+)_[^_]+\.(?:deb|udeb|ipk)$`)
	rpmLike     = regexp.MustCompile(`^(.+)-([^-]+)-([^-]+)\.[^.]+\.rpm$`)
	// name-1.2.3.ext, name_1.2.3.ext, name-v1.2.3-rc1.tar.gz …
	genericVer  = regexp.MustCompile(`^(.+?)[-_.@]v?(\d+(?:\.\d+)+(?:[-+_~][0-9A-Za-z.+~_-]*)?)$`)
	pypiWheel   = regexp.MustCompile(`^([A-Za-z0-9_.]+)-([0-9][^-]*)(?:-[^-]+)*\.whl$`)
	pypiSdist   = regexp.MustCompile(`^(.+?)-([0-9][^-]*?)(?:\.tar\.gz|\.zip|\.tar\.bz2)$`)
	alphaExt    = regexp.MustCompile(`^\.[A-Za-z][A-Za-z0-9]{0,5}$`)
	compoundExt = regexp.MustCompile(`\.tar\.(?:gz|bz2|xz|zst)$`)
	companion   = []string{".sha1", ".sha256", ".sha512", ".md5", ".asc", ".sig"}
)

func stripCompanion(p string) string {
	for _, ext := range companion {
		if strings.HasSuffix(p, ext) {
			return strings.TrimSuffix(p, ext)
		}
	}
	return p
}

// isIndexPath recognises metadata that is generated or cached, never a version.
func isIndexPath(p string) bool {
	base := path.Base(p)
	for _, pre := range []string{"maven-metadata.xml", "index.yaml", "index.json", "Packages", "Sources",
		"Release", "InRelease", "KEY.gpg", "repomd.xml", "PACKAGES", "archive-contents", "repodata.json",
		"current_repodata.json", "list", "latest"} {
		if base == pre || strings.HasPrefix(base, pre+".") {
			return true
		}
	}
	return strings.HasPrefix(p, "repodata/") || strings.HasPrefix(p, "dists/") ||
		strings.Contains(p, "/repodata/") || strings.HasSuffix(p, "/@latest")
}

func parseMaven(p string) (pkg, version string, ok bool) {
	parts := strings.Split(p, "/")
	if len(parts) < 4 {
		return "", "", false
	}
	file, ver, art := parts[len(parts)-1], parts[len(parts)-2], parts[len(parts)-3]
	if !strings.HasPrefix(file, art+"-") {
		return "", "", false
	}
	return strings.Join(parts[:len(parts)-2], "/"), ver, true
}

func parseNpm(p string) (pkg, version string, ok bool) {
	i := strings.Index(p, "/-/")
	if i <= 0 {
		return "", "", false
	}
	pkg = p[:i]
	file := strings.TrimSuffix(p[i+3:], ".tgz")
	name := path.Base(pkg)
	if !strings.HasPrefix(file, name+"-") {
		return "", "", false
	}
	return pkg, strings.TrimPrefix(file, name+"-"), true
}

func parsePyPI(base string) (pkg, version string, ok bool) {
	norm := func(s string) string {
		return strings.ToLower(strings.NewReplacer("_", "-", ".", "-").Replace(s))
	}
	if m := pypiWheel.FindStringSubmatch(base); m != nil {
		return norm(m[1]), m[2], true
	}
	if m := pypiSdist.FindStringSubmatch(base); m != nil {
		return norm(m[1]), m[2], true
	}
	return "", "", false
}

// parseGeneric handles every other layout: a version token in the file name
// groups files into versions of one package; a file without one is its own
// unit within its directory, so "keep the newest N files per directory"
// still means something for raw stores.
func parseGeneric(p string) (pkg, version string, ok bool) {
	dir, base := path.Split(p)
	stem := base
	if m := compoundExt.FindString(base); m != "" {
		stem = strings.TrimSuffix(base, m)
	} else if e := path.Ext(base); alphaExt.MatchString(e) {
		stem = strings.TrimSuffix(base, e)
	}
	if m := genericVer.FindStringSubmatch(stem); m != nil {
		return dir + m[1], m[2], true
	}
	return strings.TrimSuffix(dir, "/"), base, true
}
