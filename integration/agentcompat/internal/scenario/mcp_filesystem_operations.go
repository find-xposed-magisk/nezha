//go:build linux

package scenario

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/client"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/dashboard"
)

func runMCPFilesystemOperations(ctx context.Context, assertions *AssertionSet, filesystem mcpFilesystemClient, permission permissionAgentContract, dashboardInstance *dashboard.Dashboard) error {
	text := "typed filesystem payload"
	textHash := sha256.Sum256([]byte(text))
	textDigest := hex.EncodeToString(textHash[:])
	written, err := filesystem.write(ctx, mcpFilesystemWrite{relative: "tree/text.txt", content: text, encoding: "utf8", mode: "0640", createDirs: true})
	assertions.Record("fs.write create_dirs size and SHA", err == nil && written.StructuredContent.Size == int64(len(text)) && written.StructuredContent.SHA256 == textDigest, errorText(err))
	if err != nil {
		return err
	}
	tree, err := filesystem.list(ctx, "tree", false)
	assertions.Record("fs.write applies exact file mode", err == nil && len(tree.StructuredContent.Entries) == 1 && tree.StructuredContent.Entries[0].Name == "text.txt" && tree.StructuredContent.Entries[0].Type == "file" && tree.StructuredContent.Entries[0].Size == int64(len(text)) && tree.StructuredContent.Entries[0].Mode == "0640", errorText(err))
	_, err = filesystem.write(ctx, mcpFilesystemWrite{relative: ".hidden", content: "hidden", encoding: "utf8", mode: "0600"})
	if err != nil {
		return err
	}
	visible, err := filesystem.list(ctx, ".", false)
	assertions.Record("fs.list hides dot entries with exact totals", err == nil && visible.StructuredContent.Total == 1 && entryNames(visible.StructuredContent.Entries) == "tree", errorText(err))
	hidden, err := filesystem.list(ctx, ".", true)
	assertions.Record("fs.list show_hidden includes exact dot entry", err == nil && hidden.StructuredContent.Total == 2 && entryNames(hidden.StructuredContent.Entries) == ".hidden,tree", errorText(err))
	// Inventory and setup consume the PAT's fixed MCP call budget; rotate before content and CAS checks.
	filesystem, err = refreshedMCPFilesystemClient(ctx, dashboardInstance, filesystem)
	if err != nil {
		return err
	}
	readText, err := filesystem.read(ctx, "tree/text.txt", 0, int64(len(text)), "utf8")
	assertions.Record("fs.read utf8 exact content size SHA and truncation", err == nil && readText.StructuredContent.Content == text && readText.StructuredContent.Encoding == "utf8" && readText.StructuredContent.Size == int64(len(text)) && readText.StructuredContent.SHA256 == textDigest && !readText.StructuredContent.Truncated, errorText(err))
	binary := []byte{0x00, 0x41, 0xff, 0x42}
	binaryHash := sha256.Sum256(binary)
	binaryDigest := hex.EncodeToString(binaryHash[:])
	_, err = filesystem.write(ctx, mcpFilesystemWrite{relative: "tree/binary.bin", content: base64.StdEncoding.EncodeToString(binary), encoding: "base64", mode: "0600"})
	if err != nil {
		return err
	}
	readBinary, err := filesystem.read(ctx, "tree/binary.bin", 0, int64(len(binary)), "base64")
	assertions.Record("fs.read base64 exact content size and SHA", err == nil && readBinary.StructuredContent.Content == base64.StdEncoding.EncodeToString(binary) && readBinary.StructuredContent.Encoding == "base64" && readBinary.StructuredContent.Size == int64(len(binary)) && readBinary.StructuredContent.SHA256 == binaryDigest, errorText(err))
	updated := "CAS updated payload"
	updatedHash := sha256.Sum256([]byte(updated))
	updatedDigest := hex.EncodeToString(updatedHash[:])
	cas, err := filesystem.write(ctx, mcpFilesystemWrite{relative: "tree/text.txt", content: updated, encoding: "utf8", mode: "0640", ifMatchSHA256: textDigest})
	assertions.Record("fs.write CAS success exact size and SHA", err == nil && cas.StructuredContent.Size == int64(len(updated)) && cas.StructuredContent.SHA256 == updatedDigest, errorText(err))
	_, casErr := filesystem.write(ctx, mcpFilesystemWrite{relative: "tree/text.txt", content: "must-not-land", encoding: "utf8", mode: "0640", ifMatchSHA256: textDigest})
	unchanged, readErr := filesystem.read(ctx, "tree/text.txt", 0, int64(len(updated)), "utf8")
	assertions.Record("fs.write CAS mismatch is typed and leaves content unchanged", toolFailureContains(casErr, "if_match precondition failed") && readErr == nil && unchanged.StructuredContent.Content == updated && unchanged.StructuredContent.SHA256 == updatedDigest, errorText(errors.Join(casErr, readErr)))
	permissionDirectory, pathErr := filesystem.root.Path("permission-denied")
	if pathErr != nil {
		return pathErr
	}
	if err := os.Mkdir(permissionDirectory.String(), 0o500); err != nil {
		return err
	}
	permissionResponse, permissionErr := permission.filesystem.write(ctx, mcpFilesystemWrite{relative: "permission-denied/file.txt", content: "denied", encoding: "utf8", mode: "0600"})
	permissionDeniedObserved := permissionErr == nil && permissionResponse.StructuredContent.Error == "permission denied"
	assertions.Record("fs.write Agent filesystem permission denial is typed", permission.processContract && permissionDeniedObserved, fmt.Sprintf("%s; uid=%d gid=%d process_contract=%t", permissionResponse.StructuredContent.Error, permission.uid, permission.gid, permission.processContract))
	if err := os.Remove(permissionDirectory.String()); err != nil {
		return err
	}
	filesystem, err = refreshedMCPFilesystemClient(ctx, dashboardInstance, filesystem)
	if err != nil {
		return err
	}
	_, missingErr := filesystem.read(ctx, "tree/missing.txt", 0, 1, "utf8")
	assertions.Record("fs.read nonexistent path is typed", toolFailureContains(missingErr, "does not exist"), errorText(missingErr))
	_, encodingErr := filesystem.read(ctx, "tree/text.txt", 0, 1, "rot13")
	assertions.Record("fs.read invalid encoding is typed", toolFailureContains(encodingErr, "unknown encoding"), errorText(encodingErr))
	_, writeEncodingErr := filesystem.write(ctx, mcpFilesystemWrite{relative: "tree/invalid-encoding.txt", content: "x", encoding: "rot13", mode: "0600"})
	assertions.Record("fs.write invalid encoding is typed", toolFailureContains(writeEncodingErr, "unknown encoding"), errorText(writeEncodingErr))
	_, modeErr := filesystem.write(ctx, mcpFilesystemWrite{relative: "tree/invalid-mode.txt", content: "x", encoding: "utf8", mode: "invalid"})
	assertions.Record("fs.write invalid mode is typed", toolFailureContains(modeErr, "invalid mode"), errorText(modeErr))
	filesystem, err = refreshedMCPFilesystemClient(ctx, dashboardInstance, filesystem)
	if err != nil {
		return err
	}
	oversize, oversizeErr := client.DoREST[agentcompatFsWriteContractRequest, agentcompatFsWriteContractResponse](ctx, filesystem.client, client.RESTRequest[agentcompatFsWriteContractRequest]{Method: http.MethodPost, Path: "/agentcompat/fs-write-contract", Body: &agentcompatFsWriteContractRequest{ServerID: filesystem.serverID, Operation: agentcompatFsWriteOperationOversize}})
	oversizeHandlerObserved := oversize.AgentRPCResponse
	productionMaxWriteCheck := oversizeErr == nil && oversize.Result.Error == "content exceeds max write size"
	assertions.Record("fs.write Agent oversize contract is typed", oversizeHandlerObserved && productionMaxWriteCheck, fmt.Sprintf("%s; agent_handler=%t production_max_write_check=%t; agent_rpc_response=%t", oversize.Result.Error, oversizeHandlerObserved, productionMaxWriteCheck, oversize.AgentRPCResponse))
	_, nonrecursiveErr := filesystem.delete(ctx, "tree", false)
	assertions.Record("fs.delete nonrecursive rejects nonempty directory", toolFailureContains(nonrecursiveErr, "internal agent error"), errorText(nonrecursiveErr))
	deleted, err := filesystem.delete(ctx, "tree", true)
	assertions.Record("fs.delete recursive returns exact positive count", err == nil && deleted.StructuredContent.DeletedCount == 3, errorText(err))
	final, err := filesystem.list(ctx, ".", true)
	assertions.Record("fs.list final absence after recursive delete", err == nil && final.StructuredContent.Total == 1 && entryNames(final.StructuredContent.Entries) == ".hidden", errorText(err))
	_, err = filesystem.delete(ctx, ".hidden", false)
	if err != nil {
		return err
	}
	empty, err := filesystem.list(ctx, ".", true)
	assertions.Record("fs.list final fixture root is empty", err == nil && empty.StructuredContent.Total == 0 && len(empty.StructuredContent.Entries) == 0, errorText(err))
	return err
}

type agentcompatFsWriteOperation string

const (
	agentcompatFsWriteOperationOversize agentcompatFsWriteOperation = "oversize"
)

type agentcompatFsWriteContractRequest struct {
	ServerID  uint64                      `json:"server_id"`
	Operation agentcompatFsWriteOperation `json:"operation"`
}

type agentcompatFsWriteContractResponse struct {
	Result           client.FsWriteResult `json:"result"`
	AgentRPCResponse bool                 `json:"agent_rpc_response"`
}

func refreshedMCPFilesystemClient(ctx context.Context, dashboardInstance *dashboard.Dashboard, filesystem mcpFilesystemClient) (mcpFilesystemClient, error) {
	mcpClient, err := createScopedClient(ctx, dashboardInstance, []string{"nezha:*"})
	if err != nil {
		return mcpFilesystemClient{}, err
	}
	return newMCPFilesystemClient(mcpClient, filesystem.serverID, filesystem.root), nil
}

func entryNames(entries []client.FsEntry) string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name)
	}
	slices.Sort(names)
	return strings.Join(names, ",")
}

func toolFailureContains(err error, text string) bool {
	var failure *client.ToolFailure
	return errors.As(err, &failure) && strings.Contains(failure.Message, text)
}
