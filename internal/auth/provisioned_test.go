package auth

import (
	"errors"
	"os"
	"testing"

	"registry/internal/authz"
)

func TestReadServiceAccounts(t *testing.T) {
	files := map[string]string{
		"/keys/scanner": "rk_0123456789abcdef0123456789abcdef01234567\n",
		"/keys/warmer":  "rk_fedcba9876543210fedcba9876543210fedcba98",
		"/keys/short":   "rk_corta",
		"/keys/nopfx":   "0123456789abcdef0123456789abcdef0123456789",
	}
	read := func(p string) ([]byte, error) {
		if v, ok := files[p]; ok {
			return []byte(v), nil
		}
		return nil, os.ErrNotExist
	}
	ok := []ServiceAccount{
		{Name: "stiva-scanner", Role: "scanner", Scope: authz.ScopeAll, KeyFile: "/keys/scanner"},
		{Name: "stiva-warmer", Role: "warmer", Scope: "oci:cache", KeyFile: "/keys/warmer"},
	}
	got, err := readServiceAccounts(ok, read)
	if err != nil {
		t.Fatal(err)
	}
	// Il newline finale del Secret non fa parte della chiave.
	if got[0].key != "rk_0123456789abcdef0123456789abcdef01234567" || got[1].Scope != "oci:cache" {
		t.Errorf("letti: %+v", got)
	}
	if got, err := readServiceAccounts(nil, read); err != nil || len(got) != 0 {
		t.Errorf("dichiarazione vuota: %v %v", got, err)
	}

	cases := map[string]ServiceAccount{
		"senza nome":        {Role: "scanner", Scope: authz.ScopeAll, KeyFile: "/keys/scanner"},
		"senza ruolo":       {Name: "x", Scope: authz.ScopeAll, KeyFile: "/keys/scanner"},
		"senza file":        {Name: "x", Role: "scanner", Scope: authz.ScopeAll},
		"superutente":       {Name: "x", Role: authz.SystemAdmin, Scope: authz.ScopeAll, KeyFile: "/keys/scanner"},
		"ambito non valido": {Name: "x", Role: "scanner", Scope: "*:cache", KeyFile: "/keys/scanner"},
		"file mancante":     {Name: "x", Role: "scanner", Scope: authz.ScopeAll, KeyFile: "/keys/nessuno"},
		"chiave corta":      {Name: "x", Role: "scanner", Scope: authz.ScopeAll, KeyFile: "/keys/short"},
		"chiave senza rk_":  {Name: "x", Role: "scanner", Scope: authz.ScopeAll, KeyFile: "/keys/nopfx"},
	}
	for name, a := range cases {
		if _, err := readServiceAccounts([]ServiceAccount{a}, read); err == nil {
			t.Errorf("%s: atteso un errore", name)
		}
	}
	if _, err := readServiceAccounts([]ServiceAccount{ok[0], ok[0]}, read); err == nil {
		t.Error("account dichiarato due volte accettato")
	}
	_, err = readServiceAccounts([]ServiceAccount{{Name: "x", Role: "scanner", Scope: authz.ScopeAll, KeyFile: "/keys/nessuno"}}, read)
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("l'errore di lettura deve restare riconoscibile: %v", err)
	}
}
