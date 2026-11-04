package registry

import (
	"reflect"
	"sort"
	"testing"
	"time"

	"registry/internal/storage"
)

func tagsAt(now time.Time, spec map[string]int) []storage.TagInfo {
	var out []storage.TagInfo
	for tag, daysAgo := range spec {
		pushed := int64(0)
		if daysAgo >= 0 {
			pushed = now.Add(-time.Duration(daysAgo) * 24 * time.Hour).UnixNano()
		}
		out = append(out, storage.TagInfo{Tag: tag, Digest: "sha256:" + tag, PushedAt: pushed})
	}
	return out
}

func planned(t []storage.TagInfo) []string {
	var out []string
	for _, x := range t {
		out = append(out, x.Tag)
	}
	sort.Strings(out)
	return out
}

func TestPlanCleanup(t *testing.T) {
	now := time.Now()
	tags := tagsAt(now, map[string]int{"v1": 100, "v2": 60, "v3": 30, "v4": 5, "latest": 200, "old": 90, "legacy": -1})

	cases := []struct {
		name string
		p    *CleanupPolicy
		want []string
	}{
		{"nil policy keeps all", nil, nil},
		{"disabled keeps all", &CleanupPolicy{KeepLast: 1}, nil},
		{"enabled but no rule keeps all", &CleanupPolicy{Enabled: true}, nil},
		{"keep last 2", &CleanupPolicy{Enabled: true, KeepLast: 2, Protect: []string{"latest"}},
			[]string{"legacy", "old", "v1", "v2"}}, // newest: v4, v3; unknown push time ranks last
		{"max age 45d keeps recent and unknown", &CleanupPolicy{Enabled: true, MaxAgeDays: 45, Protect: []string{"latest"}},
			[]string{"old", "v1", "v2"}},
		{"union of rules", &CleanupPolicy{Enabled: true, KeepLast: 1, MaxAgeDays: 45, Protect: []string{"latest"}},
			[]string{"old", "v1", "v2"}},
		{"protect glob", &CleanupPolicy{Enabled: true, KeepLast: 1, Protect: []string{"v*", "latest"}},
			[]string{"legacy"}}, // old is the newest unprotected tag, so keep-last-1 keeps it
	}
	for _, c := range cases {
		got := planned(PlanCleanup(tags, c.p, now))
		if !reflect.DeepEqual(got, c.want) && !(len(got) == 0 && len(c.want) == 0) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestPlanCleanupAttachmentsProtected(t *testing.T) {
	now := time.Now()
	tags := tagsAt(now, map[string]int{"sha256-abc.sig": 400, "sha256-abc.att": 400, "v1": 400})
	got := planned(PlanCleanup(tags, &CleanupPolicy{Enabled: true, MaxAgeDays: 30}, now))
	if !reflect.DeepEqual(got, []string{"v1"}) {
		t.Errorf("got %v, want only v1", got)
	}
}

func TestCleanupPolicyValidate(t *testing.T) {
	if err := (&CleanupPolicy{KeepLast: -1}).Validate(); err == nil {
		t.Error("negative keep_last accepted")
	}
	if err := (&CleanupPolicy{Protect: []string{"["}}).Validate(); err == nil {
		t.Error("bad glob accepted")
	}
	var nilp *CleanupPolicy
	if err := nilp.Validate(); err != nil {
		t.Errorf("nil policy: %v", err)
	}
}

func TestPlanArtifactCleanupKeepsVersionsWhole(t *testing.T) {
	now := time.Now()
	at := func(daysAgo int) int64 { return now.Add(-time.Duration(daysAgo) * 24 * time.Hour).UnixNano() }
	objs := []storage.ObjectInfo{
		{Path: "org/acme/lib/1.0.0/lib-1.0.0.jar", CreatedAt: at(90)},
		{Path: "org/acme/lib/1.0.0/lib-1.0.0.jar.sha1", CreatedAt: at(90)},
		{Path: "org/acme/lib/1.0.0/lib-1.0.0.pom", CreatedAt: at(90)},
		{Path: "org/acme/lib/1.1.0/lib-1.1.0.jar", CreatedAt: at(40)},
		{Path: "org/acme/lib/1.2.0/lib-1.2.0.jar", CreatedAt: at(2)},
		{Path: "org/acme/lib/maven-metadata.xml", CreatedAt: at(500)},
		{Path: "org/acme/other/0.1.0/other-0.1.0.jar", CreatedAt: at(300)},
	}
	units := GroupArtifacts(FormatMaven, objs)
	if len(units) != 4 {
		t.Fatalf("units = %d, want 4 (metadata excluded)", len(units))
	}
	// keep newest 1 per package: lib keeps 1.2.0; other keeps its only version.
	del := PlanArtifactCleanup(units, &CleanupPolicy{Enabled: true, KeepLast: 1}, now)
	var got []string
	for _, u := range del {
		got = append(got, u.Package+"@"+u.Version)
	}
	sort.Strings(got)
	if !reflect.DeepEqual(got, []string{"org/acme/lib@1.0.0", "org/acme/lib@1.1.0"}) {
		t.Errorf("keep-last-1 deletes %v", got)
	}
	for _, u := range del {
		if u.Version == "1.0.0" && len(u.Paths) != 3 {
			t.Errorf("1.0.0 must go whole with its companions, got %d paths", len(u.Paths))
		}
	}
	// max age 60d + protect 1.0.0: only nothing old unprotected remains except other@0.1.0.
	del = PlanArtifactCleanup(units, &CleanupPolicy{Enabled: true, MaxAgeDays: 60, Protect: []string{"1.0.0"}}, now)
	got = nil
	for _, u := range del {
		got = append(got, u.Package+"@"+u.Version)
	}
	if !reflect.DeepEqual(got, []string{"org/acme/other@0.1.0"}) {
		t.Errorf("max-age-60 + protect deletes %v", got)
	}
}
