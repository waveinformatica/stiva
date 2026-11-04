package api

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"registry/internal/registry"
)

func makeTgz(t *testing.T, name, version string) []byte {
	t.Helper()
	pj, _ := json.Marshal(map[string]string{"name": name, "version": version})
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{Name: "package/package.json", Mode: 0o644, Size: int64(len(pj))})
	_, _ = tw.Write(pj)
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

func publishDoc(name string, versions map[string][]byte, filenames map[string]string) []byte {
	vs := map[string]any{}
	att := map[string]any{}
	for v, data := range versions {
		vs[v] = map[string]string{"name": name, "version": v}
		fn := filenames[v]
		if fn == "" {
			fn = name[strings.LastIndex(name, "/")+1:] + "-" + v + ".tgz"
		}
		att[fn] = map[string]any{"content_type": "application/octet-stream", "data": base64.StdEncoding.EncodeToString(data), "length": len(data)}
	}
	b, _ := json.Marshal(map[string]any{"name": name, "versions": vs, "_attachments": att})
	return b
}

func TestParseNpmPublish(t *testing.T) {
	good := makeTgz(t, "@acme/hello", "1.0.0")
	atts, err := parseNpmPublish(publishDoc("@acme/hello", map[string][]byte{"1.0.0": good}, nil), "@acme/hello")
	if err != nil || len(atts) != 1 || atts[0].filename != "hello-1.0.0.tgz" || !bytes.Equal(atts[0].data, good) {
		t.Fatalf("valid publish = (%+v, %v)", atts, err)
	}

	// The real client names a scoped package's attachment with the scope.
	atts, err = parseNpmPublish(publishDoc("@acme/hello", map[string][]byte{"1.0.0": good}, map[string]string{"1.0.0": "@acme/hello-1.0.0.tgz"}), "@acme/hello")
	if err != nil || len(atts) != 1 || atts[0].filename != "hello-1.0.0.tgz" {
		t.Fatalf("scoped attachment name = (%+v, %v), want it stored as hello-1.0.0.tgz", atts, err)
	}

	cases := map[string]struct {
		doc []byte
		pkg string
	}{
		"wrong package in URL":       {publishDoc("@acme/hello", map[string][]byte{"1.0.0": good}, nil), "@acme/other"},
		"tarball of another name":    {publishDoc("@acme/hello", map[string][]byte{"1.0.0": makeTgz(t, "@evil/pkg", "1.0.0")}, nil), "@acme/hello"},
		"tarball of another version": {publishDoc("@acme/hello", map[string][]byte{"1.0.0": makeTgz(t, "@acme/hello", "9.9.9")}, nil), "@acme/hello"},
		"traversal in filename":      {publishDoc("@acme/hello", map[string][]byte{"1.0.0": good}, map[string]string{"1.0.0": "../x.tgz"}), "@acme/hello"},
		"filename not name-version":  {publishDoc("@acme/hello", map[string][]byte{"1.0.0": good}, map[string]string{"1.0.0": "other-1.0.0.tgz"}), "@acme/hello"},
		"not a tarball":              {publishDoc("@acme/hello", map[string][]byte{"1.0.0": []byte("junk")}, nil), "@acme/hello"},
		"no attachments":             {[]byte(`{"name":"@acme/hello","versions":{}}`), "@acme/hello"},
		"not json":                   {[]byte("x"), "@acme/hello"},
	}
	for name, c := range cases {
		if _, err := parseNpmPublish(c.doc, c.pkg); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestNpmMetadataCarriesDistAndPicksLatest(t *testing.T) {
	v9 := makeTgz(t, "left-pad", "1.9.0")
	v10 := makeTgz(t, "left-pad", "1.10.0")
	rc := makeTgz(t, "left-pad", "2.0.0-rc.1")
	be := &p2Backend{data: map[string][]byte{
		"left-pad/-/left-pad-1.9.0.tgz":      v9,
		"left-pad/-/left-pad-1.10.0.tgz":     v10,
		"left-pad/-/left-pad-2.0.0-rc.1.tgz": rc,
	}}
	body, err := generateNpmMetadata(be, "left-pad", "https://r.example.com/npm")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Name     string
		Versions map[string]struct {
			Dist struct{ Tarball, Shasum, Integrity string }
		}
		DistTags map[string]string `json:"dist-tags"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.DistTags["latest"] != "1.10.0" {
		t.Errorf("latest = %q, want 1.10.0 (numeric order, stable before pre-release)", doc.DistTags["latest"])
	}
	d := doc.Versions["1.9.0"].Dist
	sum := sha1.Sum(v9)
	if d.Tarball != "https://r.example.com/npm/left-pad/-/left-pad-1.9.0.tgz" || d.Shasum != hex.EncodeToString(sum[:]) || !strings.HasPrefix(d.Integrity, "sha512-") {
		t.Errorf("dist = %+v", d)
	}
	if len(doc.Versions) != 3 {
		t.Errorf("%d versions, want 3", len(doc.Versions))
	}
}

func TestPypiTargetKeepsUploadsInTheirOwnProject(t *testing.T) {
	cases := []struct {
		name, file, want string
		ok               bool
	}{
		{"My_Package", "my_package-1.0-py3-none-any.whl", "my-package/my_package-1.0-py3-none-any.whl", true},
		{"requests", "requests-2.31.0.tar.gz", "requests/requests-2.31.0.tar.gz", true},
		{"Django", "Django-4.2.1-py3-none-any.whl", "django/Django-4.2.1-py3-none-any.whl", true},
		{"requests", "evil-1.0.tar.gz", "", false}, // another project's file
		{"", "x-1.0.tar.gz", "", false},            // no project named
		{"x", "../x-1.0.tar.gz", "", false},        // traversal
		{"x", "x-1.0.exe", "", false},              // not a distribution
	}
	for _, c := range cases {
		got, err := pypiTarget(c.name, c.file)
		if (err == nil) != c.ok || got != c.want {
			t.Errorf("pypiTarget(%q, %q) = (%q, %v), want (%q, ok=%v)", c.name, c.file, got, err, c.want, c.ok)
		}
	}
}

func TestLatestGoVersion(t *testing.T) {
	for _, c := range []struct {
		in   []string
		want string
	}{
		{[]string{"v1.0.0", "v1.2.0", "v1.10.0"}, "v1.10.0"},
		{[]string{"v1.9.0", "v2.0.0-rc1"}, "v1.9.0"},
		{[]string{"v2.0.0-rc1", "v2.0.0-rc2"}, "v2.0.0-rc2"},
		{nil, ""},
	} {
		if got := latestGoVersion(c.in); got != c.want {
			t.Errorf("latestGoVersion(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestWebUploadTarget(t *testing.T) {
	tgz := makeTgz(t, "@acme/hello", "1.2.3")
	cases := []struct {
		format    registry.Format
		dir, file string
		data      []byte
		want      string
		ok        bool
	}{
		{registry.FormatRaw, "", "setup.exe", nil, "setup.exe", true},
		{registry.FormatRaw, "builds/linux/", "app-1.0.tar.gz", nil, "builds/linux/app-1.0.tar.gz", true},
		{registry.FormatRaw, "/a//b/", "f.txt", nil, "a/b/f.txt", true},
		{registry.FormatRaw, "../etc", "passwd", nil, "", false},
		{registry.FormatRaw, "a/./b", "f", nil, "", false},
		{registry.FormatRaw, "", "../../x", nil, "x", true}, // only the base name of the upload counts
		{registry.FormatMaven, "org/acme/lib/1.0/", "lib-1.0.jar", nil, "org/acme/lib/1.0/lib-1.0.jar", true},
		{registry.FormatNpm, "ignored", "acme-hello-1.2.3.tgz", tgz, "@acme/hello/-/hello-1.2.3.tgz", true},
		{registry.FormatNpm, "", "x.zip", tgz, "", false},
		{registry.FormatNpm, "", "x.tgz", []byte("junk"), "", false},
		{registry.FormatPyPI, "ignored", "My_Pkg-1.0-py3-none-any.whl", nil, "my-pkg/My_Pkg-1.0-py3-none-any.whl", true},
		{registry.FormatPyPI, "", "readme.txt", nil, "", false},
		{registry.FormatConan, "", "conanfile.py", nil, "", false},
		{registry.FormatDocker, "", "x", nil, "", false},
	}
	for _, c := range cases {
		got, err := webUploadTarget(c.format, c.dir, c.file, c.data)
		if (err == nil) != c.ok || got != c.want {
			t.Errorf("%s dir=%q file=%q = (%q, %v), want (%q, ok=%v)", c.format, c.dir, c.file, got, err, c.want, c.ok)
		}
	}
}
