package model

import (
	"encoding/json"
	"testing"
)

// RoleAdmin is the zero value (0). The Role field must NOT use json:",omitempty"
// or an admin profile would serialize without a `role` key, and the frontend
// (which gates the admin menu on `role === 0`) would treat the admin as a
// regular user. Guard against a regression that drops the field for admins.
func TestUserRoleSerializedForAdmin(t *testing.T) {
	u := User{Common: Common{ID: 1}, Username: "admin", Role: RoleAdmin}

	b, err := json.Marshal(u)
	if err != nil {
		t.Fatalf("marshal user: %v", err)
	}

	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("unmarshal user: %v", err)
	}

	raw, ok := decoded["role"]
	if !ok {
		t.Fatalf("admin user JSON must include the `role` field, got: %s", b)
	}
	if string(raw) != "0" {
		t.Fatalf("admin user `role` must serialize as 0, got: %s", raw)
	}
}
