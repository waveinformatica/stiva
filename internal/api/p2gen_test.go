package api

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"sort"
	"strings"
	"testing"

	"registry/internal/storage"
)

func makeJar(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(body))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

type p2Backend struct {
	data    map[string][]byte
	created map[string]int64
}

func (b *p2Backend) Name() string { return "p2" }
func (b *p2Backend) Get(p string) (io.ReadCloser, int64, string, error) {
	d, ok := b.data[p]
	if !ok {
		return nil, 0, "", storage.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(d)), int64(len(d)), "", nil
}
func (b *p2Backend) Head(string) (int64, string, error)  { return 0, "", storage.ErrNotFound }
func (b *p2Backend) Put(string, string, io.Reader) error { return nil }
func (b *p2Backend) Delete(string) error                 { return nil }
func (b *p2Backend) Close() error                        { return nil }
func (b *p2Backend) List(string) ([]string, error) {
	var out []string
	for p := range b.data {
		out = append(out, p)
	}
	sort.Strings(out)
	return out, nil
}
func (b *p2Backend) ListObjectInfos(string) ([]storage.ObjectInfo, error) {
	var out []storage.ObjectInfo
	for p, d := range b.data {
		out = append(out, storage.ObjectInfo{Path: p, Size: int64(len(d)), CreatedAt: b.created[p]})
	}
	return out, nil
}

const bundleMF = `Manifest-Version: 1.0
Bundle-SymbolicName: com.acme.core;singleton:=true
Bundle-Version: 1.2.0.v2024
Bundle-Name: Acme Core
Bundle-Vendor: Acme
Export-Package: com.acme.api;version="1.2.0",
 com.acme.spi;version="1.0"
Import-Package: org.osgi.framework;version="[1.5,2.0)",
 org.slf4j;resolution:=optional
Require-Bundle: org.eclipse.core.runtime;bundle-version="3.20.0"
Eclipse-BundleShape: dir
`

const featureXMLDoc = `<?xml version="1.0"?>
<feature id="com.acme.feature" version="1.2.0.v2024" label="Acme" provider-name="Acme Inc">
  <includes id="com.acme.base.feature" version="1.0.0"/>
  <plugin id="com.acme.core" version="1.2.0.v2024"/>
</feature>`

