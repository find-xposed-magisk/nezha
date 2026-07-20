package rpc

import (
	"errors"
	"testing"
)

func TestIOStreamStateSnapshotAndGeneration(t *testing.T) {
	handler := NewNezhaHandler()
	initial := handler.SnapshotIOStreamState()
	if initial.Count != 0 || initial.Generation != 0 {
		t.Fatalf("unexpected initial state: %+v", initial)
	}
	if err := handler.CreateStream("state-stream", 1, 1); err != nil {
		t.Fatal(err)
	}
	created := handler.SnapshotIOStreamState()
	if created.Count != 1 || created.Generation != 1 {
		t.Fatalf("unexpected created state: %+v", created)
	}
	if err := handler.CreateStream("state-stream", 2, 2); !errors.Is(err, ErrStreamAlreadyExists) {
		t.Fatalf("duplicate create error: %v", err)
	}
	if got := handler.SnapshotIOStreamState(); got != created {
		t.Fatalf("duplicate create changed state: %+v", got)
	}
	if err := handler.CloseStream("unknown"); err != nil {
		t.Fatal(err)
	}
	if got := handler.SnapshotIOStreamState(); got != created {
		t.Fatalf("unknown close changed state: %+v", got)
	}
}

func TestIOStreamStateRevocationPublishesOncePerBatch(t *testing.T) {
	handler := NewNezhaHandler()
	if err := handler.CreateStreamWithPurpose("purpose-a", 0, 1, PurposeMCPTransfer); err != nil {
		t.Fatal(err)
	}
	if err := handler.CreateStreamWithPurpose("purpose-b", 0, 1, PurposeMCPTransfer); err != nil {
		t.Fatal(err)
	}
	if err := handler.CreateStream("server-a", 0, 2); err != nil {
		t.Fatal(err)
	}
	before := handler.SnapshotIOStreamState()
	if revoked := handler.RevokeStreamsForPurpose(PurposeMCPTransfer); revoked != 2 {
		t.Fatalf("revoked purpose streams: %d", revoked)
	}
	afterPurpose := handler.SnapshotIOStreamState()
	if afterPurpose.Generation != before.Generation+1 || afterPurpose.Count != 1 {
		t.Fatalf("purpose revocation state: before=%+v after=%+v", before, afterPurpose)
	}
	if revoked := handler.RevokeStreamsForPurpose(PurposeMCPTransfer); revoked != 0 {
		t.Fatalf("repeat purpose revocation: %d", revoked)
	}
	if got := handler.SnapshotIOStreamState(); got != afterPurpose {
		t.Fatalf("empty purpose revocation changed state: %+v", got)
	}
	handler.RevokeStreamsForServer(2)
	if got := handler.SnapshotIOStreamState(); got.Generation != afterPurpose.Generation+1 || got.Count != 0 {
		t.Fatalf("server revocation state: %+v", got)
	}
	handler.RevokeStreamsForServer(2)
	if got := handler.SnapshotIOStreamState(); got.Generation != afterPurpose.Generation+1 {
		t.Fatalf("empty server revocation changed generation: %+v", got)
	}
}
