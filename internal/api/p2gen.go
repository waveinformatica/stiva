package api

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"

	"registry/internal/registry"
	"registry/internal/storage"
)

// Eclipse p2 repository metadata for hosted p2 registries.
//
// A p2 repository is a directory of bundle and feature jars plus two index
// documents: artifacts.xml (what files exist) and content.xml (the
// installable units they provide and require). Build tools produce them with
// the p2 publisher; here they are generated from the jars uploaded under
// plugins/ and features/, the way the other hosted formats generate theirs, so
// a plain PUT of a jar is all publishing takes.
//
// Coverage: OSGi bundles (symbolic name, version, exported and imported
// packages, required bundles, bundle shape) and features (included features
// and plugins, requirements). Fragment-host links, localisation properties,
// category definitions and composite repositories are not generated.

const (
	p2PluginsDir  = "plugins/"
	p2FeaturesDir = "features/"
	maxP2JarBytes = 256 << 20
)

// p2Unit is the generated description of one uploaded jar.
type p2Unit struct {
	classifier string // osgi.bundle | org.eclipse.update.feature
	id         string
	version    string
	size       int64
	units      string // <unit> XML elements (a feature yields two)
	unitCount  int
}

var p2Cache sync.Map // "path|size|created" -> *p2Unit

// ---- manifest and version parsing ----

// parseManifest reads MANIFEST.MF: "Key: value" lines, continuation lines
// starting with a single space.
func parseManifest(b []byte) map[string]string {
	out := map[string]string{}
	var key string
	for _, line := range strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n") {
		switch {
		case line == "":
			key = ""
		case line[0] == ' ' && key != "":
			out[key] += line[1:]
		default:
			if i := strings.Index(line, ":"); i > 0 {
				key = line[:i]
				out[key] = strings.TrimSpace(line[i+1:])
			}
		}
	}
	return out
}

type osgiClause struct {
	name  string
	attrs map[string]string // attributes and directives, keys lowercased, ":=" folded to "="
}

// splitOutside splits s on sep, ignoring separators inside double quotes.
func splitOutside(s string, sep byte) []string {
	var out []string
	inQ, start := false, 0
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '"':
			inQ = !inQ
		case s[i] == sep && !inQ:
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

// parseOSGiHeader parses a clause list such as Export-Package or
// Require-Bundle. "a;b;version=1" yields a clause for each of a and b.
func parseOSGiHeader(v string) []osgiClause {
	var out []osgiClause
	for _, c := range splitOutside(v, ',') {
		var names []string
		attrs := map[string]string{}
		for _, part := range splitOutside(c, ';') {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			if i := strings.Index(part, "="); i > 0 {
				k := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(part[:i]), ":"))
				attrs[k] = strings.Trim(strings.TrimSpace(part[i+1:]), `"`)
			} else {
				names = append(names, part)
			}
		}
		for _, n := range names {
			out = append(out, osgiClause{name: n, attrs: attrs})
		}
	}
	return out
}

// normVersion pads an OSGi version to major.minor.micro[.qualifier].
func normVersion(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return "0.0.0"
	}
	parts := strings.SplitN(v, ".", 4)
	for len(parts) < 3 {
		parts = append(parts, "0")
	}
	return strings.Join(parts, ".")
}

// normRange converts an OSGi version or range to p2 syntax. A bare version
// means "at least" in both.
func normRange(r string) string {
	r = strings.TrimSpace(r)
	if r == "" {
		return "0.0.0"
	}
	if r[0] == '[' || r[0] == '(' {
		inner := r[1 : len(r)-1]
		lo, hi, _ := strings.Cut(inner, ",")
		return string(r[0]) + normVersion(lo) + "," + normVersion(hi) + string(r[len(r)-1])
	}
	return normVersion(r)
}

func xa(s string) string { // XML attribute/text escape; newlines survive as &#xA;
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return strings.NewReplacer("'", "&#39;", "\n", "&#xA;").Replace(b.String())
}

// ---- jar -> p2 unit ----

