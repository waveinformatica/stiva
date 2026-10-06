package authz

import "testing"

// TestScopeMatches covers the granularity the model promises: a whole registry,
// a namespace prefix of arbitrary depth, or a single repository.
func TestScopeMatches(t *testing.T) {
	cases := []struct {
		scope             Scope
		format, reg, repo string
		want              bool
	}{
		{"*", "docker", "main", "kosmos/core", true},

		// Un intero formato, senza elencare i registry.
		{"helm", "helm", "charts", "wave/app", true},
		{"helm", "docker", "main", "kosmos/core", false},

		// Formato + registry.
		{"docker:main", "docker", "main", "kosmos/core", true},
		{"docker:main", "docker", "altro", "kosmos/core", false},
		{"docker:main", "helm", "main", "kosmos/core", false},

		// Il pattern vale per qualunque registry di quel formato.
		{"docker:*:kosmos/**", "docker", "main", "kosmos/core", true},
		{"docker:*:kosmos/**", "docker", "altro", "kosmos/a/b", true},
		{"docker:*:kosmos/**", "docker", "main", "apeiron/iot-hub", false},

		// Tutte e tre le posizioni.
		{"maven:libs:com/wave/**", "maven", "libs", "com/wave/ocean", true},
		{"maven:libs:com/wave/**", "maven", "libs", "org/altro/x", false},

		// Profondità arbitraria, come nei dati reali.
		{"docker:main:fca-nbes/**", "docker", "main", "fca-nbes/fca-nbes/upm", true},

		// "sotto kosmos/", non il repository chiamato kosmos.
		{"docker:main:kosmos/**", "docker", "main", "kosmos", false},

		// Uno scope ristretto non risponde su tutto il registry.
		{"docker:main:kosmos/**", "docker", "main", "", false},
		{"docker:main", "docker", "main", "", true},
	}
	for _, c := range cases {
		if got := c.scope.Matches(c.format, c.reg, c.repo); got != c.want {
			t.Errorf("Scope(%q).Matches(%q,%q,%q) = %v, atteso %v", c.scope, c.format, c.reg, c.repo, got, c.want)
		}
	}
}

func TestScopeParts(t *testing.T) {
	// Le posizioni finali omesse valgono "*": "docker" e "docker:*:*" sono lo
	// stesso scope, così la forma breve resta leggibile.
	f, r, p := Scope("docker").Parts()
	if f != "docker" || r != "*" || p != "*" {
		t.Fatalf("Parts = %q %q %q", f, r, p)
	}
	f, r, p = Scope("maven:libs:com/wave/**").Parts()
	if f != "maven" || r != "libs" || p != "com/wave/**" {
		t.Fatalf("Parts = %q %q %q", f, r, p)
	}
}

func TestScopeValid(t *testing.T) {
	for _, s := range []Scope{"*", "docker", "docker:main", "docker:*:kosmos/**"} {
		if !s.Valid() {
			t.Errorf("%q dovrebbe essere valido", s)
		}
	}
	// Un formato jolly con un registry specifico è contraddittorio.
	for _, s := range []Scope{"", "*:main", "*:main:kosmos/**"} {
		if s.Valid() {
			t.Errorf("%q non dovrebbe essere valido", s)
		}
	}
}

// TestPermissionVocabulary pins that the set is closed: anything outside it is
// refused at write time rather than stored and silently never matched, which is
// exactly how the previous free-text permissions became decorative.
func TestPermissionVocabulary(t *testing.T) {
	if !RegistryRead.Valid() || !AdminUsers.Valid() {
		t.Fatal("i permessi dichiarati devono essere validi")
	}
	if Permission("registry:frobnicate").Valid() {
		t.Fatal("un permesso inventato non deve essere accettato")
	}
	if RegistryRead.IsAdmin() || !AdminStores.IsAdmin() {
		t.Fatal("classificazione admin errata")
	}
	for _, p := range All {
		if Describe[p] == "" {
			t.Errorf("il permesso %q non ha descrizione: la UI mostrerebbe una voce muta", p)
		}
	}
}
