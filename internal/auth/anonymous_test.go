package auth

import (
	"net"
	"net/http/httptest"
	"testing"
)

// TestClientIPIgnoresForgeableHeader is the property that decides whether an
// address-restricted anonymous identity is security or decoration: with no
// trusted proxies declared, X-Forwarded-For must not influence the result.
func TestClientIPIgnoresForgeableHeader(t *testing.T) {
	r := httptest.NewRequest("GET", "/v2/", nil)
	r.RemoteAddr = "203.0.113.9:5000"
	r.Header.Set("X-Forwarded-For", "172.16.0.5")

	if got := ClientIP(r, 0); !got.Equal(net.ParseIP("203.0.113.9")) {
		t.Fatalf("senza proxy fidati l'header va ignorato, ottenuto %v", got)
	}

	// Con un hop fidato si prende l'ultima voce, quella scritta dal proxy.
	r2 := httptest.NewRequest("GET", "/v2/", nil)
	r2.RemoteAddr = "10.0.0.1:5000"
	r2.Header.Set("X-Forwarded-For", "1.2.3.4, 172.16.0.5")
	if got := ClientIP(r2, 1); !got.Equal(net.ParseIP("172.16.0.5")) {
		t.Fatalf("con un proxy fidato atteso 172.16.0.5, ottenuto %v", got)
	}

	// Un client che finge una catena non deve poter far scegliere la sua voce.
	r3 := httptest.NewRequest("GET", "/v2/", nil)
	r3.RemoteAddr = "10.0.0.1:5000"
	r3.Header.Set("X-Forwarded-For", "172.16.0.5, 203.0.113.9")
	if got := ClientIP(r3, 1); got.Equal(net.ParseIP("172.16.0.5")) {
		t.Fatal("con un solo hop fidato la voce falsificata a sinistra non deve vincere")
	}
}

func TestAnonymousMatching(t *testing.T) {
	k8s := AnonymousIdentity{Name: "anon-k8s", CIDRs: []string{"172.16.0.0/16", "192.168.2.0/24"}}
	all := AnonymousIdentity{Name: "anon-public"}
	off := AnonymousIdentity{Name: "spento", Disabled: true}

	if !k8s.Matches(net.ParseIP("172.16.88.4")) {
		t.Error("un indirizzo dentro il CIDR deve corrispondere")
	}
	if k8s.Matches(net.ParseIP("203.0.113.9")) {
		t.Error("un indirizzo fuori non deve corrispondere")
	}
	if k8s.Matches(nil) {
		t.Error("senza indirizzo un'identità filtrata non può corrispondere")
	}
	if !all.Matches(net.ParseIP("203.0.113.9")) || !all.Matches(nil) {
		t.Error("un'identità senza filtri accetta chiunque")
	}
	if off.Matches(net.ParseIP("1.2.3.4")) {
		t.Error("un'identità disabilitata non corrisponde mai")
	}
	if !k8s.Filtered() || all.Filtered() {
		t.Error("Filtered() errata")
	}
}
