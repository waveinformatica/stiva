package api

import (
	"encoding/json"
	"strings"
	"testing"

	"registry/internal/registry"
)

func has(sn []snippet, sub string) bool {
	for _, s := range sn {
		if strings.Contains(s.Code, sub) {
			return true
		}
	}
	return false
}

func TestUseSnippetsPerFormat(t *testing.T) {
	ep := "https://reg.example.com/repo"
	cases := []struct {
		format   registry.Format
		pkg, ver string
		want     []string
	}{
		{registry.FormatMaven, "org/acme/lib", "1.2.0", []string{"<groupId>org.acme</groupId>", "<artifactId>lib</artifactId>", `implementation("org.acme:lib:1.2.0")`}},
		{registry.FormatNpm, "@acme/ui", "2.0.1", []string{"npm install @acme/ui@2.0.1"}},
		{registry.FormatPyPI, "requests", "2.31.0", []string{"pip install requests==2.31.0"}},
		{registry.FormatGo, "github.com/acme/x", "v1.2.3", []string{"go get github.com/acme/x@v1.2.3"}},
		{registry.FormatNuGet, "acme.core", "1.0.0", []string{"dotnet add package acme.core --version 1.0.0"}},
		{registry.FormatHelm, "charts/nginx", "1.4.2", []string{"helm install my-nginx stiva/nginx --version 1.4.2"}},
		{registry.FormatAPT, "curl", "7.88.1-10", []string{"sudo apt install curl=7.88.1-10"}},
		{registry.FormatYUM, "bash", "5.1.8-6.el9", []string{"sudo dnf install bash-5.1.8-6.el9"}},
		{registry.FormatConan, "zlib/1.2.13", "abc123", []string{"--requires=zlib/1.2.13 -r stiva"}},
		{registry.FormatComposer, "acme/lib", "1.2.0", []string{"composer require acme/lib:1.2.0"}},
		{registry.FormatCocoaPods, "Alamofire", "5.8.0", []string{ep + "/Alamofire/5.8.0/Alamofire.podspec.json"}},
		{registry.FormatP2, "plugins/com.acme.core", "1.0.0", []string{"-installIU com.acme.core/1.0.0"}},
	}
	for _, c := range cases {
		sn := useSnippets(c.format, ep, c.pkg, c.ver, "")
		for _, w := range c.want {
			if !has(sn, w) {
				t.Errorf("%s: no snippet contains %q; got %+v", c.format, w, sn)
			}
		}
	}
	// Formats without their own client syntax fall back to a direct download.
	sn := useSnippets(registry.FormatRaw, ep, "builds/app", "1.0", "builds/app-1.0.tar.gz")
	if !has(sn, "curl -fLO "+ep+"/builds/app-1.0.tar.gz") {
		t.Errorf("raw: want a curl download, got %+v", sn)
	}
	if got := useSnippets(registry.FormatRaw, ep, "x", "1", ""); got != nil {
		t.Errorf("raw without a file should give nothing, got %+v", got)
	}
}

func TestSetupSnippetsPerFormat(t *testing.T) {
	ep := "https://reg.example.com/maven"
	for format, want := range map[registry.Format]string{
		registry.FormatMaven:    "<url>" + ep + "</url>",
		registry.FormatNpm:      "registry=" + ep + "/",
		registry.FormatPyPI:     "index-url = " + ep + "/simple/",
		registry.FormatGo:       "GOPROXY=" + ep + ",direct",
		registry.FormatNuGet:    ep + "/v3/index.json",
		registry.FormatHelm:     "helm repo add stiva " + ep,
		registry.FormatAPT:      ep + " ./",
		registry.FormatYUM:      "baseurl=" + ep + "/",
		registry.FormatConan:    "conan remote add stiva " + ep,
		registry.FormatP2:       ep,
		registry.FormatComposer: ep,
	} {
		if !has(setupSnippets(format, ep), want) {
			t.Errorf("%s: setup lacks %q", format, want)
		}
	}
	// The npm auth line is keyed on host and path without the scheme.
	if !has(setupSnippets(registry.FormatNpm, ep), "//reg.example.com/maven/:_authToken=") {
		t.Errorf("npm auth line wrong: %+v", setupSnippets(registry.FormatNpm, ep))
	}
}

func TestOCISnippets(t *testing.T) {
	sn := ociSnippets("registry.example.com", "acme/app", "1.2.3")
	for _, w := range []string{"docker pull registry.example.com/acme/app:1.2.3", "podman pull", "image: registry.example.com/acme/app:1.2.3"} {
		if !has(sn, w) {
			t.Errorf("missing %q", w)
		}
	}
	if ociSnippets("", "acme/app", "") != nil {
		t.Error("no pull host: no snippets")
	}
	if !has(ociSnippets("h", "r", ""), "h/r:latest") {
		t.Error("an empty tag should default to latest")
	}
}

func TestServiceDocuments(t *testing.T) {
	var idx struct {
		Version   string
		Resources []struct {
			ID   string `json:"@id"`
			Type string `json:"@type"`
		}
	}
	if err := json.Unmarshal(nugetServiceIndex("https://r.example.com/nuget"), &idx); err != nil {
		t.Fatalf("nuget index is not JSON: %v", err)
	}
	if idx.Version != "3.0.0" || len(idx.Resources) != 1 ||
		idx.Resources[0].ID != "https://r.example.com/nuget/v3-flatcontainer/" || idx.Resources[0].Type != "PackageBaseAddress/3.0.0" {
		t.Errorf("nuget index = %+v", idx)
	}
	var root map[string]string
	if err := json.Unmarshal(composerRoot("php"), &root); err != nil || root["metadata-url"] != "/php/p/%package%.json" {
		t.Errorf("composer root = %v (%v)", root, err)
	}
	if err := json.Unmarshal(composerRoot(""), &root); err != nil || root["metadata-url"] != "/p/%package%.json" {
		t.Errorf("composer root without base path = %v", root)
	}
}

func TestFormatsWithoutOwnSyntaxFallBackToDownloads(t *testing.T) {
	ep := "https://r.example.com"
	if useSnippets(registry.FormatRaw, ep, "pkg", "1", "") != nil {
		t.Error("raw has no client syntax: without a file there is nothing to show")
	}
	for _, f := range []registry.Format{registry.FormatMaven, registry.FormatNpm, registry.FormatPyPI, registry.FormatGo, registry.FormatHelm} {
		if useSnippets(f, ep, "pkg", "1", "") == nil {
			t.Errorf("%s should have its own usage snippets", f)
		}
	}
}

func TestNilSnippetListsReachTheBrowserAsEmptyArrays(t *testing.T) {
	// raw has no client setup; the JSON must say [] or the page reads null.length.
	b, _ := json.Marshal(map[string]any{"setup": nonNil(setupSnippets(registry.FormatRaw, "https://r.example.com"))})
	if string(b) != `{"setup":[]}` {
		t.Errorf("raw setup serialises as %s, want an empty array", b)
	}
}
