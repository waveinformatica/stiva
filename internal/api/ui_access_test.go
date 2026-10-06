package api

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"registry/internal/auth"
	"registry/internal/authz"
	"registry/internal/registry"
)

// ctxAs builds a gin context carrying the given principal (nil = none).
func ctxAs(u *auth.User) *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/api/v1/repositories?registry=private", nil)
	if u != nil {
		c.Set("user", u)
	}
	return c
}

// handlerWith builds a handler whose authorization engine holds the given
// grants, without a database behind it.
func handlerWith(bindings []authz.Binding, rolePerms map[string][]authz.Permission) *Handler {
	return &Handler{authz: authz.NewInMemory(rolePerms, bindings)}
}

var private = &registry.Registry{Name: "private", Format: "docker", Type: "hosted", Online: true}

// TestAccessRequiresAGrant pins the change at the heart of this phase: nothing
// is permitted unless granted. The previous model allowed anonymous reads and
// authenticated writes whenever a registry had no access list, which meant the
// safest-looking configuration — an empty one — was the most permissive.
func TestAccessRequiresAGrant(t *testing.T) {
	h := handlerWith(nil, nil)
	for _, u := range []*auth.User{
		nil,
		{Name: "anonymous", Anonymous: true},
		{Name: "alice"},
		{Name: "bob", Groups: []string{"devs"}},
	} {
		if h.allowAccess(ctxAs(u), private, false) {
			t.Errorf("senza alcun grant nessuno deve leggere (utente %+v)", u)
		}
	}
}

// TestGrantsReachLocalGroups is the defect this phase fixes: a role granted to
// a group must apply to a local account placed in that group.
func TestGrantsReachLocalGroups(t *testing.T) {
	h := handlerWith(
		[]authz.Binding{{Subject: "group:devs", Role: "pusher", Scope: "docker:private:kosmos/**"}},
		map[string][]authz.Permission{"pusher": {authz.RegistryRead, authz.RegistryWrite}},
	)
	bob := &auth.User{Name: "bob", Groups: []string{"devs"}}

	if !h.allowRepo(ctxAs(bob), private, "kosmos/core", true) {
		t.Error("il gruppo concede la scrittura sotto kosmos/")
	}
	if h.allowRepo(ctxAs(bob), private, "apeiron/iot-hub", true) {
		t.Error("fuori dallo scope non deve passare")
	}
	// Chi non è nel gruppo non eredita nulla.
	if h.allowRepo(ctxAs(&auth.User{Name: "carol"}), private, "kosmos/core", false) {
		t.Error("un utente fuori dal gruppo non deve leggere")
	}
}

// TestDeleteIsNotWrite covers the split between pushing a tag and erasing one.
func TestDeleteIsNotWrite(t *testing.T) {
	h := handlerWith(
		[]authz.Binding{{Subject: "user:alice", Role: "pusher", Scope: "docker:private"}},
		map[string][]authz.Permission{"pusher": {authz.RegistryRead, authz.RegistryWrite}},
	)
	alice := ctxAs(&auth.User{Name: "alice"})
	if !h.allowAccess(alice, private, true) {
		t.Error("alice deve poter scrivere")
	}
	if h.allowDelete(ctxAs(&auth.User{Name: "alice"}), private, "kosmos/core") {
		t.Error("scrivere non implica cancellare")
	}
}

// TestNoAuthzEngineDeniesEverything: a missing engine must fail closed.
func TestNoAuthzEngineDeniesEverything(t *testing.T) {
	h := &Handler{}
	if h.allowAccess(ctxAs(&auth.User{Name: "alice"}), private, false) || h.isAdmin(ctxAs(nil)) {
		t.Fatal("senza motore di autorizzazione tutto deve essere negato")
	}
}
