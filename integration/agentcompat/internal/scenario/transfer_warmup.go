//go:build linux

package scenario

import (
	"context"
	"errors"
	"time"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/client"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/contract"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/fixture"
)

const transferWarmupBytes = 64 * 1024

type transferWarmupEvidence struct {
	uploadBytes   uint64
	downloadBytes uint64
	sha256        string
	duration      time.Duration
	deadline      time.Duration
}

func (execution transferExecution) runWarmup(ctx context.Context) (transferWarmupEvidence, error) {
	payload, err := fixture.NewPayload(contract.DefaultSeed, transferWarmupBytes)
	if err != nil {
		return transferWarmupEvidence{}, err
	}
	digest, err := fixture.VerifyPayload(payload.Reader(), transferWarmupBytes)
	if err != nil {
		return transferWarmupEvidence{}, err
	}
	path, err := execution.root.Path("warmup/nested/payload.bin")
	if err != nil {
		return transferWarmupEvidence{}, err
	}
	uploadURL, err := client.RequestUploadURL(ctx, execution.client, client.UploadURLRequest{
		ServerID: execution.serverID, Path: path.String(), TTLSeconds: 60, Mode: "0600", CreateDirs: true,
	})
	if err != nil {
		return transferWarmupEvidence{}, err
	}
	uploadClient, err := clientForTransferURL(uploadURL)
	if err != nil {
		return transferWarmupEvidence{}, err
	}
	defer uploadClient.Close()
	started := time.Now()
	uploadResult, err := uploadClient.UploadTransfer(ctx, uploadURL, client.UploadTransfer{
		Body: payload.Reader(), ContentLength: transferWarmupBytes, SHA256: digest.Hex(),
	})
	if err != nil {
		return transferWarmupEvidence{}, err
	}
	if uploadResult.Size != transferWarmupBytes || uploadResult.SHA256 != digest.Hex() {
		return transferWarmupEvidence{}, errors.New("warm-up upload evidence mismatch")
	}
	downloadURL, err := client.RequestDownloadURL(ctx, execution.client, client.DownloadURLRequest{
		ServerID: execution.serverID, Path: path.String(), TTLSeconds: 60,
	})
	if err != nil {
		return transferWarmupEvidence{}, err
	}
	downloadClient, err := clientForTransferURL(downloadURL)
	if err != nil {
		return transferWarmupEvidence{}, err
	}
	defer downloadClient.Close()
	measured := fixture.NewMeasuredWriter()
	written, err := downloadClient.DownloadTransfer(ctx, downloadURL, measured)
	if err != nil {
		return transferWarmupEvidence{}, err
	}
	measurement := measured.Measurement()
	if written != transferWarmupBytes || measurement.Digest.Bytes != transferWarmupBytes || measurement.Digest.Hex() != digest.Hex() {
		return transferWarmupEvidence{}, errors.New("warm-up download evidence mismatch")
	}
	return transferWarmupEvidence{
		uploadBytes: uint64(uploadResult.Size), downloadBytes: measurement.Digest.Bytes,
		sha256: digest.Hex(), duration: time.Since(started),
	}, nil
}

func (evidence transferWarmupEvidence) valid() bool {
	return evidence.uploadBytes == transferWarmupBytes && evidence.downloadBytes == transferWarmupBytes && evidence.sha256 != "" && evidence.duration > 0 && evidence.deadline > 0
}
