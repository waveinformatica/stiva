package api

import (
	"fmt"
	"path"
	"strings"

	"github.com/gin-gonic/gin"

	"registry/internal/registry"
)

// A snippet is a ready-to-paste piece of configuration or a command that
// connects a client to the registry: how to add the repository, and how to
// depend on a package. They are generated from what the registry really serves,
// per format, so the page can say exactly what to type.
type snippet struct {
	Title string `json:"title"`
	Lang  string `json:"lang"` // xml, bash, ini, json, kotlin, text …
	Code  string `json:"code"`
	Note  string `json:"note,omitempty"`
}

// requestScheme is the scheme the caller reached us on, honouring a proxy.
func requestScheme(c *gin.Context) string {
	if p := c.GetHeader("X-Forwarded-Proto"); p == "http" || p == "https" {
		return p
	}
	if c.Request.TLS != nil {
		return "https"
	}
	return "http"
}

// endpointURL is the address clients use for a path-addressed registry: the
// host that really routes to it, then its base path.
func (h *Handler) endpointURL(c *gin.Context, reg *registry.Registry) string {
	host, _, _ := h.pullRoute(c.Request.Host, reg)
	if host == "" {
		host = c.Request.Host
	}
	u := requestScheme(c) + "://" + host
	if bp := strings.Trim(reg.BasePath, "/"); bp != "" {
		u += "/" + bp
	}
	return u
}

// mavenCoords turns a stored package path ("org/acme/lib") into group and artifact.
func mavenCoords(pkg string) (group, artifact string) {
	parts := strings.Split(strings.Trim(pkg, "/"), "/")
	artifact = parts[len(parts)-1]
	if len(parts) > 1 {
		group = strings.Join(parts[:len(parts)-1], ".")
	}
	return
}

func fenced(lines ...string) string { return strings.Join(lines, "\n") }

const authNote = "Public content needs no credentials. For private content use your username and an API key as the password."

// setupSnippets say how to point a client at the registry (once per machine or
// project). They do not depend on a package.
func setupSnippets(format registry.Format, ep string) []snippet {
	switch format {
	case registry.FormatMaven, registry.FormatGradle:
		return []snippet{
			{"Maven (pom.xml)", "xml", fenced(
				"<repositories>",
				"  <repository>",
				"    <id>stiva</id>",
				"    <url>"+ep+"</url>",
				"  </repository>",
				"</repositories>"), authNote + " Credentials go in ~/.m2/settings.xml, in a <server> with the same id."},
			{"Gradle (build.gradle.kts)", "kotlin", fenced(
				"repositories {",
				"    maven {",
				"        url = uri(\""+ep+"\")",
				"        // credentials { username = \"…\"; password = \"…\" }",
				"    }",
				"}"), ""},
		}
	case registry.FormatSBT:
		return []snippet{{"sbt (build.sbt)", "scala", `resolvers += "stiva" at "` + ep + `"`, authNote}}
	case registry.FormatIvy:
		return []snippet{{"Ivy (ivysettings.xml)", "xml", fenced(
			"<ivysettings>",
			"  <resolvers>",
			"    <ibiblio name=\"stiva\" root=\""+ep+"\" m2compatible=\"true\"/>",
			"  </resolvers>",
			"</ivysettings>"), ""}}
	case registry.FormatNpm:
		nerf := strings.TrimRight(strings.TrimPrefix(strings.TrimPrefix(ep, "https://"), "http://"), "/")
		return []snippet{{"npm / yarn / pnpm (.npmrc)", "ini", fenced(
			"registry="+ep+"/",
			"//"+nerf+"/:_authToken=${NPM_TOKEN}"),
			authNote + " For a scoped package use @scope:registry=" + ep + "/ instead."}}
	case registry.FormatPyPI:
		return []snippet{
			{"pip (pip.conf)", "ini", fenced("[global]", "index-url = "+ep+"/simple/"), authNote},
			{"One-off", "bash", "pip install --index-url " + ep + "/simple/ <package>", ""},
			{"Publish (twine)", "bash", "twine upload --repository-url " + ep + "/ dist/*", authNote},
		}
	case registry.FormatGo:
		return []snippet{{"Go", "bash", fenced(
			"go env -w GOPROXY="+ep+",direct",
			"# Private modules are not in the public checksum database:",
			"go env -w GONOSUMDB=<your/module/prefix>"), authNote + " Credentials go in ~/.netrc."}}
	case registry.FormatNuGet:
		return []snippet{{".NET", "bash", "dotnet nuget add source " + ep + "/v3/index.json --name stiva", authNote + " Add --username and --password for private feeds."}}
	case registry.FormatHelm:
		return []snippet{{"Helm", "bash", fenced("helm repo add stiva "+ep, "helm repo update"), authNote + " Add --username and --password for private repositories."}}
	case registry.FormatRubyGems:
		return []snippet{{"Bundler (Gemfile)", "ruby", `source "` + ep + `"`, ""}}
	case registry.FormatComposer:
		return []snippet{{"Composer (composer.json)", "json", fenced(
			"{",
			"  \"repositories\": [",
			"    { \"type\": \"composer\", \"url\": \""+ep+"\" }",
			"  ]",
			"}"), ""}}
	case registry.FormatConda:
		return []snippet{{"conda", "bash", "conda config --add channels " + ep, ""}}
	case registry.FormatAPT:
		return []snippet{{"APT", "bash", fenced(
			"curl -fsSL "+ep+"/KEY.gpg | sudo gpg --dearmor -o /etc/apt/keyrings/stiva.gpg",
			"echo \"deb [signed-by=/etc/apt/keyrings/stiva.gpg] "+ep+" ./\" | sudo tee /etc/apt/sources.list.d/stiva.list",
			"sudo apt update"), "KEY.gpg exists once a signing key is created for the registry; without one, use [trusted=yes] in place of signed-by (only on networks you trust)."}}
	case registry.FormatYUM:
		return []snippet{{"dnf / yum (/etc/yum.repos.d/stiva.repo)", "ini", fenced(
			"[stiva]",
			"name=Stiva",
			"baseurl="+ep+"/",
			"enabled=1",
			"gpgcheck=0"), ""}}
	case registry.FormatConan:
		return []snippet{{"Conan 2", "bash", fenced(
			"conan remote add stiva "+ep,
			"conan remote login stiva <user>"), authNote}}
	case registry.FormatCRAN:
		return []snippet{{"R", "r", `options(repos = c(stiva = "` + ep + `", getOption("repos")))`, ""}}
	case registry.FormatELPA:
		return []snippet{{"Emacs", "lisp", `(add-to-list 'package-archives '("stiva" . "` + ep + `/") t)`, ""}}
	case registry.FormatP2:
		return []snippet{
			{"Eclipse IDE", "text", "Help → Install New Software… → Add… → Location: " + ep, ""},
			{"Tycho (pom.xml)", "xml", fenced(
				"<repository>",
				"  <id>stiva</id>",
				"  <layout>p2</layout>",
				"  <url>"+ep+"</url>",
				"</repository>"), ""},
		}
	case registry.FormatOpkg:
		return []snippet{{"opkg", "bash", fenced(
			"echo \"src/gz stiva "+ep+"\" >> /etc/opkg/customfeeds.conf",
			"opkg update"), ""}}
	case registry.FormatCocoaPods:
		return nil // the podspec URL in the usage snippet is all a Podfile needs
	}
	return nil
}

