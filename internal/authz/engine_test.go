package authz

import "testing"

func newTestEngine(rolePerms map[string][]Permission, bindings []Binding) *Engine {
	return NewInMemory(rolePerms, bindings)
}

// TestSubjectsIncludeLocalGroups is the fix for the defect this whole phase
// exists for: a local user's groups must expand into group subjects, or a role
// granted to a group means nothing for them.
func TestSubjectsIncludeLocalGroups(t *testing.T) {
	p := &Principal{Name: "alice", Groups: []string{"devs", "sre"}}
	got := map[string]bool{}
	for _, s := range p.Subjects() {
		got[s] = true
	}
	for _, want := range []string{"authenticated", "user:alice", "group:devs", "group:sre"} {
		if !got[want] {
			t.Errorf("manca il soggetto %q", want)
		}
	}
	if got["anonymous"] {
		t.Error("un utente autenticato non deve essere anche anonimo")
	}

	a := &Principal{Name: "anon-k8s", Anonymous: true}
	subs := map[string]bool{}
	for _, s := range a.Subjects() {
		subs[s] = true
	}
	if !subs["anonymous"] || subs["authenticated"] {
		t.Error("un anonimo deve valere solo come anonymous")
	}
}

func TestAllowsThroughGroupAndScope(t *testing.T) {
	e := newTestEngine(
		map[string][]Permission{
			"reader": {RegistryRead},
			"pusher": {RegistryRead, RegistryWrite},
		},
		[]Binding{
			{Subject: "group:devs", Role: "pusher", Scope: "docker:main:kosmos/**"},
			{Subject: "authenticated", Role: "reader", Scope: "docker:main"},
			{Subject: "user:carol", Role: "reader", Scope: "maven:libs"},
		},
	)

	dev := &Principal{Name: "bob", Groups: []string{"devs"}}
	if !e.Allows(dev, RegistryWrite, "docker", "main", "kosmos/core") {
		t.Error("il gruppo devs deve poter scrivere sotto kosmos/")
	}
	if e.Allows(dev, RegistryWrite, "docker", "main", "apeiron/iot-hub") {
		t.Error("fuori dallo scope la scrittura non deve passare")
	}
	if !e.Allows(dev, RegistryRead, "docker", "main", "apeiron/iot-hub") {
		t.Error("la lettura arriva dal binding su authenticated")
	}
	if e.Allows(dev, RegistryRead, "maven", "libs", "x") {
		t.Error("nessun binding copre maven per questo utente")
	}

	carol := &Principal{Name: "carol"}
	if !e.Allows(carol, RegistryRead, "maven", "libs", "gruppo/artefatto") {
		t.Error("il binding diretto sull'utente deve valere")
	}
	if e.Allows(carol, RegistryWrite, "maven", "libs", "gruppo/artefatto") {
		t.Error("reader non concede scrittura")
	}
}

// TestAdminPermissionsAreGlobal pins that administrative power cannot be handed
// out inside a single registry, where it would look restricted without being so.
func TestAdminPermissionsAreGlobal(t *testing.T) {
	e := newTestEngine(
		map[string][]Permission{"halfadmin": {AdminUsers}},
		[]Binding{
			{Subject: "user:mallory", Role: "halfadmin", Scope: "docker:main"},
			{Subject: "user:root", Role: SystemAdmin, Scope: ScopeAll},
		},
	)
	if e.Allows(&Principal{Name: "mallory"}, AdminUsers, "", "", "") {
		t.Error("un permesso admin con scope ristretto non deve valere")
	}
	if e.IsAdmin(&Principal{Name: "mallory"}) {
		t.Error("mallory non è amministratore")
	}
	if !e.IsAdmin(&Principal{Name: "root"}) {
		t.Error("system:admin su scope * deve essere amministratore")
	}
}

