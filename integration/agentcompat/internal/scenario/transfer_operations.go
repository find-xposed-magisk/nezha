//go:build linux

package scenario

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/client"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/contract"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/fixture"
)

func (execution transferExecution) upload(ctx context.Context, path fixture.AgentPath, payload fixture.Payload, digest fixture.PayloadDigest, fault contract.Fault) (transferPathEvidence, client.TransferURL, error) {
	transferURL, err := client.RequestUploadURL(ctx, execution.client, client.UploadURLRequest{
		ServerID: execution.serverID, Path: path.String(), TTLSeconds: 60, Mode: "0640", CreateDirs: true,
	})
	if err != nil {
		return transferPathEvidence{}, client.TransferURL{}, err
	}
	transferClient, err := clientForTransferURL(transferURL)
	if err != nil {
		return transferPathEvidence{}, client.TransferURL{}, err
	}
	defer transferClient.Close()
	measured := fixture.NewMeasuredReader(payload.Reader())
	expectedSHA := digest.Hex()
	if fault.String() == "transfer-hash" {
		expectedSHA = strings.Repeat("0", 64)
	}
	started := time.Now()
	result, err := transferClient.UploadTransfer(ctx, transferURL, client.UploadTransfer{
		Body: measured, ContentLength: int64(contract.TransferBytes), SHA256: expectedSHA,
	})
	duration := time.Since(started)
	measurement := measured.Measurement()
	if err != nil {
		return transferPathEvidence{bytes: measurement.Digest.Bytes, sha256: measurement.Digest.Hex(), chunks: measurement.Chunks, duration: duration}, transferURL, err
	}
	fileDigest, info, err := verifyUploadedTransfer(path)
	if err != nil {
		return transferPathEvidence{}, transferURL, err
	}
	if result.Size != info.Size() || result.SHA256 != fileDigest.Hex() {
		return transferPathEvidence{}, transferURL, errors.New("upload result differs from Agent file")
	}
	return transferPathEvidence{bytes: uint64(result.Size), sha256: result.SHA256, chunks: measurement.Chunks, duration: duration, mode: info.Mode()}, transferURL, nil
}

func verifyUploadedTransfer(path fixture.AgentPath) (digest fixture.PayloadDigest, info os.FileInfo, err error) {
	file, err := os.Open(path.String())
	if err != nil {
		return fixture.PayloadDigest{}, nil, fmt.Errorf("open uploaded transfer: %w", err)
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	digest, err = fixture.VerifyPayload(file, contract.TransferBytes)
	if err != nil {
		return fixture.PayloadDigest{}, nil, err
	}
	info, err = file.Stat()
	if err != nil {
		return fixture.PayloadDigest{}, nil, fmt.Errorf("stat uploaded transfer: %w", err)
	}
	return digest, info, nil
}

func (execution transferExecution) download(ctx context.Context, path fixture.AgentPath) (transferPathEvidence, client.TransferURL, error) {
	transferURL, err := client.RequestDownloadURL(ctx, execution.client, client.DownloadURLRequest{
		ServerID: execution.serverID, Path: path.String(), TTLSeconds: 60,
	})
	if err != nil {
		return transferPathEvidence{}, client.TransferURL{}, err
	}
	transferClient, err := clientForTransferURL(transferURL)
	if err != nil {
		return transferPathEvidence{}, client.TransferURL{}, err
	}
	defer transferClient.Close()
	measured := fixture.NewMeasuredWriter()
	started := time.Now()
	written, err := transferClient.DownloadTransfer(ctx, transferURL, measured)
	duration := time.Since(started)
	measurement := measured.Measurement()
	if err != nil {
		return transferPathEvidence{}, transferURL, err
	}
	if written != int64(measurement.Digest.Bytes) {
		return transferPathEvidence{}, transferURL, errors.New("download byte count differs from measured digest")
	}
	return transferPathEvidence{bytes: measurement.Digest.Bytes, sha256: measurement.Digest.Hex(), chunks: measurement.Chunks, duration: duration}, transferURL, nil
}

func (execution transferExecution) replayUpload(ctx context.Context, transferURL client.TransferURL) error {
	transferClient, err := clientForTransferURL(transferURL)
	if err != nil {
		return err
	}
	defer transferClient.Close()
	payload, err := fixture.NewPayload(contract.DefaultSeed, 1)
	if err != nil {
		return err
	}
	digest, err := fixture.VerifyPayload(payload.Reader(), 1)
	if err != nil {
		return err
	}
	_, err = transferClient.UploadTransfer(ctx, transferURL, client.UploadTransfer{Body: payload.Reader(), ContentLength: 1, SHA256: digest.Hex()})
	return err
}

func (execution transferExecution) replayDownload(ctx context.Context, transferURL client.TransferURL) error {
	transferClient, err := clientForTransferURL(transferURL)
	if err != nil {
		return err
	}
	defer transferClient.Close()
	_, err = transferClient.DownloadTransfer(ctx, transferURL, io.Discard)
	return err
}

func (execution transferExecution) probeOversize(ctx context.Context) error {
	path, err := execution.root.Path("oversize/rejected.bin")
	if err != nil {
		return err
	}
	transferURL, err := client.RequestUploadURL(ctx, execution.client, client.UploadURLRequest{
		ServerID: execution.serverID, Path: path.String(), TTLSeconds: 60, CreateDirs: true,
	})
	if err != nil {
		return err
	}
	transferClient, err := clientForTransferURL(transferURL)
	if err != nil {
		return err
	}
	defer transferClient.Close()
	return transferClient.ProbeOversizeUpload(ctx, transferURL, client.OversizeUploadProbe{
		Body: zeroReader{}, ContentLength: int64(contract.TransferBytes) + 1,
	})
}

type ownedTransferClient struct {
	*client.Client
	transport *http.Transport
}

func clientForTransferURL(transferURL client.TransferURL) (*ownedTransferClient, error) {
	parsed, err := url.Parse(transferURL.URL)
	if err != nil {
		return nil, fmt.Errorf("parse transfer URL origin: %w", err)
	}
	defaultTransport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, errors.New("default HTTP transport is not cloneable")
	}
	transport := defaultTransport.Clone()
	transferClient, err := client.New(client.Config{
		BaseURL: parsed.Scheme + "://" + parsed.Host, HTTPClient: &http.Client{Transport: transport}, TransferTimeout: 5 * time.Minute,
	})
	if err != nil {
		transport.CloseIdleConnections()
		return nil, err
	}
	return &ownedTransferClient{Client: transferClient, transport: transport}, nil
}

func (client *ownedTransferClient) Close() {
	client.transport.CloseIdleConnections()
}

type zeroReader struct{}

func (zeroReader) Read(destination []byte) (int, error) {
	clear(destination)
	return len(destination), nil
}
