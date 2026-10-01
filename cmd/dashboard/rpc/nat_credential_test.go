package rpc

import (
	"testing"

	"github.com/nezhahq/nezha/model"
)

// Values standing in for the credentials this dashboard issues. The fake gate
// below mirrors the contract of controller.IsDashboardCredential; the real
// classification semantics — panel JWTs accepted regardless of expiry,
// foreign-signed JWTs and random strings rejected — are pinned by the
// controller package tests (dashboard_credential_test.go).
const (
	natTestDashboardJWT        = "Bearer dashboard-signed-jwt"
	natTestDashboardExpiredJWT = "Bearer dashboard-signed-expired-jwt"
	natTestDashboardAPIToken   = "Bearer " + model.APITokenPrefix + "dashboard-issued-pat"
	natTestForeignJWT          = "Bearer foreign-signed-jwt"
	natTestForeignRandom       = "Bearer foreign-random-string"
)

// installNATCredentialTestGate wires a fake dashboard-credential classifier
// for the duration of the test: exactly the dashboard-issued test values above
// are classified as dashboard credentials, everything else is foreign.
func installNATCredentialTestGate(t *testing.T) {
	t.Helper()
	original := natDashboardCredentialGate
	natDashboardCredentialGate = func(authz string) bool {
		switch authz {
		case natTestDashboardJWT, natTestDashboardExpiredJWT, natTestDashboardAPIToken:
			return true
		default:
			return false
		}
	}
	t.Cleanup(func() { natDashboardCredentialGate = original })
}