func readJarEntry(zr *zip.Reader, name string) ([]byte, bool) {
	for _, f := range zr.File {
		if strings.EqualFold(f.Name, name) {
			rc, err := f.Open()
			if err != nil {
				return nil, false
			}
			defer rc.Close()
			b, err := io.ReadAll(io.LimitReader(rc, 4<<20))
			return b, err == nil
		}
	}
	return nil, false
}

// bundleUnit builds the p2 unit of an OSGi bundle from its manifest.
func bundleUnit(mf map[string]string, size int64) (*p2Unit, error) {
	id := strings.TrimSpace(strings.SplitN(mf["Bundle-SymbolicName"], ";", 2)[0])
	if id == "" {
		return nil, fmt.Errorf("no Bundle-SymbolicName")
	}
	ver := normVersion(mf["Bundle-Version"])
	singleton := strings.Contains(strings.ReplaceAll(mf["Bundle-SymbolicName"], " ", ""), "singleton:=true")

	var props []string
	if v := mf["Bundle-Name"]; v != "" {
		props = append(props, fmt.Sprintf("<property name='org.eclipse.equinox.p2.name' value='%s'/>", xa(v)))
	}
	if v := mf["Bundle-Vendor"]; v != "" {
		props = append(props, fmt.Sprintf("<property name='org.eclipse.equinox.p2.provider' value='%s'/>", xa(v)))
	}

	provides := []string{
		fmt.Sprintf("<provided namespace='org.eclipse.equinox.p2.iu' name='%s' version='%s'/>", xa(id), ver),
		fmt.Sprintf("<provided namespace='osgi.bundle' name='%s' version='%s'/>", xa(id), ver),
		fmt.Sprintf("<provided namespace='org.eclipse.equinox.p2.eclipse.type' name='bundle' version='1.0.0'/>"),
	}
	for _, c := range parseOSGiHeader(mf["Export-Package"]) {
		provides = append(provides, fmt.Sprintf("<provided namespace='java.package' name='%s' version='%s'/>",
			xa(c.name), normVersion(c.attrs["version"])))
	}
	if h := parseOSGiHeader(mf["Fragment-Host"]); len(h) > 0 {
		provides = append(provides, fmt.Sprintf("<provided namespace='osgi.fragment' name='%s' version='%s'/>", xa(id), ver))
	}

	var requires []string
	req := func(ns, name, rng string, optional bool) {
		extra := ""
		if optional {
			extra = " optional='true' greedy='false'"
		}
		requires = append(requires, fmt.Sprintf("<required namespace='%s' name='%s' range='%s'%s/>",
			ns, xa(name), xa(normRange(rng)), extra))
	}
	for _, c := range parseOSGiHeader(mf["Require-Bundle"]) {
		req("osgi.bundle", c.name, c.attrs["bundle-version"], c.attrs["resolution"] == "optional")
	}
	for _, c := range parseOSGiHeader(mf["Import-Package"]) {
		req("java.package", c.name, c.attrs["version"], c.attrs["resolution"] == "optional")
	}

	manifest := "Bundle-SymbolicName: " + strings.SplitN(mf["Bundle-SymbolicName"], ";", 2)[0] + "\nBundle-Version: " + mf["Bundle-Version"] + "\n"
	instr := []string{fmt.Sprintf("<instruction key='manifest'>%s</instruction>", xa(manifest))}
	if strings.EqualFold(mf["Eclipse-BundleShape"], "dir") {
		instr = append(instr, "<instruction key='zipped'>true</instruction>")
	}

	var b strings.Builder
	fmt.Fprintf(&b, "    <unit id='%s' version='%s' singleton='%t'>\n", xa(id), ver, singleton)
	fmt.Fprintf(&b, "      <update id='%s' range='[0.0.0,%s)' severity='0'/>\n", xa(id), ver)
	writeList(&b, "properties", props)
	writeList(&b, "provides", provides)
	writeList(&b, "requires", requires)
	fmt.Fprintf(&b, "      <artifacts size='1'>\n        <artifact classifier='osgi.bundle' id='%s' version='%s'/>\n      </artifacts>\n", xa(id), ver)
	b.WriteString("      <touchpoint id='org.eclipse.equinox.p2.osgi' version='1.0.0'/>\n")
	fmt.Fprintf(&b, "      <touchpointData size='1'>\n        <instructions size='%d'>\n", len(instr))
	for _, i := range instr {
		fmt.Fprintf(&b, "          %s\n", i)
	}
	b.WriteString("        </instructions>\n      </touchpointData>\n    </unit>\n")
	return &p2Unit{classifier: "osgi.bundle", id: id, version: ver, size: size, units: b.String(), unitCount: 1}, nil
}

