package controller

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/nezhahq/nezha/model"
)

// GHSA-x6fg-52vr-hj4w: the admin settings endpoint must accept reserved_hosts
// so a reverse-proxy operator can declare the public dashboard hostnames the
// process itself never sees. Without binding here, the only way to set the
// field would be hand-editing the YAML, defeating the in-product guard.
func TestSettingForm_BindsReservedHosts(t *testing.T) {
	body := []byte(`{"site_name":"X","reserved_hosts":"panel.example.com, admin.example.com"}`)
	var sf model.SettingForm
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&sf); err != nil {
		t.Fatal(err)
	}
	if sf.ReservedHosts != "panel.example.com, admin.example.com" {
		t.Fatalf("reserved_hosts must decode into SettingForm, got %q", sf.ReservedHosts)
	}
}
