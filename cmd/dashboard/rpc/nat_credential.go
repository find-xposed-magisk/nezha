package rpc

import "net/http"

// natDashboardCredentialGate classifies an Authorization header value as a
// credential issued by this dashboard (panel-signed JWT or panel API token).
// main() injects controller.IsDashboardCredential; the setter keeps the rpc
// package free of a dependency on the controller package, the same pattern as
// SetReceiptGateListener and SetMCPKillSwitchObserver.
var natDashboardCredentialGate func(authz string) bool

// SetNATDashboardCredentialGate wires the dashboard-credential classifier used
// by the NAT ingress. Passing nil restores the fail-closed default in
// stripDashboardCredential.
func SetNATDashboardCredentialGate(fn func(authz string) bool) {
	natDashboardCredentialGate = fn
}

// stripDashboardCredential removes the Authorization header when it carries a
// credential issued by this dashboard (panel-signed JWT or panel API token).
// Such credentials authenticate the dashboard itself and must never become
// origin credentials for the NAT backend. Foreign credentials are forwarded
// untouched: the NAT backend may authenticate with credentials of its own, and
// this dashboard must not make policy for them.
func stripDashboardCredential(request *http.Request) {
	if natDashboardCredentialGate == nil {
		// Gate not wired (early startup or tests): fail closed and keep the
		// historical behavior of stripping every Authorization header.
		request.Header.Del("Authorization")
		return
	}
	// A multi-valued Authorization header is malformed traffic, but any
	// dashboard-issued value in it must still not survive into the tunnel, so
	// the whole header is dropped when one is recognized.
	for _, value := range request.Header.Values("Authorization") {
		if natDashboardCredentialGate(value) {
			request.Header.Del("Authorization")
			return
		}
	}
}
