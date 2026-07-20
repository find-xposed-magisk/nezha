//go:build linux

package scenario

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/agent"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/client"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/fixture"
	processharness "github.com/nezhahq/nezha/integration/agentcompat/internal/process"
)

type legacyFMRecordingWriter struct {
	frames []client.Frame
}

func (writer *legacyFMRecordingWriter) WriteFrame(_ context.Context, frame client.Frame) error {
	writer.frames = append(writer.frames, frame)
	return nil
}

type legacyFMFilesystemWriter struct {
	frames int
}

func (writer *legacyFMFilesystemWriter) WriteFrame(_ context.Context, frame client.Frame) error {
	writer.frames++
	switch frame.Payload[0] {
	case 0:
		_, err := os.ReadDir(string(frame.Payload[1:]))
		return err
	case 1:
		_, err := os.ReadFile(string(frame.Payload[1:]))
		return err
	case 2:
		return os.WriteFile(string(frame.Payload[9:]), []byte("uploaded"), 0o600)
	default:
		return errors.New("unexpected FM operation")
	}
}

func TestLegacyFM_BuildersRequireAgentPath(t *testing.T) {
	var _ func(fixture.AgentPath) []byte = buildLegacyFMList
	var _ func(fixture.AgentPath) []byte = buildLegacyFMDownload
	var _ func(fixture.AgentPath, uint64) []byte = buildLegacyFMUpload

	root, err := fixture.NewAgentRoot(t.TempDir(), "fm-protocol")
	if err != nil {
		t.Fatal(err)
	}
	path, err := root.Path("wire/file.bin")
	if err != nil {
		t.Fatal(err)
	}

	if got, want := buildLegacyFMList(path), append([]byte{0x00}, []byte(path.String())...); !bytes.Equal(got, want) {
		t.Fatalf("list frame = %x, want %x", got, want)
	}
	if got, want := buildLegacyFMDownload(path), append([]byte{0x01}, []byte(path.String())...); !bytes.Equal(got, want) {
		t.Fatalf("download frame = %x, want %x", got, want)
	}
	wantUpload := append([]byte{0x02}, make([]byte, 8)...)
	binary.BigEndian.PutUint64(wantUpload[1:9], 0x0102030405060708)
	wantUpload = append(wantUpload, []byte(path.String())...)
	if got := buildLegacyFMUpload(path, 0x0102030405060708); !bytes.Equal(got, wantUpload) {
		t.Fatalf("upload frame = %x, want %x", got, wantUpload)
	}
}

func TestLegacyFM_RejectedPathDispatchesNoFrame(t *testing.T) {
	root, err := fixture.NewAgentRoot(t.TempDir(), "fm-path-boundary")
	if err != nil {
		t.Fatal(err)
	}
	symlinkTarget := t.TempDir()
	if err := os.Symlink(symlinkTarget, filepath.Join(root.Absolute(), "linked")); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name       string
		candidate  string
		wantReason fixture.PathRejectionReason
	}{
		{name: "absolute", candidate: filepath.Join(t.TempDir(), "outside"), wantReason: fixture.PathRejectionAbsolute},
		{name: "parent", candidate: "../outside", wantReason: fixture.PathRejectionParent},
		{name: "destructive root", candidate: ".", wantReason: fixture.PathRejectionDestructiveRoot},
		{name: "volume", candidate: `C:\outside`, wantReason: fixture.PathRejectionVolume},
		{name: "separator", candidate: `inside\outside`, wantReason: fixture.PathRejectionSeparator},
		{name: "symlink parent", candidate: "linked/file", wantReason: fixture.PathRejectionSymlinkParent},
	}
	operations := []struct {
		name string
		run  func(context.Context, legacyFMCommandDispatcher, string) error
	}{
		{name: "list", run: func(ctx context.Context, dispatcher legacyFMCommandDispatcher, candidate string) error {
			return dispatcher.list(ctx, candidate)
		}},
		{name: "upload", run: func(ctx context.Context, dispatcher legacyFMCommandDispatcher, candidate string) error {
			return dispatcher.upload(ctx, candidate, 1)
		}},
		{name: "download", run: func(ctx context.Context, dispatcher legacyFMCommandDispatcher, candidate string) error {
			return dispatcher.download(ctx, candidate)
		}},
	}
	for _, test := range tests {
		for _, operation := range operations {
			t.Run(test.name+"/"+operation.name, func(t *testing.T) {
				writer := &legacyFMRecordingWriter{}
				dispatcher := legacyFMCommandDispatcher{writer: writer, root: root}
				pathErr := operation.run(t.Context(), dispatcher, test.candidate)
				var agentPathErr *fixture.AgentPathError
				if !errors.As(pathErr, &agentPathErr) || agentPathErr.Reason != test.wantReason {
					t.Fatalf("rejected path error=%v, want reason %s", pathErr, test.wantReason)
				}
				if len(writer.frames) != 0 {
					t.Fatalf("rejected path dispatched %d frames", len(writer.frames))
				}
			})
		}
	}
}

