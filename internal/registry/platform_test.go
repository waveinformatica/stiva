package registry

import "testing"

func TestParsePlatform(t *testing.T) {
	p, err := ParsePlatform("linux/arm/v7")
	if err != nil || p != (Platform{OS: "linux", Architecture: "arm", Variant: "v7"}) || p.String() != "linux/arm/v7" {
		t.Errorf("linux/arm/v7: %+v %v", p, err)
	}
	if p, err := ParsePlatform("linux/amd64"); err != nil || p.Variant != "" || p.String() != "linux/amd64" {
		t.Errorf("linux/amd64: %+v %v", p, err)
	}
	for _, bad := range []string{"", "linux", "linux/", "/amd64", "linux/arm/v7/x"} {
		if _, err := ParsePlatform(bad); err == nil {
			t.Errorf("%q accettata", bad)
		}
	}
}

func TestMatchesPlatform(t *testing.T) {
	amd64 := []Platform{{OS: "linux", Architecture: "amd64"}}
	arm64 := []Platform{{OS: "linux", Architecture: "arm64"}}
	armv7 := []Platform{{OS: "linux", Architecture: "arm", Variant: "v7"}}
	cases := []struct {
		entry *Platform
		want  []Platform
		ok    bool
	}{
		{&Platform{OS: "linux", Architecture: "amd64"}, amd64, true},
		{&Platform{OS: "linux", Architecture: "arm64", Variant: "v8"}, arm64, true}, // variante non richiesta
		{&Platform{OS: "linux", Architecture: "arm", Variant: "v6"}, armv7, false},
		{&Platform{OS: "linux", Architecture: "arm", Variant: "v7"}, armv7, true},
		{&Platform{OS: "windows", Architecture: "amd64"}, amd64, false},
		{&Platform{OS: "unknown", Architecture: "unknown"}, amd64, false}, // attestazione di build
		{nil, amd64, false},
	}
	for _, c := range cases {
		if got := matchesPlatform(c.entry, c.want); got != c.ok {
			t.Errorf("%+v contro %+v: %v, atteso %v", c.entry, c.want, got, c.ok)
		}
	}
}
