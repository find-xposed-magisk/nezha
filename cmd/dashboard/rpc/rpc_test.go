package rpc

import (
	"context"
	"net"
	"testing"

	"google.golang.org/grpc/peer"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/service/singleton"
)

// peerCtx builds a context carrying a gRPC peer address the same way the
// real transport does, so ctxWithRealIP's peer.FromContext + netip parsing
// path is exercised end-to-end.
func peerCtx(addr string) context.Context {
	tcp, err := net.ResolveTCPAddr("tcp", addr)
	if err != nil {
		panic(err)
	}
	return peer.NewContext(context.Background(), &peer.Peer{Addr: tcp})
}

func withRealIPHeader(t *testing.T, header string) {
	t.Helper()
	conf := &model.Config{}
	conf.AgentRealIPHeader = header
	orig := singleton.Conf
	singleton.Conf = &singleton.ConfigClass{Config: conf}
	t.Cleanup(func() { singleton.Conf = orig })
}

// TestCtxWithRealIP_PeerIPModePopulatesRealIP pins the regression: in
// AgentRealIPHeader == ConfigUsePeerIP mode the resolved real IP must equal
// the connecting peer IP. If it stays empty, model.CheckIP / model.BlockIP
// short-circuit on "" and the gRPC WAF + brute-force blocking are bypassed.
func TestCtxWithRealIP_PeerIPModePopulatesRealIP(t *testing.T) {
	withRealIPHeader(t, model.ConfigUsePeerIP)

	ctx, err := ctxWithRealIP(peerCtx("203.0.113.7:54321"))
	if err != nil {
		t.Fatalf("ctxWithRealIP returned error: %v", err)
	}

	realIP, _ := ctx.Value(model.CtxKeyRealIP{}).(string)
	if realIP != "203.0.113.7" {
		t.Fatalf("CtxKeyRealIP = %q, want %q (empty defeats WAF/BlockIP)", realIP, "203.0.113.7")
	}

	connIP, _ := ctx.Value(model.CtxKeyConnectingIP{}).(string)
	if connIP != "203.0.113.7" {
		t.Fatalf("CtxKeyConnectingIP = %q, want %q", connIP, "203.0.113.7")
	}
}

// TestCtxWithRealIP_PeerIPModeIPv6 confirms the port is stripped and the IPv6
// literal is normalized for the peer-IP path.
func TestCtxWithRealIP_PeerIPModeIPv6(t *testing.T) {
	withRealIPHeader(t, model.ConfigUsePeerIP)

	ctx, err := ctxWithRealIP(peerCtx("[2001:db8::1]:443"))
	if err != nil {
		t.Fatalf("ctxWithRealIP returned error: %v", err)
	}

	realIP, _ := ctx.Value(model.CtxKeyRealIP{}).(string)
	if realIP != "2001:db8::1" {
		t.Fatalf("CtxKeyRealIP = %q, want %q", realIP, "2001:db8::1")
	}
}

// TestCtxWithRealIP_PeerIPModeNoPeer keeps the documented failure behaviour
// when no connecting IP can be derived.
func TestCtxWithRealIP_PeerIPModeNoPeer(t *testing.T) {
	withRealIPHeader(t, model.ConfigUsePeerIP)

	_, err := ctxWithRealIP(context.Background())
	if err == nil {
		t.Fatal("expected error when connecting IP cannot be resolved in peer-IP mode")
	}
}

// TestCtxWithRealIP_NoHeaderLeavesRealIPUnset pins the intended behaviour when
// no real-IP header is configured: this is an explicit "no IP-based WAF" stance,
// so ctxWithRealIP must not error and must leave CtxKeyRealIP unset (CheckIP
// then no-ops on empty IP). CtxKeyConnectingIP is still populated for the
// nezha.go fallback. Guards against accidentally coupling the no-header path to
// the peer-IP fix.
func TestCtxWithRealIP_NoHeaderLeavesRealIPUnset(t *testing.T) {
	withRealIPHeader(t, "")

	ctx, err := ctxWithRealIP(peerCtx("203.0.113.7:54321"))
	if err != nil {
		t.Fatalf("no-header mode must not error, got %v", err)
	}

	if v := ctx.Value(model.CtxKeyRealIP{}); v != nil {
		t.Fatalf("CtxKeyRealIP must be unset when no real-IP header is configured, got %v", v)
	}

	connIP, _ := ctx.Value(model.CtxKeyConnectingIP{}).(string)
	if connIP != "203.0.113.7" {
		t.Fatalf("CtxKeyConnectingIP = %q, want %q", connIP, "203.0.113.7")
	}
}