// downloadSnippets fetch one stored file directly; they apply to every
// path-addressed format.
func downloadSnippets(ep, filePath string) []snippet {
	if filePath == "" {
		return nil
	}
	u := ep + "/" + strings.TrimLeft(filePath, "/")
	return []snippet{{"curl", "bash", fenced(
		"curl -fLO "+u,
		"# private: curl -fLO -u <user>:<api-key> "+u), ""},
		{"wget", "bash", "wget " + u, ""}}
}

// useSnippets say how to depend on one package version. pkg and version are
// what ParsePackagePath reads from the stored path; filePath is the main file
// of the version (used for direct downloads and for formats addressed by file).
func useSnippets(format registry.Format, ep, pkg, version, filePath string) []snippet {
	name := path.Base(pkg)
	switch format {
	case registry.FormatMaven, registry.FormatGradle:
		g, a := mavenCoords(pkg)
		return []snippet{
			{"Maven (pom.xml)", "xml", fenced(
				"<dependency>",
				"  <groupId>"+g+"</groupId>",
				"  <artifactId>"+a+"</artifactId>",
				"  <version>"+version+"</version>",
				"</dependency>"), ""},
			{"Gradle (Kotlin DSL)", "kotlin", fmt.Sprintf(`implementation("%s:%s:%s")`, g, a, version), ""},
		}
	case registry.FormatSBT:
		g, a := mavenCoords(pkg)
		return []snippet{{"sbt", "scala", fmt.Sprintf(`libraryDependencies += "%s" %% "%s" %% "%s"`, g, a, version), ""}}
	case registry.FormatIvy:
		g, a := mavenCoords(pkg)
		return []snippet{{"Ivy", "xml", fmt.Sprintf(`<dependency org="%s" name="%s" rev="%s"/>`, g, a, version), ""}}
	case registry.FormatNpm:
		return []snippet{
			{"npm", "bash", "npm install " + pkg + "@" + version, ""},
			{"package.json", "json", fmt.Sprintf(`"dependencies": { %q: %q }`, pkg, version), ""},
		}
	case registry.FormatPyPI:
		return []snippet{
			{"pip", "bash", "pip install " + pkg + "==" + version, ""},
			{"requirements.txt", "text", pkg + "==" + version, ""},
		}
	case registry.FormatGo:
		return []snippet{{"Go", "bash", "go get " + pkg + "@" + version, ""}}
	case registry.FormatNuGet:
		return []snippet{
			{".NET CLI", "bash", "dotnet add package " + pkg + " --version " + version, ""},
			{"csproj", "xml", fmt.Sprintf(`<PackageReference Include="%s" Version="%s" />`, pkg, version), ""},
		}
	case registry.FormatHelm:
		return []snippet{{"Helm", "bash", fmt.Sprintf("helm install my-%s stiva/%s --version %s", name, name, version), ""}}
	case registry.FormatRubyGems:
		return []snippet{
			{"Gemfile", "ruby", fmt.Sprintf(`gem "%s", "%s"`, pkg, version), ""},
			{"gem", "bash", fmt.Sprintf("gem install %s -v %s --source %s", pkg, version, ep), ""},
		}
	case registry.FormatComposer:
		return []snippet{{"Composer", "bash", "composer require " + pkg + ":" + version, ""}}
	case registry.FormatConda:
		return []snippet{{"conda", "bash", fmt.Sprintf("conda install -c %s %s=%s", ep, pkg, version), ""}}
	case registry.FormatAPT, registry.FormatOpkg:
		if format == registry.FormatOpkg {
			return []snippet{{"opkg", "bash", "opkg install " + pkg, ""}}
		}
		return []snippet{{"APT", "bash", fmt.Sprintf("sudo apt install %s=%s", pkg, version), ""}}
	case registry.FormatYUM:
		return []snippet{{"dnf", "bash", fmt.Sprintf("sudo dnf install %s-%s", pkg, version), ""}}
	case registry.FormatConan:
		return []snippet{{"Conan 2", "bash", fmt.Sprintf("conan install --requires=%s -r stiva", pkg), "Pin the recipe revision with " + pkg + "#" + version + "."}}
	case registry.FormatCRAN:
		return []snippet{{"R", "r", fmt.Sprintf(`install.packages("%s", repos = "%s")`, pkg, ep),
			`For this exact version: remotes::install_version("` + pkg + `", version = "` + version + `", repos = "` + ep + `")`}}
	case registry.FormatELPA:
		return []snippet{{"Emacs", "lisp", fmt.Sprintf("(package-install '%s)", pkg), ""}}
	case registry.FormatP2:
		id := strings.TrimPrefix(strings.TrimPrefix(pkg, "plugins/"), "features/")
		if strings.HasPrefix(pkg, "features/") {
			id += ".feature.group"
		}
		return []snippet{{"p2 director", "bash", fmt.Sprintf(
			"eclipse -nosplash -application org.eclipse.equinox.p2.director -repository %s -installIU %s/%s", ep, id, version), ""}}
	case registry.FormatCocoaPods:
		return []snippet{{"Podfile", "ruby", fmt.Sprintf(`pod '%s', :podspec => '%s/%s/%s/%s.podspec.json'`, name, ep, name, version, name), ""}}
	case registry.FormatVagrant:
		return []snippet{{"Vagrant", "bash", fmt.Sprintf("vagrant box add %s %s/%s", name, ep, strings.TrimLeft(filePath, "/")), ""}}
	}
	return downloadSnippets(ep, filePath)
}