func TestLegacyFM_OutsideRootSentinelUnchanged(t *testing.T) {
	parent := t.TempDir()
	root, err := fixture.NewAgentRoot(parent, "fm-sentinel")
	if err != nil {
		t.Fatal(err)
	}
	sentinelPath := filepath.Join(parent, "outside-sentinel")
	symlinkTargetDir := t.TempDir()
	symlinkTargetPath := filepath.Join(symlinkTargetDir, "target-sentinel")
	want := []byte("unchanged")
	for _, path := range []string{sentinelPath, symlinkTargetPath} {
		if err := os.WriteFile(path, want, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(symlinkTargetDir, filepath.Join(root.Absolute(), "linked")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root.Absolute(), "inside-dir"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root.Absolute(), "inside-download"), []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}

	writer := &legacyFMFilesystemWriter{}
	dispatcher := legacyFMCommandDispatcher{writer: writer, root: root}
	coverage := legacyFMSentinelCoverage{}
	coverage.ListRejected = dispatcher.list(t.Context(), "../outside-sentinel") != nil
	coverage.UploadRejected = dispatcher.upload(t.Context(), "linked/target-sentinel", uint64(len(want))) != nil
	coverage.DownloadRejected = dispatcher.download(t.Context(), sentinelPath) != nil
	if writer.frames != 0 {
		t.Fatalf("rejected sentinel paths dispatched %d frames", writer.frames)
	}
	for _, operation := range []func() error{
		func() error { return dispatcher.list(t.Context(), "inside-dir") },
		func() error { return dispatcher.upload(t.Context(), "inside-upload", 8) },
		func() error { return dispatcher.download(t.Context(), "inside-download") },
	} {
		if err := operation(); err != nil {
			t.Fatal(err)
		}
		coverage.SuccessCount++
	}
	if err := dispatcher.download(t.Context(), "inside-missing"); err != nil {
		coverage.ErrorCount++
	}
	if err := coverage.validate(); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{sentinelPath, symlinkTargetPath} {
		got, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("outside-root sentinel %s = %q, want %q", path, got, want)
		}
	}
}

func TestLegacyFM_ProducerObservationRejectsHardcodedZero(t *testing.T) {
	observation := legacyFMProducerObservation{
		RunID: "run-a", AgentUUID: "agent-a", SessionID: "session-a",
		Samples: []agent.FMProducerSample{{RunID: "run-a", AgentUUID: "agent-a", SessionID: "session-a", Phase: "closed", Active: 0}},
	}

	if err := observation.validate(); err == nil {
		t.Fatal("producer observation accepted zero-only samples without a live active producer")
	}
}

func TestLegacyFM_SentinelCoverageRejectsNoOp(t *testing.T) {
	if err := (legacyFMSentinelCoverage{}).validate(); err == nil {
		t.Fatal("sentinel coverage accepted a no-op test without rejected, successful, and failing FM operations")
	}
}

func TestLegacyFM_ProcessResidueRejectsFDAndDescendantDrift(t *testing.T) {
	tests := []struct {
		name string
		end  processharness.Sample
	}{
		{name: "fd drift", end: processharness.Sample{NonStdioFDCount: 6, DescendantCount: 2}},
		{name: "descendant drift", end: processharness.Sample{NonStdioFDCount: 5, DescendantCount: 3}},
	}
	baseline := processharness.Sample{NonStdioFDCount: 5, DescendantCount: 2}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := (legacyFMProcessResidue{Baseline: baseline, End: test.end}).validate(); err == nil {
				t.Fatal("process residue accepted FD or descendant drift")
			}
		})
	}
}
