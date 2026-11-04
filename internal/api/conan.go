package api

import (
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"registry/internal/registry"
	"registry/internal/storage"
)

// Conan 2 server API for hosted registries.
//
// Conan 2 clients talk a REST protocol (/v2/...) built on revisions: a recipe
// revision (rrev) holds the recipe files and any number of binary packages,
// each with its own package revisions (prev). Files travel by plain GET and
// PUT on their own URL, so they are stored at that path as ordinary objects;
// what a server must add is everything that is not a file transfer: ping,
// authentication, "latest" and revision listings (ordered by upload time),
// search, and deletion of a revision's whole tree. Those are derived here from
// the stored objects, so there is no separate index to keep in step.
//
// Not covered: the legacy Conan 1.x (v1) API, and package search by settings
// (the package listing returns ids without their conaninfo).

const conanTimeFormat = "2006-01-02T15:04:05.000+00:00"

// conanRef addresses a recipe: name/version@user/channel, with "_" standing in
// for a missing user and channel as the Conan URL scheme does.
type conanRef struct{ name, version, user, channel string }

func (r conanRef) display() string {
	s := r.name + "/" + r.version
	if r.user != "_" || r.channel != "_" {
		s += "@" + r.user + "/" + r.channel
	}
	return s
}

// base is the storage prefix of everything belonging to the recipe.
func (r conanRef) base() string {
	return "v2/conans/" + r.name + "/" + r.version + "/" + r.user + "/" + r.channel + "/"
}

