//go:build linux

package scenario

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/fixture"
)

func verifyMCPFilesystemPathGuards(ctx context.Context, assertions *AssertionSet, filesystem mcpFilesystemClient, fixtureParent string) error {
	directTarget := filepath.Join(fixtureParent, "outside-sentinel")
	symlinkParentTarget := filepath.Join(fixtureParent, "outside-directory", "parent-target.txt")
	symlinkFinalTarget := filepath.Join(fixtureParent, "final-target.txt")
	if err := os.Mkdir(filepath.Dir(symlinkParentTarget), 0o700); err != nil {
		return err
	}
	for _, target := range []string{directTarget, symlinkParentTarget, symlinkFinalTarget} {
		if err := os.WriteFile(target, []byte("unchanged:"+filepath.Base(target)), 0o600); err != nil {
			return err
		}
	}
	if err := os.Symlink(filepath.Dir(symlinkParentTarget), filepath.Join(filesystem.root.Absolute(), "linked-parent")); err != nil {
		return err
	}
	if err := os.Symlink(symlinkFinalTarget, filepath.Join(filesystem.root.Absolute(), "linked-final")); err != nil {
		return err
	}
	before, err := filesystemTargetHashes(directTarget, symlinkParentTarget, symlinkFinalTarget)
	if err != nil {
		return err
	}

	requestsBefore := filesystem.client.RequestCount()
	rejections := []struct {
		want fixture.PathRejectionReason
		run  func() error
	}{
		{fixture.PathRejectionAbsolute, func() error { _, err := filesystem.read(ctx, directTarget, 0, 1, "utf8"); return err }},
		{fixture.PathRejectionParent, func() error {
			_, err := filesystem.write(ctx, mcpFilesystemWrite{relative: "../outside-sentinel", content: "changed", encoding: "utf8", mode: "0600"})
			return err
		}},
		{fixture.PathRejectionVolume, func() error { _, err := filesystem.list(ctx, `C:\outside.txt`, false); return err }},
		{fixture.PathRejectionSeparator, func() error { _, err := filesystem.read(ctx, `inside\outside.txt`, 0, 1, "utf8"); return err }},
		{fixture.PathRejectionDestructiveRoot, func() error { _, err := filesystem.delete(ctx, ".", true); return err }},
		{fixture.PathRejectionSymlinkParent, func() error {
			_, err := filesystem.write(ctx, mcpFilesystemWrite{relative: "linked-parent/parent-target.txt", content: "changed", encoding: "utf8", mode: "0600"})
			return err
		}},
		{fixture.PathRejectionSymlinkFinal, func() error { _, err := filesystem.delete(ctx, "linked-final", false); return err }},
	}
	matched := 0
	for _, rejection := range rejections {
		var pathError *fixture.AgentPathError
		if errors.As(rejection.run(), &pathError) && pathError.Reason == rejection.want {
			matched++
		}
	}
	requestsAfter := filesystem.client.RequestCount()
	dispatched := int64(requestsAfter) - int64(requestsBefore)
	after, err := filesystemTargetHashes(directTarget, symlinkParentTarget, symlinkFinalTarget)
	if err != nil {
		return err
	}
	if err := errors.Join(
		os.Remove(filepath.Join(filesystem.root.Absolute(), "linked-parent")),
		os.Remove(filepath.Join(filesystem.root.Absolute(), "linked-final")),
	); err != nil {
		return err
	}
	assertions.Record("fixture path rejections dispatch zero MCP HTTP requests", matched == len(rejections) && dispatched == 0, fmt.Sprintf("path_rejections_dispatched: %d; matched=%d total=%d requests_before=%d requests_after=%d", dispatched, matched, len(rejections), requestsBefore, requestsAfter))
	assertions.Record("fixture path rejections leave outside and symlink targets unchanged", before == after, fmt.Sprintf("before=%x after=%x", before, after))
	return nil
}

func filesystemTargetHashes(paths ...string) ([32]byte, error) {
	hash := sha256.New()
	for _, path := range paths {
		content, err := os.ReadFile(path)
		if err != nil {
			return [32]byte{}, err
		}
		if _, err := hash.Write(content); err != nil {
			return [32]byte{}, err
		}
	}
	var digest [32]byte
	copy(digest[:], hash.Sum(nil))
	return digest, nil
}
