package main

import "testing"

func TestDashboardListenerAddress_PreservesCurrentSemantics(t *testing.T) {
	tests := []struct {
		name string
		host string
		port uint16
		want string
	}{
		{name: "empty host", host: "", port: 8008, want: ":8008"},
		{name: "loopback IPv4", host: "127.0.0.1", port: 8008, want: "127.0.0.1:8008"},
		{name: "loopback IPv6", host: "::1", port: 8008, want: "::1:8008"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := dashboardListenerAddress(test.host, test.port)

			if got != test.want {
				t.Fatalf("dashboardListenerAddress(%q, %d) = %q, want %q", test.host, test.port, got, test.want)
			}
		})
	}
}
