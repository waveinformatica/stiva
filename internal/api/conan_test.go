package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"registry/internal/storage"
)

// conanBackend is an in-memory ArtifactBackend that can list with times.
type conanBackend struct {
	objs map[string]int64 // path -> created_at (UnixNano)
}

func (b *conanBackend) Name() string { return "conan" }
func (b *conanBackend) Get(string) (io.ReadCloser, int64, string, error) {
	return nil, 0, "", storage.ErrNotFound
}
func (b *conanBackend) Head(string) (int64, string, error)  { return 0, "", storage.ErrNotFound }
func (b *conanBackend) Put(string, string, io.Reader) error { return nil }
func (b *conanBackend) Delete(p string) error               { delete(b.objs, p); return nil }
func (b *conanBackend) List(string) ([]string, error)       { return nil, nil }
func (b *conanBackend) Close() error                        { return nil }
func (b *conanBackend) ListObjectInfos(prefix string) ([]storage.ObjectInfo, error) {
	var out []storage.ObjectInfo
	for p, t := range b.objs {
		if strings.HasPrefix(p, prefix) {
			out = append(out, storage.ObjectInfo{Path: p, CreatedAt: t})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

const sec = int64(1e9)

func newConan() *conanBackend {
	base := "v2/conans/zlib/1.2.13/_/_/revisions/"
	return &conanBackend{objs: map[string]int64{
		base + "aaa/files/conanfile.py":                                     100 * sec,
		base + "aaa/files/conanmanifest.txt":                                101 * sec,
		base + "bbb/files/conanfile.py":                                     200 * sec, // newer recipe revision
		base + "bbb/files/conanmanifest.txt":                                201 * sec,
		base + "bbb/packages/p1/revisions/r1/files/conan_package.tgz":       210 * sec,
		base + "bbb/packages/p1/revisions/r2/files/conan_package.tgz":       220 * sec,
		base + "bbb/packages/p2/revisions/r1/files/conan_package.tgz":       230 * sec,
		"v2/conans/fmt/10.0.0/acme/stable/revisions/ccc/files/conanfile.py": 300 * sec,
		// Another user's single-char directory: "_" in a LIKE pattern would match it.
		"v2/conans/zlib/1.2.13/x/y/revisions/zzz/files/conanfile.py": 400 * sec,
	}}
}

func conanReq(t *testing.T, be *conanBackend, method, path, query string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, "/"+path+"?"+query, nil)
	h := &Handler{}
	if !h.conanDispatch(c, be, path) {
		t.Fatalf("%s %s was not handled", method, path)
	}
	c.Writer.WriteHeaderNow()
	return w
}

func TestConanPingAdvertisesRevisions(t *testing.T) {
	w := conanReq(t, newConan(), "GET", "v2/ping", "")
	if w.Code != http.StatusOK || w.Header().Get("X-Conan-Server-Capabilities") != "revisions" {
		t.Errorf("ping = %d %v, want 200 with the revisions capability", w.Code, w.Header())
	}
}

func TestConanLatestAndRevisionsOrderedByUploadTime(t *testing.T) {
	be := newConan()
	w := conanReq(t, be, "GET", "v2/conans/zlib/1.2.13/_/_/latest", "")
	var latest conanRev
	_ = json.Unmarshal(w.Body.Bytes(), &latest)
	if w.Code != 200 || latest.Revision != "bbb" {
		t.Errorf("latest = %d %s, want revision bbb", w.Code, w.Body)
	}
	w = conanReq(t, be, "GET", "v2/conans/zlib/1.2.13/_/_/revisions", "")
	var list struct{ Revisions []conanRev }
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if len(list.Revisions) != 2 || list.Revisions[0].Revision != "bbb" || list.Revisions[1].Revision != "aaa" {
		t.Errorf("revisions = %+v, want [bbb aaa]", list.Revisions)
	}
	// The unrelated "x/y" tree sharing the "_" lookalike must not leak in.
	if w := conanReq(t, be, "GET", "v2/conans/nope/1.0/_/_/latest", ""); w.Code != 404 {
		t.Errorf("unknown recipe = %d, want 404", w.Code)
	}
}

func TestConanPackageRevisions(t *testing.T) {
	be := newConan()
	w := conanReq(t, be, "GET", "v2/conans/zlib/1.2.13/_/_/revisions/bbb/packages/p1/latest", "")
	var latest conanRev
	_ = json.Unmarshal(w.Body.Bytes(), &latest)
	if latest.Revision != "r2" {
		t.Errorf("latest package revision = %q, want r2", latest.Revision)
	}
	w = conanReq(t, be, "GET", "v2/conans/zlib/1.2.13/_/_/revisions/bbb/search", "")
	var pkgs map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &pkgs)
	if len(pkgs) != 2 || pkgs["p1"] == nil || pkgs["p2"] == nil {
		t.Errorf("package ids = %v, want p1 and p2", pkgs)
	}
}

func TestConanFilesListing(t *testing.T) {
	w := conanReq(t, newConan(), "GET", "v2/conans/zlib/1.2.13/_/_/revisions/aaa/files", "")
	var out struct{ Files map[string]any }
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if len(out.Files) != 2 || out.Files["conanfile.py"] == nil || out.Files["conanmanifest.txt"] == nil {
		t.Errorf("files = %v", out.Files)
	}
}

func TestConanSearch(t *testing.T) {
	be := newConan()
	for q, want := range map[string]string{
		"*":        `["fmt/10.0.0@acme/stable","zlib/1.2.13","zlib/1.2.13@x/y"]`,
		"zlib/*":   `["zlib/1.2.13","zlib/1.2.13@x/y"]`,
		"fmt/10.*": `["fmt/10.0.0@acme/stable"]`,
		"ZLIB*":    `["zlib/1.2.13","zlib/1.2.13@x/y"]`,
	} {
		w := conanReq(t, be, "GET", "v2/conans/search", "q="+q)
		var out struct{ Results []string }
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		got, _ := json.Marshal(out.Results)
		if string(got) != want {
			t.Errorf("search %q = %s, want %s", q, got, want)
		}
	}
}

func TestConanDeleteRevisionLeavesTheRest(t *testing.T) {
	be := newConan()
	if w := conanReq(t, be, "DELETE", "v2/conans/zlib/1.2.13/_/_/revisions/aaa", ""); w.Code != 200 {
		t.Fatalf("delete = %d", w.Code)
	}
	for p := range be.objs {
		if strings.Contains(p, "/revisions/aaa/") {
			t.Errorf("%s survived", p)
		}
	}
	if len(be.objs) != 7 {
		t.Errorf("%d objects remain, want 7 (only revision aaa gone)", len(be.objs))
	}
	// Deleting the recipe "_/_" removes its revisions but not the x/y recipe.
	conanReq(t, be, "DELETE", "v2/conans/zlib/1.2.13/_/_", "")
	if _, ok := be.objs["v2/conans/zlib/1.2.13/x/y/revisions/zzz/files/conanfile.py"]; !ok {
		t.Error("deleting recipe _/_ removed the x/y recipe")
	}
	if _, ok := be.objs["v2/conans/zlib/1.2.13/_/_/revisions/bbb/files/conanfile.py"]; ok {
		t.Error("recipe _/_ was not deleted")
	}
}

func TestConanRefParsingRejectsTraversal(t *testing.T) {
	for _, sub := range []string{"conans/../../etc/passwd/x/latest", "conans/a/b/c/../latest", "conans/a/b/c", "other/a/b/c/d"} {
		if _, _, ok := parseConanRef(sub); ok {
			t.Errorf("parseConanRef(%q) accepted", sub)
		}
	}
	ref, rest, ok := parseConanRef("conans/zlib/1.2.13/_/_/revisions/a/files")
	if !ok || ref.display() != "zlib/1.2.13" || rest != "revisions/a/files" {
		t.Errorf("parse = (%+v, %q, %v)", ref, rest, ok)
	}
}

func TestConanFilePathsFallThrough(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("PUT", "/v2/conans/zlib/1.0/_/_/revisions/a/files/conanfile.py", nil)
	if (&Handler{}).conanDispatch(c, newConan(), "v2/conans/zlib/1.0/_/_/revisions/a/files/conanfile.py") {
		t.Error("file upload must fall through to object storage, not be handled here")
	}
}