// TestAnonymousNeverAdministers covers the constraint that is easy to get wrong
// once and never notice.
func TestAnonymousNeverAdministers(t *testing.T) {
	e := newTestEngine(nil, []Binding{
		{Subject: "anonymous", Role: SystemAdmin, Scope: ScopeAll},
	})
	anon := &Principal{Name: "anonymous", Anonymous: true}
	if e.Allows(anon, AdminUsers, "", "", "") || e.IsAdmin(anon) {
		t.Fatal("un principal anonimo non deve mai amministrare, nemmeno con un binding esplicito")
	}
	// Ma la lettura concessa a un anonimo deve funzionare.
	e2 := newTestEngine(map[string][]Permission{"r": {RegistryRead}},
		[]Binding{{Subject: "anonymous", Role: "r", Scope: "docker:main"}})
	if !e2.Allows(anon, RegistryRead, "docker", "main", "kosmos/core") {
		t.Error("il pull anonimo esplicitamente concesso deve passare")
	}
}

func TestNilPrincipalIsDenied(t *testing.T) {
	e := newTestEngine(nil, []Binding{{Subject: "authenticated", Role: SystemAdmin, Scope: ScopeAll}})
	if e.Allows(nil, RegistryRead, "docker", "main", "x") {
		t.Fatal("nessun principal significa nessun accesso")
	}
}

// TestKeyGrantsIntersectOwner pins the API-key authorization contract: a key
// authenticates as its owner, but a request must pass the key's own grants AND
// the owner's bindings. However the key is scoped, it can never exceed its
// owner; an empty grant set preserves the legacy full-owner power.
func TestKeyGrantsIntersectOwner(t *testing.T) {
	roles := map[string][]Permission{
		"reader": {RegistryRead},
		"writer": {RegistryRead, RegistryWrite},
	}
	ownerBindings := []Binding{
		{Subject: "user:alice", Role: "reader", Scope: "docker:prod:**"},
	}
	e := newTestEngine(roles, ownerBindings)
	alice := func(grants ...KeyGrant) *Principal {
		return &Principal{Name: "alice", KeyGrants: grants}
	}

	if !e.Allows(alice(KeyGrant{Role: "reader", Scope: "docker:prod:team/**"}), RegistryRead, "docker", "prod", "team/api") {
		t.Error("key within owner power should allow")
	}
	if e.Allows(alice(KeyGrant{Role: "reader", Scope: "docker:prod:team/**"}), RegistryRead, "docker", "prod", "other/api") {
		t.Error("key scope must narrow even inside owner power")
	}
	if e.Allows(alice(KeyGrant{Role: "writer", Scope: "docker:prod:**"}), RegistryWrite, "docker", "prod", "team/api") {
		t.Error("key must not grant what the owner lacks")
	}
	// Owner with no bindings at all: the key side alone is never enough.
	bare := newTestEngine(roles, nil)
	if bare.Allows(alice(KeyGrant{Role: "reader", Scope: "*"}), RegistryRead, "docker", "prod", "team/api") {
		t.Error("key must not work without owner grants")
	}
	// Empty grants preserve the legacy behavior.
	if !e.Allows(&Principal{Name: "alice"}, RegistryRead, "docker", "prod", "team/api") {
		t.Error("legacy key (no grants) should keep full owner power")
	}
	// Admin power needs the admin role at global scope on BOTH sides.
	admine := newTestEngine(roles, []Binding{{Subject: "user:root", Role: SystemAdmin, Scope: ScopeAll}})
	root := &Principal{Name: "root", KeyGrants: []KeyGrant{{Role: SystemAdmin, Scope: ScopeAll}}}
	if !admine.IsAdmin(root) {
		t.Error("admin owner with admin key grant should stay admin")
	}
	if e.IsAdmin(&Principal{Name: "alice", KeyGrants: []KeyGrant{{Role: SystemAdmin, Scope: ScopeAll}}}) {
		t.Error("non-admin owner must never gain admin through a key")
	}
}

// TestKeyGrantsKeepOwnerIdentity ensures key sessions resolve to the owner for
// identity purposes: subjects never gain anything key-specific.
func TestKeyGrantsKeepOwnerIdentity(t *testing.T) {
	p := &Principal{Name: "alice", Groups: []string{"devs"}, KeyGrants: []KeyGrant{{Role: "reader", Scope: "*"}}}
	got := map[string]bool{}
	for _, s := range p.Subjects() {
		got[s] = true
	}
	for _, want := range []string{"authenticated", "user:alice", "group:devs"} {
		if !got[want] {
			t.Errorf("manca il soggetto %q", want)
		}
	}
	if len(got) != 3 {
		t.Errorf("soggetti inattesi: %v", got)
	}
}
