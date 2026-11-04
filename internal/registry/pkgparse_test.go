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
