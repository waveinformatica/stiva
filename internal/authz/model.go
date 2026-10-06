// Package authz is the authorization model: an enumerated permission
// vocabulary, roles that hold permissions, and bindings that grant a role to a
// principal within a scope.
//
// It replaces two mechanisms that used to coexist — per-registry access lists
// and a roles table nothing ever read — with one. Having a declared vocabulary
// is also what makes a permission picker possible in the UI: before, a role's
// permissions were free text with no defined set to choose from.
package authz

import (
	"strings"
)

// Permission is one action a principal may be granted.
type Permission string

const (
	// Registry-scoped.
	RegistryRead   Permission = "registry:read"
	RegistryWrite  Permission = "registry:write"
	RegistryDelete Permission = "registry:delete"

	// Administrative. These are only meaningful at scope "*".
	AdminRegistries Permission = "admin:registries"
	AdminStores     Permission = "admin:stores"
	AdminUsers      Permission = "admin:users"
	AdminSettings   Permission = "admin:settings"
)

// All is the vocabulary, in the order the UI should present it.
var All = []Permission{
	RegistryRead, RegistryWrite, RegistryDelete,
	AdminRegistries, AdminStores, AdminUsers, AdminSettings,
}

// Describe returns a short explanation for the permission picker. Naming a
// permission is not enough: "registry:delete" reads the same to someone who
// thinks it removes a registry and to someone who knows it removes images.
var Describe = map[Permission]string{
	RegistryRead:    "Pull images and browse repositories",
	RegistryWrite:   "Push images and upload artifacts",
	RegistryDelete:  "Delete tags, manifests and repositories",
	AdminRegistries: "Create, edit and delete registries",
	AdminStores:     "Manage blob stores and their credentials",
	AdminUsers:      "Manage users, groups, roles and grants",
	AdminSettings:   "Change global settings",
}

// IsAdmin reports whether a permission is administrative, i.e. only meaningful
// globally. Granting one inside a single registry's scope would look like it
// restricted the power, and it would not.
func (p Permission) IsAdmin() bool { return strings.HasPrefix(string(p), "admin:") }

// Valid reports whether p is part of the vocabulary. Anything else is refused
// at write time rather than silently stored and never matched.
func (p Permission) Valid() bool {
	for _, x := range All {
		if x == p {
			return true
		}
	}
	return false
}

// SystemAdmin is the built-in role that holds every permission. Its permission
// set is not stored: it is defined as "all", so a permission added in a later
// release is covered without a data migration. It cannot be deleted, and at
// least one active principal must always hold it.
const SystemAdmin = "system:admin"

// Scope limits where a binding applies. It has three positions — format,
// registry, repository pattern — and "*" is a wildcard in any of them:
//
//   - everywhere
//     helm                       every Helm registry
//     docker:*:kosmos/**         repositories under kosmos/ in any OCI registry
//     docker:internal            one registry, whole
//     maven:libs:com/wave/**     one namespace of one Maven registry
//
// The format comes first because it is what gives the path pattern its meaning:
// "com/waveinformatica/**" is a Maven coordinate, "kosmos/**" is an image
// namespace, and a grant that did not say which would be guessing. It also lets
// a grant cover a whole format without naming every registry in it.
//
// Trailing positions may be omitted and default to "*", so "docker" and
// "docker:*:*" are the same scope.
//
// Repository paths have arbitrary depth ("fca-nbes/fca-nbes/upm"), so the last
// position is a pattern rather than a fixed number of levels.
type Scope string

const ScopeAll Scope = "*"

// Parts splits a scope into its three positions, filling omitted ones with "*".
func (s Scope) Parts() (format, registry, pattern string) {
	format, registry, pattern = "*", "*", "*"
	if s == "" {
		return
	}
	bits := strings.SplitN(string(s), ":", 3)
	format = bits[0]
	if len(bits) > 1 {
		registry = bits[1]
	}
	if len(bits) > 2 {
		pattern = bits[2]
	}
	return
}

// Matches reports whether the scope covers a repository of a given format and
// registry. An empty repo asks "does this scope touch the registry at all",
// which is what a check with no repository in hand needs.
func (s Scope) Matches(format, registry, repo string) bool {
	sf, sr, sp := s.Parts()
	if sf != "*" && sf != format {
		return false
	}
	if sr != "*" && sr != registry {
		return false
	}
	if sp == "*" || sp == "**" {
		return true
	}
	if repo == "" {
		// The scope narrows to part of a registry, so it cannot answer a
		// question about the whole of it.
		return false
	}
	return matchPath(sp, repo)
}

// Valid reports whether the scope is well-formed.
func (s Scope) Valid() bool {
	if s == "" {
		return false
	}
	f, r, p := s.Parts()
	if f == "" || r == "" || p == "" {
		return false
	}
	// A wildcard format with a specific registry is contradictory: registry
	// names are unique, so the format is either implied or wrong.
	if f == "*" && r != "*" {
		return false
	}
	return true
}

// matchPath matches a slash-separated path against a pattern where "*" covers
// one segment and "**" covers any number, including none.
func matchPath(pattern, path string) bool {
	return matchSegments(strings.Split(pattern, "/"), strings.Split(path, "/"))
}

func matchSegments(pat, seg []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			// A trailing "**" covers what lies below, not the parent itself:
			// "kosmos/**" grants the repositories under kosmos/, and not a
			// repository named exactly "kosmos". Granting the parent as well
			// would be the surprising reading of a pattern that ends in a
			// separator.
			if len(pat) == 1 {
				return len(seg) > 0
			}
			for i := 0; i <= len(seg); i++ {
				if matchSegments(pat[1:], seg[i:]) {
					return true
				}
			}
			return false
		}
		if len(seg) == 0 {
			return false
		}
		if !matchSegment(pat[0], seg[0]) {
			return false
		}
		pat, seg = pat[1:], seg[1:]
	}
	return len(seg) == 0
}

// matchSegment matches one path segment, where "*" is a wildcard within it.
func matchSegment(pat, s string) bool {
	if pat == "*" {
		return true
	}
	if !strings.Contains(pat, "*") {
		return pat == s
	}
	parts := strings.Split(pat, "*")
	if !strings.HasPrefix(s, parts[0]) {
		return false
	}
	s = s[len(parts[0]):]
	for i := 1; i < len(parts)-1; i++ {
		j := strings.Index(s, parts[i])
		if j < 0 {
			return false
		}
		s = s[j+len(parts[i]):]
	}
	last := parts[len(parts)-1]
	return strings.HasSuffix(s, last) && len(s) >= len(last)
}
