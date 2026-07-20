//go:build linux && agentcompat

package scenario

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"slices"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/client"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/dashboard"
)

func realHeldSessionSetAgentOrdinals(plan StressPlan) []int {
	ordinals := make([]int, 0, heldSessionSetAgentCount)
	for _, session := range plan.Sessions {
		if !slices.Contains(ordinals, session.Agent.Int()) {
			ordinals = append(ordinals, session.Agent.Int())
		}
	}
	for ordinal := 1; ordinal <= heldSessionSetAgentCount; ordinal++ {
		if !slices.Contains(ordinals, ordinal) {
			ordinals = append(ordinals, ordinal)
		}
	}
	return ordinals
}

type heldRealPATIdentity struct {
	Client       *client.Client
	TokenID      uint64
	ServerIDs    []uint64
	IdentitySeen bool
}

func mintHeldRealPAT(ctx context.Context, dashboardInstance *dashboard.Dashboard, name string, serverIDs []uint64) (heldRealPATIdentity, error) {
	pat, err := createTerminalPAT(ctx, dashboardInstance, name, serverIDs)
	if err != nil {
		return heldRealPATIdentity{}, err
	}
	identity, err := client.CallTool[struct{}, client.WhoAmIResult](ctx, pat, client.ToolCall[struct{}]{Name: "meta.whoami", Arguments: struct{}{}})
	if err != nil {
		return heldRealPATIdentity{}, err
	}
	whoami := identity.StructuredContent
	if whoami.TokenID == 0 || whoami.TokenName != name || !slices.Equal(whoami.Scopes, []string{"nezha:*"}) || !slices.Equal(whoami.ServerIDs, serverIDs) {
		return heldRealPATIdentity{}, errors.New("PAT identity or server allowlist mismatch")
	}
	return heldRealPATIdentity{Client: pat, TokenID: whoami.TokenID, ServerIDs: append([]uint64(nil), serverIDs...), IdentitySeen: true}, nil
}

func createTerminalPAT(ctx context.Context, dashboardInstance *dashboard.Dashboard, name string, serverIDs []uint64) (*client.Client, error) {
	pat, err := client.DoREST[terminalPATRequest, terminalPATResponse](ctx, dashboardInstance.Clients().REST, client.RESTRequest[terminalPATRequest]{Method: http.MethodPost, Path: "/api/v1/api-tokens", Body: &terminalPATRequest{Name: name, Scopes: []string{"nezha:*"}, ServerIDs: serverIDs}})
	if err != nil {
		return nil, err
	}
	if pat.Token == "" {
		return nil, errors.New("PAT response omitted token")
	}
	return dashboardInstance.AuthenticatedClient(pat.Token)
}

func heldRealDigest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}