var conanSeg = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.+-]*$`)

// parseConanRef splits "conans/n/v/u/c/<rest>" and validates each segment so a
// crafted URL cannot escape the recipe tree.
func parseConanRef(sub string) (ref conanRef, rest string, ok bool) {
	parts := strings.SplitN(strings.TrimPrefix(sub, "conans/"), "/", 5)
	if !strings.HasPrefix(sub, "conans/") || len(parts) < 4 {
		return ref, "", false
	}
	for _, p := range parts[:4] {
		if !conanSeg.MatchString(p) {
			return ref, "", false
		}
	}
	ref = conanRef{parts[0], parts[1], parts[2], parts[3]}
	if len(parts) == 5 {
		rest = parts[4]
	}
	return ref, rest, true
}

type conanRev struct {
	Revision string `json:"revision"`
	Time     string `json:"time"`
	t        int64
}

// revisionsUnder lists the revision ids directly below prefix+"revisions/",
// newest first. A revision's time is its earliest object: the upload moment.
func revisionsUnder(objs []storage.ObjectInfo, prefix string) []conanRev {
	pre := prefix + "revisions/"
	first := map[string]int64{}
	for _, o := range objs {
		if !strings.HasPrefix(o.Path, pre) {
			continue
		}
		id := strings.SplitN(strings.TrimPrefix(o.Path, pre), "/", 2)[0]
		if id == "" {
			continue
		}
		if t, ok := first[id]; !ok || o.CreatedAt < t {
			first[id] = o.CreatedAt
		}
	}
	out := make([]conanRev, 0, len(first))
	for id, t := range first {
		out = append(out, conanRev{Revision: id, t: t, Time: time.Unix(0, t).UTC().Format(conanTimeFormat)})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].t != out[j].t {
			return out[i].t > out[j].t
		}
		return out[i].Revision < out[j].Revision
	})
	return out
}

// subdirs lists the names one level below prefix (used for package ids).
func subdirs(objs []storage.ObjectInfo, prefix string) []string {
	seen := map[string]bool{}
	for _, o := range objs {
		if !strings.HasPrefix(o.Path, prefix) {
			continue
		}
		rest := strings.TrimPrefix(o.Path, prefix)
		if i := strings.Index(rest, "/"); i > 0 {
			seen[rest[:i]] = true
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

var conanRecipePath = regexp.MustCompile(`^v2/conans/([^/]+)/([^/]+)/([^/]+)/([^/]+)/revisions/`)

// searchRefs returns the recipes whose reference matches the glob q ("*"
// matches anything, including "/"), sorted.
func searchRefs(objs []storage.ObjectInfo, q string) []string {
	if q == "" {
		q = "*"
	}
	re, err := regexp.Compile("^(?i:" + strings.ReplaceAll(regexp.QuoteMeta(q), `\*`, ".*") + ")$")
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	for _, o := range objs {
		m := conanRecipePath.FindStringSubmatch(o.Path)
		if m == nil {
			continue
		}
		ref := conanRef{m[1], m[2], m[3], m[4]}.display()
		if !seen[ref] && re.MatchString(ref) {
			seen[ref] = true
		}
	}
	out := make([]string, 0, len(seen))
	for r := range seen {
		out = append(out, r)
	}
	sort.Strings(out)
	return out
}

type objectLister interface {
	ListObjectInfos(prefix string) ([]storage.ObjectInfo, error)
}

// conanDispatch serves the non-file part of the Conan 2 API. It reports true
// when it answered; file GET/PUT/HEAD fall through to ordinary object storage.
func (h *Handler) conanDispatch(c *gin.Context, be registry.ArtifactBackend, p string) bool {
	if !strings.HasPrefix(p, "v2/") {
		return false
	}
	sub := strings.TrimPrefix(p, "v2/")
	method := c.Request.Method

	switch sub {
	case "ping":
		c.Header("X-Conan-Server-Capabilities", "revisions")
		c.Status(http.StatusOK)
		return true
	case "users/authenticate":
		u := h.currentUser(c)
		if u == nil || u.IsAnonymous() {
			c.Header("WWW-Authenticate", h.authMgr.Challenge(c))
			c.JSON(http.StatusUnauthorized, gin.H{"errors": []gin.H{{"code": "UNAUTHORIZED", "message": "credentials required"}}})
			return true
		}
		tok, err := h.authMgr.IssueToken(u)
		if err != nil {
			c.JSON(http.StatusInternalServerError, errorBody("INTERNAL_ERROR", err.Error()))
			return true
		}
		c.Data(http.StatusOK, "text/plain", []byte(tok))
		return true
	case "users/check_credentials":
		if u := h.currentUser(c); u == nil || u.IsAnonymous() {
			c.Status(http.StatusUnauthorized)
			return true
		}
		c.Status(http.StatusOK)
		return true
	}

	lister, ok := be.(objectLister)
	if !ok {
		return false
	}

	if sub == "conans/search" {
		objs, err := lister.ListObjectInfos("v2/conans/")
		if err != nil {
			c.JSON(http.StatusInternalServerError, errorBody("INTERNAL_ERROR", err.Error()))
			return true
		}
		c.JSON(http.StatusOK, gin.H{"results": searchRefs(objs, c.Query("q"))})
		return true
	}

	ref, rest, ok := parseConanRef(sub)
	if !ok {
		return false
	}
	// A recipe revision tree: revisions/<rrev>[/packages/<pid>/revisions/<prev>]/...
	segs := strings.Split(strings.Trim(rest, "/"), "/")
	if rest == "" {
		segs = nil
	}
	base := ref.base()

	del := func(prefix string) bool {
		objs, err := lister.ListObjectInfos(prefix)
		if err != nil {
			c.JSON(http.StatusInternalServerError, errorBody("INTERNAL_ERROR", err.Error()))
			return true
		}
		if len(objs) == 0 {
			c.JSON(http.StatusNotFound, errorBody("NOT_FOUND", "nothing to delete"))
			return true
		}
		for _, o := range objs {
			if err := be.Delete(o.Path); err != nil {
				c.JSON(http.StatusInternalServerError, errorBody("INTERNAL_ERROR", err.Error()))
				return true
			}
		}
		c.Status(http.StatusOK)
		return true
	}
	// latest / revisions listing for the tree rooted at prefix.
	revisionsAt := func(prefix string, latestOnly bool) bool {
		objs, err := lister.ListObjectInfos(prefix + "revisions/")
		if err != nil {
			c.JSON(http.StatusInternalServerError, errorBody("INTERNAL_ERROR", err.Error()))
			return true
		}
		revs := revisionsUnder(objs, prefix)
		if len(revs) == 0 {
			c.JSON(http.StatusNotFound, errorBody("NOT_FOUND", "no revisions"))
			return true
		}
		if latestOnly {
			c.JSON(http.StatusOK, revs[0])
		} else {
			c.JSON(http.StatusOK, gin.H{"revisions": revs})
		}
		return true
	}
	// filesAt lists the files stored under a ".../files/" prefix.
	filesAt := func(prefix string) bool {
		objs, err := lister.ListObjectInfos(prefix)
		if err != nil {
			c.JSON(http.StatusInternalServerError, errorBody("INTERNAL_ERROR", err.Error()))
			return true
		}
		files := gin.H{}
		for _, o := range objs {
			name := strings.TrimPrefix(o.Path, prefix)
			if name != "" {
				files[name] = gin.H{}
			}
		}
		if len(files) == 0 {
			c.JSON(http.StatusNotFound, errorBody("NOT_FOUND", "revision not found"))
			return true
		}
		c.JSON(http.StatusOK, gin.H{"files": files})
		return true
	}

	switch {
	case len(segs) == 0 && method == http.MethodDelete:
		return del(base) // every revision of the recipe
	case len(segs) == 1 && segs[0] == "latest" && method == http.MethodGet:
		return revisionsAt(base, true)
	case len(segs) == 1 && segs[0] == "revisions" && method == http.MethodGet:
		return revisionsAt(base, false)
	case len(segs) == 2 && segs[0] == "revisions" && method == http.MethodDelete:
		return del(base + "revisions/" + segs[1] + "/")
	}

	// Below a recipe revision.
	if len(segs) >= 3 && segs[0] == "revisions" && conanSeg.MatchString(segs[1]) {
		rrev := base + "revisions/" + segs[1] + "/"
		tail := segs[2:]
		switch {
		case len(tail) == 1 && tail[0] == "files" && method == http.MethodGet:
			return filesAt(rrev + "files/")
		case len(tail) == 1 && tail[0] == "search" && method == http.MethodGet:
			objs, err := lister.ListObjectInfos(rrev + "packages/")
			if err != nil {
				c.JSON(http.StatusInternalServerError, errorBody("INTERNAL_ERROR", err.Error()))
				return true
			}
			out := gin.H{}
			for _, id := range subdirs(objs, rrev+"packages/") {
				out[id] = gin.H{}
			}
			c.JSON(http.StatusOK, out)
			return true
		case len(tail) == 1 && tail[0] == "packages" && method == http.MethodDelete:
			return del(rrev + "packages/")
		case len(tail) >= 2 && tail[0] == "packages" && conanSeg.MatchString(tail[1]):
			pkg := rrev + "packages/" + tail[1] + "/"
			ptail := tail[2:]
			switch {
			case len(ptail) == 0 && method == http.MethodDelete:
				return del(pkg)
			case len(ptail) == 1 && ptail[0] == "latest" && method == http.MethodGet:
				return revisionsAt(pkg, true)
			case len(ptail) == 1 && ptail[0] == "revisions" && method == http.MethodGet:
				return revisionsAt(pkg, false)
			case len(ptail) == 2 && ptail[0] == "revisions" && method == http.MethodDelete:
				return del(pkg + "revisions/" + ptail[1] + "/")
			case len(ptail) == 3 && ptail[0] == "revisions" && ptail[2] == "files" && method == http.MethodGet:
				return filesAt(pkg + "revisions/" + ptail[1] + "/files/")
			}
		}
	}
	return false
}
