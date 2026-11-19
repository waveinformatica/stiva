package storage

import "testing"

// Il testo cercato e' una sottostringa letterale: % e _ non sono jolly.
func TestLikePatternEscapes(t *testing.T) {
	cases := map[string]string{
		"openssl": "%openssl%",
		"100%":    `%100\%%`,
		"lib_ssl": `%lib\_ssl%`,
		`a\b`:     `%a\\b%`,
		"":        "%%",
	}
	for in, want := range cases {
		if got := likePattern(in); got != want {
			t.Errorf("likePattern(%q) = %q, atteso %q", in, got, want)
		}
	}
}
