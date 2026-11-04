package registry

import "testing"

func TestParsePackagePath(t *testing.T) {
	cases := []struct {
		format   Format
		path     string
		pkg, ver string
		ok       bool
	}{
		{FormatMaven, "org/acme/lib/1.2.0/lib-1.2.0.jar", "org/acme/lib", "1.2.0", true},
		{FormatMaven, "org/acme/lib/1.2.0/lib-1.2.0.jar.sha1", "org/acme/lib", "1.2.0", true},
		{FormatMaven, "org/acme/lib/1.2.0/lib-1.2.0-sources.jar", "org/acme/lib", "1.2.0", true},
		{FormatMaven, "org/acme/lib/maven-metadata.xml", "", "", false},
		{FormatNpm, "left-pad/-/left-pad-1.3.0.tgz", "left-pad", "1.3.0", true},
		{FormatNpm, "@acme/ui/-/ui-2.0.1-beta.1.tgz", "@acme/ui", "2.0.1-beta.1", true},
		{FormatHelm, "charts/nginx-1.4.2.tgz", "charts/nginx", "1.4.2", true},
		{FormatHelm, "index.yaml", "", "", false},
		{FormatPyPI, "packages/Django-4.2.1-py3-none-any.whl", "django", "4.2.1", true},
		{FormatPyPI, "packages/requests-2.31.0.tar.gz", "requests", "2.31.0", true},
		{FormatGo, "github.com/acme/x/@v/v1.2.3.zip", "github.com/acme/x", "v1.2.3", true},
		{FormatGo, "github.com/acme/x/@v/list", "", "", false},
		{FormatNuGet, "Acme.Core/1.0.0/Acme.Core.1.0.0.nupkg", "acme.core", "1.0.0", true},
		{FormatAPT, "pool/main/c/curl/curl_7.88.1-10_amd64.deb", "curl", "7.88.1-10", true},
		{FormatAPT, "dists/stable/Release", "", "", false},
		{FormatYUM, "Packages/bash-5.1.8-6.el9.x86_64.rpm", "bash", "5.1.8-6.el9", true},
		{FormatYUM, "repodata/repomd.xml", "", "", false},
		{FormatConan, "v2/conans/zlib/1.2.13/_/_/revisions/abc/files/conanfile.py", "zlib/1.2.13", "abc", true},
		{FormatConan, "v2/conans/zlib/1.2.13/_/_/revisions/abc/packages/p1/revisions/r1/files/conan_package.tgz", "zlib/1.2.13", "abc", true},
		{FormatConan, "v2/conans/fmt/10.0.0/acme/stable/revisions/def/files/conanfile.py", "fmt/10.0.0@acme/stable", "def", true},
		{FormatP2, "plugins/com.acme.core_1.2.0.v2024.jar", "plugins/com.acme.core", "1.2.0.v2024", true},
		{FormatP2, "features/com.acme.feature_2.0.0.jar", "features/com.acme.feature", "2.0.0", true},
		{FormatP2, "artifacts.jar", "", "", false},
		{FormatComposer, "p/acme/lib/1.2.0.zip", "acme/lib", "1.2.0", true},
		{FormatComposer, "p/acme/lib.json", "", "", false},
		{FormatCocoaPods, "Alamofire/5.8.0/Alamofire.podspec.json", "Alamofire", "5.8.0", true},
		{FormatConda, "linux-64/numpy-1.26.4-py311_0.tar.bz2", "numpy", "1.26.4", true},
		{FormatConda, "noarch/my-pkg-name-2.0-0.conda", "my-pkg-name", "2.0", true},
		{FormatRaw, "builds/app-3.4.5.tar.gz", "builds/app", "3.4.5", true},
		{FormatRaw, "builds/app-3.4.6.tar.gz", "builds/app", "3.4.6", true},
		{FormatRaw, "logs/today.txt", "logs", "today.txt", true},
	}
	for _, c := range cases {
		pkg, ver, ok := ParsePackagePath(c.format, c.path)
		if ok != c.ok || pkg != c.pkg || ver != c.ver {
			t.Errorf("%s %q = (%q, %q, %v), want (%q, %q, %v)", c.format, c.path, pkg, ver, ok, c.pkg, c.ver, c.ok)
		}
	}
}

func TestCompareVersions(t *testing.T) {
	older := [][2]string{
		{"1.9", "1.10"}, {"1.2.3", "1.2.4"}, {"1.0", "1.0.1"}, {"v1.0.0", "v2.0.0"},
		{"1.0.0-rc1", "1.0.0"}, {"1.0.0-rc2", "1.0.0-rc10"}, {"1.0.0-alpha", "1.0.0-beta"}, {"0.9.9", "1.0.0"},
		{"2024.01", "2024.02"},
	}
	for _, p := range older {
		if CompareVersions(p[0], p[1]) != -1 || CompareVersions(p[1], p[0]) != 1 {
			t.Errorf("%s should be older than %s", p[0], p[1])
		}
	}
	for _, p := range [][2]string{{"1.0", "1.0.0"}, {"v1.2", "1.2"}, {"1.0+build5", "1.0+build9"}} {
		if CompareVersions(p[0], p[1]) != 0 {
			t.Errorf("%s and %s should be equal", p[0], p[1])
		}
	}
}

func TestVersionLike(t *testing.T) {
	for s, want := range map[string]bool{"1.2.3": true, "v2": true, "2.0.1-rc1": true, "latest": false, "main": false, "setup.exe": false} {
		if got := IsVersionLike(s); got != want {
			t.Errorf("IsVersionLike(%q) = %v, want %v", s, got, want)
		}
	}
	if !IsPreRelease("1.0-rc1") || IsPreRelease("1.0") {
		t.Error("IsPreRelease wrong")
	}
}
