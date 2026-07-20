package rpc

import (
	"context"
	"fmt"
	"log"
	"net/netip"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/pkg/utils"
	"github.com/nezhahq/nezha/service/singleton"
)

func ctxWithRealIP(ctx context.Context) (context.Context, error) {
	var ip, connectingIp string
	p, ok := peer.FromContext(ctx)
	if ok {
		addrPort, err := netip.ParseAddrPort(p.Addr.String())
		if err == nil {
			connectingIp = addrPort.Addr().String()
		}
	}
	ctx = context.WithValue(ctx, model.CtxKeyConnectingIP{}, connectingIp)

	if singleton.Conf.AgentRealIPHeader == "" {
		return ctx, nil
	}

	if singleton.Conf.AgentRealIPHeader == model.ConfigUsePeerIP {
		if connectingIp == "" {
			return ctx, fmt.Errorf("connecting ip not found")
		}
		ip = connectingIp
	} else {
		vals := metadata.ValueFromIncomingContext(ctx, singleton.Conf.AgentRealIPHeader)
		if len(vals) == 0 {
			return ctx, fmt.Errorf("real ip header not found")
		}
		var err error
		ip, err = utils.GetIPFromHeader(vals[0])
		if err != nil {
			return ctx, err
		}
	}

	if singleton.Conf.Debug {
		log.Printf("NEZHA>> gRPC Agent Real IP: %s, connecting IP: %s\n", ip, connectingIp)
	}

	return context.WithValue(ctx, model.CtxKeyRealIP{}, ip), nil
}

func waf(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	realip, _ := ctx.Value(model.CtxKeyRealIP{}).(string)
	if err := model.CheckIP(singleton.DB, realip); err != nil {
		return nil, err
	}
	return handler(ctx, req)
}

func getRealIp(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	ctx, err := ctxWithRealIP(ctx)
	if err != nil {
		return nil, err
	}
	return handler(ctx, req)
}

type realIPServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *realIPServerStream) Context() context.Context { return s.ctx }

func getRealIpStream(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	ctx, err := ctxWithRealIP(ss.Context())
	if err != nil {
		return err
	}
	return handler(srv, &realIPServerStream{ServerStream: ss, ctx: ctx})
}

func wafStream(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	realip, _ := ss.Context().Value(model.CtxKeyRealIP{}).(string)
	if err := model.CheckIP(singleton.DB, realip); err != nil {
		return err
	}
	return handler(srv, ss)
}