// ociSnippets are the ways to run an image from the registry.
func ociSnippets(pullHost, repo, tag string) []snippet {
	if pullHost == "" {
		return nil
	}
	if tag == "" {
		tag = "latest"
	}
	img := pullHost + "/" + repo + ":" + tag
	name := path.Base(repo)
	return []snippet{
		{"Docker", "bash", fenced("docker pull "+img, "docker run --rm "+img), ""},
		{"Podman", "bash", "podman pull " + img, ""},
		{"Docker Compose", "yaml", fenced("services:", "  "+name+":", "    image: "+img), ""},
		{"Kubernetes", "yaml", fenced(
			"containers:",
			"  - name: "+name,
			"    image: "+img), "A private registry also needs an imagePullSecret."},
		{"Sign in (private)", "bash", "docker login " + pullHost, ""},
	}
}

// serviceDocument builds the root document some clients fetch before anything
// else, which names where the real endpoints are. It needs the address the
// caller used, so it cannot be generated from the stored files alone.
func (h *Handler) serviceDocument(c *gin.Context, reg *registry.Registry, p string) ([]byte, string, bool) {
	switch registry.Format(reg.Format) {
	case registry.FormatNuGet:
		if p == "v3/index.json" {
			return nugetServiceIndex(h.endpointURL(c, reg)), "application/json", true
		}
	case registry.FormatComposer:
		if p == "packages.json" {
			return composerRoot(strings.Trim(reg.BasePath, "/")), "application/json", true
		}
	}
	return nil, "", false
}

// nugetServiceIndex is the NuGet v3 service index. The flat container is the
// resource restore needs: it lists versions and serves the .nupkg files.
func nugetServiceIndex(ep string) []byte {
	return []byte(fmt.Sprintf(`{
  "version": "3.0.0",
  "resources": [
    { "@id": "%s/v3-flatcontainer/", "@type": "PackageBaseAddress/3.0.0", "comment": "Package content and version listing" }
  ]
}
`, ep))
}

// composerRoot is packages.json: it tells Composer 2 where each package's
// metadata lives. The URL is relative to the host root, so the base path of the
// registry is part of it.
func composerRoot(basePath string) []byte {
	prefix := ""
	if basePath != "" {
		prefix = "/" + basePath
	}
	return []byte(fmt.Sprintf("{\n  \"metadata-url\": \"%s/p/%%package%%.json\"\n}\n", prefix))
}