func writeList(b *strings.Builder, tag string, items []string) {
	if len(items) == 0 {
		return
	}
	fmt.Fprintf(b, "      <%s size='%d'>\n", tag, len(items))
	for _, it := range items {
		fmt.Fprintf(b, "        %s\n", it)
	}
	fmt.Fprintf(b, "      </%s>\n", tag)
}

type featureXML struct {
	ID       string `xml:"id,attr"`
	Version  string `xml:"version,attr"`
	Label    string `xml:"label,attr"`
	Provider string `xml:"provider-name,attr"`
	Includes []struct {
		ID      string `xml:"id,attr"`
		Version string `xml:"version,attr"`
	} `xml:"includes"`
	Plugins []struct {
		ID      string `xml:"id,attr"`
		Version string `xml:"version,attr"`
	} `xml:"plugin"`
}

// featureUnit builds the two units p2 expects for a feature: the jar unit that
// carries the artifact, and the group unit that requires its contents.
func featureUnit(f featureXML, size int64) (*p2Unit, error) {
	if f.ID == "" {
		return nil, fmt.Errorf("feature.xml has no id")
	}
	ver := normVersion(f.Version)
	pin := func(v string) string { v = normVersion(v); return "[" + v + "," + v + "]" }

	var b strings.Builder
	fmt.Fprintf(&b, "    <unit id='%s.feature.jar' version='%s'>\n", xa(f.ID), ver)
	writeList(&b, "provides", []string{
		fmt.Sprintf("<provided namespace='org.eclipse.equinox.p2.iu' name='%s.feature.jar' version='%s'/>", xa(f.ID), ver),
		fmt.Sprintf("<provided namespace='org.eclipse.update.feature' name='%s' version='%s'/>", xa(f.ID), ver),
		fmt.Sprintf("<provided namespace='org.eclipse.equinox.p2.eclipse.type' name='feature' version='1.0.0'/>"),
	})
	b.WriteString("      <filter>(org.eclipse.update.install.features=true)</filter>\n")
	fmt.Fprintf(&b, "      <artifacts size='1'>\n        <artifact classifier='org.eclipse.update.feature' id='%s' version='%s'/>\n      </artifacts>\n", xa(f.ID), ver)
	b.WriteString("      <touchpoint id='org.eclipse.equinox.p2.native' version='1.0.0'/>\n")
	b.WriteString("      <touchpointData size='1'>\n        <instructions size='1'>\n          <instruction key='zipped'>true</instruction>\n        </instructions>\n      </touchpointData>\n    </unit>\n")

	var props []string
	if f.Label != "" {
		props = append(props, fmt.Sprintf("<property name='org.eclipse.equinox.p2.name' value='%s'/>", xa(f.Label)))
	}
	if f.Provider != "" {
		props = append(props, fmt.Sprintf("<property name='org.eclipse.equinox.p2.provider' value='%s'/>", xa(f.Provider)))
	}
	props = append(props, "<property name='org.eclipse.equinox.p2.type.group' value='true'/>")

	var requires []string
	for _, p := range f.Plugins {
		requires = append(requires, fmt.Sprintf("<required namespace='osgi.bundle' name='%s' range='%s'/>", xa(p.ID), pin(p.Version)))
	}
	for _, in := range f.Includes {
		requires = append(requires, fmt.Sprintf("<required namespace='org.eclipse.equinox.p2.iu' name='%s.feature.group' range='%s'/>", xa(in.ID), pin(in.Version)))
	}
	requires = append(requires, fmt.Sprintf(
		"<required namespace='org.eclipse.equinox.p2.iu' name='%s.feature.jar' range='%s'>\n          <filter>(org.eclipse.update.install.features=true)</filter>\n        </required>", xa(f.ID), pin(f.Version)))

	fmt.Fprintf(&b, "    <unit id='%s.feature.group' version='%s' singleton='false'>\n", xa(f.ID), ver)
	writeList(&b, "properties", props)
	writeList(&b, "provides", []string{
		fmt.Sprintf("<provided namespace='org.eclipse.equinox.p2.iu' name='%s.feature.group' version='%s'/>", xa(f.ID), ver),
	})
	writeList(&b, "requires", requires)
	b.WriteString("      <touchpoint id='null' version='0.0.0'/>\n    </unit>\n")
	return &p2Unit{classifier: "org.eclipse.update.feature", id: f.ID, version: ver, size: size, units: b.String(), unitCount: 2}, nil
}

