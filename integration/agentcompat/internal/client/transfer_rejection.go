package client

import (
	"context"
	"fmt"
	"io"
	"net/http"
)

type OversizeUploadProbe struct {
	Body          io.Reader
	ContentLength int64
}

func (client *Client) ProbeOversizeUpload(ctx context.Context, transfer TransferURL, probe OversizeUploadProbe) error {
	validated, err := client.validateTransferURL(transfer, http.MethodPost)
	if err != nil {
		return err
	}
	if probe.Body == nil || probe.ContentLength != client.maxTransferBytes+1 {
		return fmt.Errorf("oversize upload probe: %w", ErrInvalidConfig)
	}
	requestContext, cancel := client.transferContext(ctx)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodPost, validated.URL, io.LimitReader(probe.Body, probe.ContentLength))
	if err != nil {
		return errorsNewRedacted("create oversize transfer probe", err)
	}
	request.ContentLength = probe.ContentLength
	response, err := client.transferHTTPClient().Do(request)
	if err != nil {
		if requestContext.Err() != nil {
			return fmt.Errorf("oversize transfer probe: %w", requestContext.Err())
		}
		return errorsNewRedacted("oversize transfer probe", err)
	}
	defer response.Body.Close()
	body, err := readBounded(response.Body, client.maxResponseBytes)
	if err != nil {
		return err
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return fmt.Errorf("oversize transfer probe unexpectedly succeeded: %w", ErrSemanticFailure)
	}
	return &HTTPError{StatusCode: response.StatusCode, Message: Redact(string(body))}
}
