package singleton

import (
	"testing"

	"github.com/nezhahq/nezha/model"
)

func withReservedHostConf(t *testing.T, c *model.Config) {
	t.Helper()
	original := Conf
	Conf = &ConfigClass{Config: c}
	t.Cleanup(func() { Conf = original })
}

// GHSA-x6fg-52vr-hj4w: the reserved-host check is the single source of truth
// for both the create/update guard and the startup cache filter. It must
// reject any NAT domain whose hostname collides with the dashboard's own
// InstallHost / ListenHost, regardless of port or case.
func TestIsReservedDashboardHost(t *testing.T) {
	withReservedHostConf(t, &model.Config{
		ConfigDashboard: model.ConfigDashboard{InstallHost: "dashboard.example:8008"},
		ListenHost:      "10.0.0.5",
		ListenPort:      8008,
	})

	cases := []struct {
		name   string
		domain string
		want   bool
	}{
		{"exact install host", "dashboard.example:8008", true},
		{"install host case-insensitive", "Dashboard.Example:8008", true},
		{"install host without port", "dashboard.example", true},
		{"install host arbitrary port", "dashboard.example:8443", true},
		{"listen host and port", "10.0.0.5:8008", true},
		{"listen host bare", "10.0.0.5", true},
		{"unrelated domain", "tunnel.member.example", false},
		{"empty domain", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsReservedDashboardHost(tc.domain); got != tc.want {
				t.Fatalf("IsReservedDashboardHost(%q) = %v, want %v", tc.domain, got, tc.want)
			}
		})
	}
}

// GHSA-x6fg-52vr-hj4w (reverse-proxy coverage): InstallHost/ListenHost alone
// cannot cover a dashboard reached through a reverse proxy on a public domain
// that the dashboard process never sees. ReservedHosts lets the operator
// declare those extra hostnames (comma-separated) so members still cannot
// register a NAT domain that collides with the public entry point.
func TestIsReservedDashboardHostHonoursReservedHostsList(t *testing.T) {
	withReservedHostConf(t, &model.Config{
		ConfigDashboard: model.ConfigDashboard{
			InstallHost:   "internal.example:8008",
			ReservedHosts: "panel.example.com, Admin.Example.COM:443 , ",
		},
	})

	reserved := []string{
		"panel.example.com",
		"panel.example.com:8443",
		"admin.example.com",
		"ADMIN.EXAMPLE.COM:443",
		"internal.example",
	}
	for _, d := range reserved {
		if !IsReservedDashboardHost(d) {
			t.Errorf("IsReservedDashboardHost(%q) = false, want true (declared reserved host)", d)
		}
	}

	if IsReservedDashboardHost("tunnel.member.example") {
		t.Error("unrelated member domain must not be reserved")
	}
	if IsReservedDashboardHost("") {
		t.Error("empty domain must not be reserved")
	}
}

// The startup cache must not load a NAT record whose domain is reserved, so a
// malicious record planted before the patch cannot keep hijacking dashboard
// routing after upgrade. filterReservedNATProfiles is the gate NewNATClass
// runs over the DB result set.
func TestFilterReservedNATProfilesDropsReserved(t *testing.T) {
	withReservedHostConf(t, &model.Config{
		ConfigDashboard: model.ConfigDashboard{InstallHost: "dashboard.example:8008"},
	})

	in := []*model.NAT{
		{Common: model.Common{ID: 1}, Domain: "dashboard.example", Enabled: true},
		{Common: model.Common{ID: 2}, Domain: "tunnel.member.example", Enabled: true},
		{Common: model.Common{ID: 3}, Domain: "Dashboard.Example:9999", Enabled: false},
	}
	out := filterReservedNATProfiles(in)

	if len(out) != 1 {
		t.Fatalf("expected only the non-reserved profile to survive, got %d", len(out))
	}
	if out[0].Domain != "tunnel.member.example" {
		t.Fatalf("surviving profile must be the member tunnel, got %q", out[0].Domain)
	}
}

// GHSA-x6fg-52vr-hj4w (canonical-host coverage): the routing match is an exact
// lookup on r.Host, so a member who registers a NAT Domain that is a DNS/IP
// *equivalent* of the dashboard host — but a different literal string — still
// hijacks the matching r.Host. The guard must collapse the trailing DNS dot and
// the IPv6 compressed/expanded forms, or these variants slip past create/update.
func TestIsReservedDashboardHostCollapsesEquivalentForms(t *testing.T) {
	withReservedHostConf(t, &model.Config{
		ConfigDashboard: model.ConfigDashboard{
			InstallHost:   "panel.example.com",
			ReservedHosts: "[::1]:8008",
		},
	})

	reserved := []string{
		"panel.example.com.",             // trailing dot, no port
		"panel.example.com.:8008",        // trailing dot with port
		"PANEL.EXAMPLE.COM.",             // trailing dot, mixed case
		"[0:0:0:0:0:0:0:1]:8008",         // IPv6 expanded form of ::1
		"::1",                            // IPv6 compressed, bare
		"[::1]",                          // IPv6 compressed, bracketed
	}
	for _, d := range reserved {
		if !IsReservedDashboardHost(d) {
			t.Errorf("IsReservedDashboardHost(%q) = false, want true (equivalent of reserved host)", d)
		}
	}

	if IsReservedDashboardHost("tunnel.member.example.") {
		t.Error("unrelated member domain with trailing dot must not be reserved")
	}
}
