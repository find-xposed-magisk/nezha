//go:build linux

package scenario

import (
	"errors"
	"net/http"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/fixture"
)

func proveHeldNATRequest(observed fixture.NATEchoRecord, domain, identity string) error {
	if observed.Method != http.MethodPatch || observed.Path != "/held/"+identity || observed.Host != domain || observed.HeaderValue != identity || string(observed.Body) != "held-body-"+identity || observed.SensitiveHeadersPresent {
		return errors.New("held NAT request did not match exact protocol proof")
	}
	return nil
}
