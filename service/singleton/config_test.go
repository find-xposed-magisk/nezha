package singleton

import (
	"os"
	"strings"
	"testing"

	"github.com/nezhahq/nezha/model"
)

func TestInitConfigFromPathRotatesJWTSecretKey(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "nezha-config-*.yaml")
	if err != nil {
		t.Fatalf("create temp config: %v", err)
	}
	if _, err := file.WriteString("jwt_secret_key: leaked-secret\nagent_secret_key: agent-secret\njwt_secret_key_last_rotated_version: v2.0.12\n"); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close temp config: %v", err)
	}

	originalConf := Conf
	originalVersion := Version
	originalTemplates := FrontendTemplates
	Version = "v2.0.13"
	FrontendTemplates = nil
	t.Cleanup(func() {
		Conf = originalConf
		Version = originalVersion
		FrontendTemplates = originalTemplates
	})

	if err := InitConfigFromPath(file.Name()); err != nil {
		t.Fatalf("init config: %v", err)
	}
	if Conf.JWTSecretKey == "leaked-secret" {
		t.Fatal("jwt_secret_key was not rotated")
	}
	if Conf.JWTSecretKeyLastRotatedVersion != model.JWTSecretKeyRotationBaselineVersion {
		t.Fatalf("jwt secret key marker = %q, want %q", Conf.JWTSecretKeyLastRotatedVersion, model.JWTSecretKeyRotationBaselineVersion)
	}

	saved, err := os.ReadFile(file.Name())
	if err != nil {
		t.Fatalf("read saved config: %v", err)
	}
	if strings.Contains(string(saved), "leaked-secret") {
		t.Fatalf("saved config still contains leaked jwt_secret_key: %s", saved)
	}
	if !strings.Contains(string(saved), "jwt_secret_key_last_rotated_version: v2.0.13") {
		t.Fatalf("saved config did not persist jwt secret key marker: %s", saved)
	}
}
