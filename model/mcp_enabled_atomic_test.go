package model

import "testing"

func TestSetMCPEnabledUsesAtomicAsSourceOfTruth(t *testing.T) {
	c := &Config{}
	if c.MCPEnabled() {
		t.Fatal("zero-value Config must report MCP disabled")
	}
	c.SetMCPEnabled(true)
	if !c.MCPEnabled() {
		t.Fatal("MCPEnabled() must observe SetMCPEnabled(true)")
	}
	c.SetMCPEnabled(false)
	if c.MCPEnabled() {
		t.Fatal("MCPEnabled() must observe SetMCPEnabled(false)")
	}
}

// save() 在 marshal 前从 atomic 同步明文 EnableMCP 字段，因此持久化/JSON 仍拿到
// 正确值；运行时 SetMCPEnabled 不直接写该字段以避免与 listConfig 的整体拷贝竞争。
func TestSaveSyncsEnableMCPFieldFromAtomic(t *testing.T) {
	c := &Config{}
	c.filePath = t.TempDir() + "/config.yaml"
	c.SetMCPEnabled(true)
	if err := c.save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	if !c.EnableMCP {
		t.Fatal("save() must sync EnableMCP field from the atomic mirror for persistence")
	}
	c.SetMCPEnabled(false)
	if err := c.save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	if c.EnableMCP {
		t.Fatal("save() must clear EnableMCP field when the atomic mirror is false")
	}
}
