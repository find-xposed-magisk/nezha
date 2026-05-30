package controller

import (
	"context"
	"strings"
	"testing"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/service/singleton"
)

func TestValidateTransferPathRejectsOversizedPath(t *testing.T) {
	if err := validateTransferPath(strings.Repeat("a", maxTransferPathLen+1)); err == nil {
		t.Fatal("path longer than maxTransferPathLen must be rejected to bound transferEntry memory")
	}
	if err := validateTransferPath(""); err == nil {
		t.Fatal("empty path must be rejected")
	}
	if err := validateTransferPath("/etc/hostname"); err != nil {
		t.Fatalf("a normal path must be accepted, got %v", err)
	}
}

// openFsTransferStream must refuse to start a new transfer (and never reach
// SendTask) once the administrator has disabled MCP, closing the race window
// between revalidateTransferEntry and stream creation.
func TestOpenFsTransferStreamRefusesWhenMCPDisabled(t *testing.T) {
	originalConf := singleton.Conf
	t.Cleanup(func() { singleton.Conf = originalConf })
	cfg := &model.Config{}
	cfg.SetMCPEnabled(false)
	singleton.Conf = &singleton.ConfigClass{Config: cfg}

	_, _, err := openFsTransferStream(context.Background(), 1, &model.FsTransferRequest{})
	if err == nil {
		t.Fatal("transfer stream must not open while MCP is disabled")
	}
}
