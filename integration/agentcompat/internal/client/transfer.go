package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type TransferURL struct {
	URL       string    `json:"url"`
	Method    string    `json:"method"`
	ExpiresAt time.Time `json:"expires_at"`
}

type DownloadURLRequest struct {
	ServerID   uint64 `json:"server_id"`
	Path       string `json:"path"`
	TTLSeconds int    `json:"ttl_seconds,omitempty"`
}

type UploadURLRequest struct {
	ServerID      uint64 `json:"server_id"`
	Path          string `json:"path"`
	TTLSeconds    int    `json:"ttl_seconds,omitempty"`
	Mode          string `json:"mode,omitempty"`
	CreateDirs    bool   `json:"create_dirs,omitempty"`
	IfMatchSHA256 string `json:"if_match_sha256,omitempty"`
}

type UploadTransfer struct {
	Body          io.Reader
	ContentLength int64
	SHA256        string
}

type UploadResult struct {
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type uploadResultPayload struct {
	Size   *int64  `json:"size"`
	SHA256 *string `json:"sha256"`
}

func RequestDownloadURL(ctx context.Context, client *Client, request DownloadURLRequest) (TransferURL, error) {
	result, err := CallTool[DownloadURLRequest, TransferURL](ctx, client, ToolCall[DownloadURLRequest]{Name: "fs.download_url", Arguments: request})
	if err != nil {
		return TransferURL{}, err
	}
	return client.validateTransferURL(result.StructuredContent, http.MethodGet)
}

func RequestUploadURL(ctx context.Context, client *Client, request UploadURLRequest) (TransferURL, error) {
	result, err := CallTool[UploadURLRequest, TransferURL](ctx, client, ToolCall[UploadURLRequest]{Name: "fs.upload_url", Arguments: request})
	if err != nil {
		return TransferURL{}, err
	}
	return client.validateTransferURL(result.StructuredContent, http.MethodPost)
}

func (client *Client) DownloadTransfer(ctx context.Context, transfer TransferURL, destination io.Writer) (int64, error) {
	validated, err := client.validateTransferURL(transfer, http.MethodGet)
	if err != nil {
		return 0, err
	}
	requestContext, cancel := client.transferContext(ctx)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodGet, validated.URL, nil)
	if err != nil {
		return 0, errorsNewRedacted("create transfer request", err)
	}
	response, err := client.transferHTTPClient().Do(request)
	if err != nil {
		if requestContext.Err() != nil {
			return 0, fmt.Errorf("download transfer: %w", requestContext.Err())
		}
		return 0, errorsNewRedacted("download transfer", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return 0, &HTTPError{StatusCode: response.StatusCode}
	}
	written, err := io.Copy(destination, io.LimitReader(response.Body, client.maxTransferBytes))
	if err != nil {
		return written, fmt.Errorf("copy download transfer: %w", err)
	}
	var overflow [1]byte
	read, err := response.Body.Read(overflow[:])
	if err != nil && err != io.EOF {
		return written, fmt.Errorf("probe download transfer size: %w", err)
	}
	if read > 0 {
		return written, ErrTransferTooLarge
	}
	return written, nil
}

func (client *Client) UploadTransfer(ctx context.Context, transfer TransferURL, upload UploadTransfer) (UploadResult, error) {
	validated, err := client.validateTransferURL(transfer, http.MethodPost)
	if err != nil {
		return UploadResult{}, err
	}
	if upload.Body == nil || upload.ContentLength <= 0 {
		return UploadResult{}, fmt.Errorf("upload content length: %w", ErrInvalidConfig)
	}
	if upload.ContentLength > client.maxTransferBytes {
		return UploadResult{}, ErrTransferTooLarge
	}
	transferURL, err := url.Parse(validated.URL)
	if err != nil {
		return UploadResult{}, errorsNewRedacted("parse transfer URL", err)
	}
	if upload.SHA256 != "" {
		query := transferURL.Query()
		query.Set("sha256", upload.SHA256)
		transferURL.RawQuery = query.Encode()
	}
	requestContext, cancel := client.transferContext(ctx)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodPost, transferURL.String(), io.LimitReader(upload.Body, upload.ContentLength))
	if err != nil {
		return UploadResult{}, errorsNewRedacted("create transfer request", err)
	}
	request.ContentLength = upload.ContentLength
	response, err := client.transferHTTPClient().Do(request)
	if err != nil {
		if requestContext.Err() != nil {
			return UploadResult{}, fmt.Errorf("upload transfer: %w", requestContext.Err())
		}
		return UploadResult{}, errorsNewRedacted("upload transfer", err)
	}
	defer response.Body.Close()
	body, err := readBounded(response.Body, client.maxResponseBytes)
	if err != nil {
		return UploadResult{}, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return UploadResult{}, &HTTPError{StatusCode: response.StatusCode, Message: Redact(string(body))}
	}
	var payload uploadResultPayload
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&payload); err != nil {
		return UploadResult{}, fmt.Errorf("decode upload result: %w", err)
	}
	if payload.Size == nil || payload.SHA256 == nil || *payload.Size <= 0 || *payload.SHA256 == "" {
		return UploadResult{}, fmt.Errorf("decode upload result: %w", ErrSemanticFailure)
	}
	return UploadResult{Size: *payload.Size, SHA256: *payload.SHA256}, nil
}

func (client *Client) transferHTTPClient() *http.Client {
	clone := *client.httpClient
	clone.Jar = nil
	clone.CheckRedirect = rejectRedirect
	return &clone
}

func (client *Client) validateTransferURL(transfer TransferURL, expectedMethod string) (TransferURL, error) {
	parsed, err := url.Parse(transfer.URL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return TransferURL{}, fmt.Errorf("transfer URL: %w", ErrInvalidConfig)
	}
	if parsed.User != nil || !client.sameOrigin(parsed) {
		return TransferURL{}, fmt.Errorf("transfer origin: %w", ErrInvalidConfig)
	}
	if transfer.ExpiresAt.IsZero() || !time.Now().Before(transfer.ExpiresAt) {
		return TransferURL{}, ErrTransferExpired
	}
	if !strings.EqualFold(transfer.Method, expectedMethod) {
		return TransferURL{}, fmt.Errorf("transfer method: %w", ErrInvalidConfig)
	}
	transfer.Method = expectedMethod
	return transfer, nil
}

func (client *Client) sameOrigin(candidate *url.URL) bool {
	return strings.EqualFold(candidate.Scheme, client.baseURL.Scheme) && strings.EqualFold(candidate.Host, client.baseURL.Host)
}