// unitForJar parses one uploaded jar. Unreadable or unrecognisable jars yield
// an error and are left out of the repository metadata.
func unitForJar(data []byte, dir string) (*p2Unit, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	if dir == p2FeaturesDir {
		fx, ok := readJarEntry(zr, "feature.xml")
		if !ok {
			return nil, fmt.Errorf("no feature.xml")
		}
		var f featureXML
		if err := xml.Unmarshal(fx, &f); err != nil {
			return nil, err
		}
		return featureUnit(f, int64(len(data)))
	}
	mfb, ok := readJarEntry(zr, "META-INF/MANIFEST.MF")
	if !ok {
		return nil, fmt.Errorf("no MANIFEST.MF")
	}
	return bundleUnit(parseManifest(mfb), int64(len(data)))
}

// ---- repository documents ----

type p2Repo struct {
	units     []*p2Unit
	timestamp int64 // ms since epoch of the newest jar
}

// loadP2Repo reads every jar under plugins/ and features/. Parsed units are
// cached by path, size and write time, so only new or changed jars are read.
func loadP2Repo(be registry.ArtifactBackend) (*p2Repo, error) {
	var infos []storage.ObjectInfo
	if l, ok := be.(objectLister); ok {
		var err error
		if infos, err = l.ListObjectInfos(""); err != nil {
			return nil, err
		}
	} else {
		paths, err := be.List("")
		if err != nil {
			return nil, err
		}
		for _, p := range paths {
			infos = append(infos, storage.ObjectInfo{Path: p})
		}
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].Path < infos[j].Path })
	repo := &p2Repo{}
	for _, o := range infos {
		dir := ""
		switch {
		case strings.HasPrefix(o.Path, p2PluginsDir):
			dir = p2PluginsDir
		case strings.HasPrefix(o.Path, p2FeaturesDir):
			dir = p2FeaturesDir
		}
		if dir == "" || !strings.HasSuffix(o.Path, ".jar") || strings.Contains(strings.TrimPrefix(o.Path, dir), "/") {
			continue
		}
		if t := o.CreatedAt / 1e6; t > repo.timestamp {
			repo.timestamp = t
		}
		key := o.Path + "|" + strconv.FormatInt(o.Size, 10) + "|" + strconv.FormatInt(o.CreatedAt, 10)
		if v, ok := p2Cache.Load(key); ok {
			if u := v.(*p2Unit); u != nil {
				repo.units = append(repo.units, u)
			}
			continue
		}
		u := readP2Jar(be, o.Path, dir)
		p2Cache.Store(key, u)
		if u != nil {
			repo.units = append(repo.units, u)
		}
	}
	return repo, nil
}

func readP2Jar(be registry.ArtifactBackend, p, dir string) *p2Unit {
	rc, sz, _, err := be.Get(p)
	if err != nil {
		return nil
	}
	defer rc.Close()
	if sz > maxP2JarBytes {
		return nil
	}
	data, err := io.ReadAll(io.LimitReader(rc, maxP2JarBytes+1))
	if err != nil || int64(len(data)) > maxP2JarBytes {
		return nil
	}
	u, err := unitForJar(data, dir)
	if err != nil {
		return nil
	}
	return u
}