func TestP2ContentAndArtifacts(t *testing.T) {
	be := &p2Backend{data: map[string][]byte{
		"plugins/com.acme.core_1.2.0.v2024.jar":     makeJar(t, map[string]string{"META-INF/MANIFEST.MF": bundleMF}),
		"features/com.acme.feature_1.2.0.v2024.jar": makeJar(t, map[string]string{"feature.xml": featureXMLDoc}),
		"plugins/broken.jar":                        []byte("not a zip"),
		"plugins/readme.txt":                        []byte("ignored"),
		"plugins/sub/nested.jar":                    makeJar(t, map[string]string{"META-INF/MANIFEST.MF": bundleMF}),
	}, created: map[string]int64{"plugins/com.acme.core_1.2.0.v2024.jar": 5_000_000_000}}

	content, _, ok := genP2(be, "content.xml")
	if !ok {
		t.Fatal("content.xml not generated")
	}
	var doc struct {
		Units []struct {
			ID        string `xml:"id,attr"`
			Version   string `xml:"version,attr"`
			Singleton string `xml:"singleton,attr"`
			Provides  []struct {
				NS      string `xml:"namespace,attr"`
				Name    string `xml:"name,attr"`
				Version string `xml:"version,attr"`
			} `xml:"provides>provided"`
			Requires []struct {
				NS       string `xml:"namespace,attr"`
				Name     string `xml:"name,attr"`
				Range    string `xml:"range,attr"`
				Optional string `xml:"optional,attr"`
			} `xml:"requires>required"`
		} `xml:"units>unit"`
	}
	if err := xml.Unmarshal(content, &doc); err != nil {
		t.Fatalf("content.xml is not well-formed: %v\n%s", err, content)
	}
	ids := map[string]int{}
	for i, u := range doc.Units {
		ids[u.ID] = i
	}
	for _, want := range []string{"com.acme.core", "com.acme.feature.feature.jar", "com.acme.feature.feature.group"} {
		if _, ok := ids[want]; !ok {
			t.Errorf("unit %q missing; have %v", want, ids)
		}
	}
	if len(doc.Units) != 3 {
		t.Errorf("%d units, want 3 (broken, nested and non-jar files must be skipped)", len(doc.Units))
	}
	core := doc.Units[ids["com.acme.core"]]
	if core.Version != "1.2.0.v2024" || core.Singleton != "true" {
		t.Errorf("core = %s %s singleton=%s", core.ID, core.Version, core.Singleton)
	}
	reqs := map[string]string{}
	opt := map[string]string{}
	for _, r := range core.Requires {
		reqs[r.NS+":"+r.Name] = r.Range
		opt[r.NS+":"+r.Name] = r.Optional
	}
	if reqs["java.package:org.osgi.framework"] != "[1.5.0,2.0.0)" {
		t.Errorf("import range = %q, want [1.5.0,2.0.0)", reqs["java.package:org.osgi.framework"])
	}
	if opt["java.package:org.slf4j"] != "true" {
		t.Error("optional import not flagged optional")
	}
	if reqs["osgi.bundle:org.eclipse.core.runtime"] != "3.20.0" {
		t.Errorf("require-bundle range = %q", reqs["osgi.bundle:org.eclipse.core.runtime"])
	}
	pkgs := 0
	for _, p := range core.Provides {
		if p.NS == "java.package" {
			pkgs++
		}
	}
	if pkgs != 2 {
		t.Errorf("exports %d packages, want 2", pkgs)
	}
	if !strings.Contains(string(content), "key='zipped'") || !strings.Contains(string(content), "key='manifest'") {
		t.Error("touchpoint instructions (manifest, zipped for dir-shaped bundles) missing")
	}

	group := doc.Units[ids["com.acme.feature.feature.group"]]
	gr := map[string]string{}
	for _, r := range group.Requires {
		gr[r.Name] = r.Range
	}
	if gr["com.acme.core"] != "[1.2.0.v2024,1.2.0.v2024]" || gr["com.acme.base.feature.feature.group"] != "[1.0.0,1.0.0]" || gr["com.acme.feature.feature.jar"] == "" {
		t.Errorf("feature group requires = %v", gr)
	}

	art, _, _ := genP2(be, "artifacts.xml")
	var adoc struct {
		Artifacts []struct {
			Classifier string `xml:"classifier,attr"`
			ID         string `xml:"id,attr"`
		} `xml:"artifacts>artifact"`
		Props []struct {
			Name  string `xml:"name,attr"`
			Value string `xml:"value,attr"`
		} `xml:"properties>property"`
	}
	if err := xml.Unmarshal(art, &adoc); err != nil {
		t.Fatalf("artifacts.xml is not well-formed: %v", err)
	}
	if len(adoc.Artifacts) != 2 {
		t.Errorf("%d artifacts, want 2 (one bundle, one feature)", len(adoc.Artifacts))
	}
	for _, p := range adoc.Props {
		if p.Name == "p2.timestamp" && p.Value != "5000" {
			t.Errorf("p2.timestamp = %s, want the newest jar's time in ms (5000)", p.Value)
		}
	}
}

func TestP2JarFormsContainTheXML(t *testing.T) {
	be := &p2Backend{data: map[string][]byte{
		"plugins/a_1.0.0.jar": makeJar(t, map[string]string{"META-INF/MANIFEST.MF": "Bundle-SymbolicName: a\nBundle-Version: 1.0.0\n"}),
	}}
	for p, entry := range map[string]string{"artifacts.jar": "artifacts.xml", "content.jar": "content.xml"} {
		body, _, ok := genP2(be, p)
		if !ok {
			t.Fatalf("%s not generated", p)
		}
		zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
		if err != nil || len(zr.File) != 1 || zr.File[0].Name != entry {
			t.Errorf("%s is not a zip holding %s: %v", p, entry, err)
		}
	}
	if body, _, ok := genP2(be, "p2.index"); !ok || !strings.Contains(string(body), "artifacts.xml") {
		t.Error("p2.index missing")
	}
	if _, _, ok := genP2(be, "plugins/a_1.0.0.jar"); ok {
		t.Error("uploaded jars must be served as stored, not generated")
	}
}

func TestOSGiHeaderParsing(t *testing.T) {
	cl := parseOSGiHeader(`a.b;c.d;version="1.0",e.f;version="[1.0,2.0)";resolution:=optional`)
	if len(cl) != 3 || cl[0].name != "a.b" || cl[1].name != "c.d" || cl[2].name != "e.f" {
		t.Fatalf("clauses = %+v", cl)
	}
	if cl[2].attrs["version"] != "[1.0,2.0)" || cl[2].attrs["resolution"] != "optional" {
		t.Errorf("attrs = %v (a comma inside a quoted range must not split the clause)", cl[2].attrs)
	}
	for in, want := range map[string]string{"1": "1.0.0", "1.2": "1.2.0", "1.2.3.q": "1.2.3.q", "": "0.0.0"} {
		if got := normVersion(in); got != want {
			t.Errorf("normVersion(%q) = %q, want %q", in, got, want)
		}
	}
	if got := normRange("(1.0,2)"); got != "(1.0.0,2.0.0)" {
		t.Errorf("normRange = %q", got)
	}
}
