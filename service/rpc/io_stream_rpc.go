package rpc

import (
	"fmt"
	"log"
	"time"

	"github.com/nezhahq/nezha/pkg/grpcx"
	pb "github.com/nezhahq/nezha/proto"
)

func (s *NezhaHandler) IOStream(stream pb.NezhaService_IOStreamServer) error {
	clientID, err := s.Auth.Check(stream.Context())
	if err != nil {
		return err
	}
	id, err := stream.Recv()
	if err != nil {
		return err
	}
	if id == nil || !isValidIOStreamMagic(id.Data) {
		return fmt.Errorf("invalid stream id")
	}
	streamID := string(id.Data[4:])
	if !s.IsStreamAuthorizedForAgent(streamID, clientID) {
		return fmt.Errorf("stream not authorized for agent")
	}
	if _, err := s.GetStream(streamID); err != nil {
		return err
	}
	wrapper := grpcx.NewIOStreamWrapper(stream)
	keepaliveDone := make(chan struct{})
	go func() {
		defer close(keepaliveDone)
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-wrapper.Context().Done():
				return
			case <-wrapper.Done():
				return
			case <-ticker.C:
				if err := wrapper.SendKeepalive(); err != nil {
					log.Printf("NEZHA>> IOStream keepAlive error: %v\n", err)
					return
				}
			}
		}
	}()
	if err := s.AgentConnected(streamID, wrapper); err != nil {
		_ = wrapper.Close()
		return err
	}
	wrapper.Wait()
	<-keepaliveDone
	return nil
}