func (r *p2Repo) artifactsXML(name string) []byte {
	var b strings.Builder
	b.WriteString("<?xml version='1.0' encoding='UTF-8'?>\n<?artifactRepository version='1.1.0'?>\n")
	fmt.Fprintf(&b, "<repository name='%s' type='org.eclipse.equinox.p2.artifact.repository.simpleRepository' version='1'>\n", xa(name))
	fmt.Fprintf(&b, "  <properties size='2'>\n    <property name='p2.timestamp' value='%d'/>\n    <property name='p2.compressed' value='false'/>\n  </properties>\n", r.timestamp)
	b.WriteString("  <mappings size='3'>\n")
	b.WriteString("    <rule filter='(&amp; (classifier=osgi.bundle))' output='${repoUrl}/plugins/${id}_${version}.jar'/>\n")
	b.WriteString("    <rule filter='(&amp; (classifier=binary))' output='${repoUrl}/binary/${id}_${version}'/>\n")
	b.WriteString("    <rule filter='(&amp; (classifier=org.eclipse.update.feature))' output='${repoUrl}/features/${id}_${version}.jar'/>\n")
	b.WriteString("  </mappings>\n")
	fmt.Fprintf(&b, "  <artifacts size='%d'>\n", len(r.units))
	for _, u := range r.units {
		fmt.Fprintf(&b, "    <artifact classifier='%s' id='%s' version='%s'>\n", u.classifier, xa(u.id), u.version)
		fmt.Fprintf(&b, "      <properties size='2'>\n        <property name='artifact.size' value='%d'/>\n        <property name='download.size' value='%d'/>\n      </properties>\n    </artifact>\n", u.size, u.size)
	}
	b.WriteString("  </artifacts>\n</repository>\n")
	return []byte(b.String())
}

func (r *p2Repo) contentXML(name string) []byte {
	n := 0
	for _, u := range r.units {
		n += u.unitCount
	}
	var b strings.Builder
	b.WriteString("<?xml version='1.0' encoding='UTF-8'?>\n<?metadataRepository version='1.1.0'?>\n")
	fmt.Fprintf(&b, "<repository name='%s' type='org.eclipse.equinox.internal.p2.metadata.repository.LocalMetadataRepository' version='1'>\n", xa(name))
	fmt.Fprintf(&b, "  <properties size='2'>\n    <property name='p2.timestamp' value='%d'/>\n    <property name='p2.compressed' value='false'/>\n  </properties>\n", r.timestamp)
	fmt.Fprintf(&b, "  <units size='%d'>\n", n)
	for _, u := range r.units {
		b.WriteString(u.units)
	}
	b.WriteString("  </units>\n</repository>\n")
	return []byte(b.String())
}

func zipped(name string, body []byte) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create(name)
	_, _ = w.Write(body)
	_ = zw.Close()
	return buf.Bytes()
}

const p2Index = "version=1\nmetadata.repository.factory.order=content.xml,\\!\nartifact.repository.factory.order=artifacts.xml,\\!\n"

// genP2 serves the generated index documents of a hosted p2 registry. Both the
// .xml and the zipped .jar form are offered: p2 clients prefer the jar.
func genP2(be registry.ArtifactBackend, p string) ([]byte, string, bool) {
	switch p {
	case "p2.index":
		return []byte(p2Index), "text/plain", true
	case "artifacts.xml", "artifacts.jar", "content.xml", "content.jar":
	default:
		return nil, "", false
	}
	repo, err := loadP2Repo(be)
	if err != nil {
		return nil, "", false
	}
	name := "Stiva p2 repository"
	switch p {
	case "artifacts.xml":
		return repo.artifactsXML(name), "application/xml", true
	case "artifacts.jar":
		return zipped("artifacts.xml", repo.artifactsXML(name)), "application/java-archive", true
	case "content.xml":
		return repo.contentXML(name), "application/xml", true
	default:
		return zipped("content.xml", repo.contentXML(name)), "application/java-archive", true
	}
}
