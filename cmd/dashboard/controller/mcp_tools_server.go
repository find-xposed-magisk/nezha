package controller

import (
	"encoding/json"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/service/singleton"
)

// server.list 返回当前 PAT 可见的服务器精简列表。
//
// 输出字段刻意保持小：LLM context 很贵，列 100 台机器时不要把整张 Host/State 表
// 全塞进去。需要细节时再调 server.get。
type serverListItem struct {
	ID         uint64    `json:"id"`
	Name       string    `json:"name"`
	UUID       string    `json:"uuid,omitempty"`
	IPv4       string    `json:"ipv4,omitempty"`
	IPv6       string    `json:"ipv6,omitempty"`
	Online     bool      `json:"online"`
	Platform   string    `json:"platform,omitempty"`
	Arch       string    `json:"arch,omitempty"`
	LastActive time.Time `json:"last_active,omitempty"`
}

type serverListArgs struct {
	OnlineOnly bool `json:"online_only,omitempty"`
}

// serverListResult 是 server.list 的返回外壳。MCP 2025-06-18 规定
// structuredContent 必须是 JSON object，不能是裸数组/标量，否则严格客户端
// （官方 TS/Python SDK）会拒绝整条 tools/call 结果。因此这里把列表包进对象，
// 不再直接返回 []serverListItem。
type serverListResult struct {
	Servers []serverListItem `json:"servers"`
	Count   int              `json:"count"`
}

func serverListItemSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":          map[string]any{"type": "integer"},
			"name":        map[string]any{"type": "string"},
			"uuid":        map[string]any{"type": "string"},
			"ipv4":        map[string]any{"type": "string"},
			"ipv6":        map[string]any{"type": "string"},
			"online":      map[string]any{"type": "boolean"},
			"platform":    map[string]any{"type": "string"},
			"arch":        map[string]any{"type": "string"},
			"last_active": map[string]any{"type": "string", "format": "date-time"},
		},
		"required": []string{"id", "name", "online"},
	}
}

func init() {
	registerMCPTool(&mcpTool{
		Name:        "server.list",
		Description: "List servers visible to the current API token. Returns minimal metadata; call server.get for full details.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"online_only": map[string]any{
					"type":        "boolean",
					"description": "If true, only return servers that have reported within the last 30s.",
				},
			},
		},
		OutputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"servers": map[string]any{
					"type":  "array",
					"items": serverListItemSchema(),
				},
				"count": map[string]any{"type": "integer"},
			},
			"required": []string{"servers", "count"},
		},
		RequiredScope: model.ScopeInventoryRead,
		Handler:       handleServerList,
	})

	registerMCPTool(&mcpTool{
		Name:        "server.get",
		Description: "Return full Host/State snapshot for a single server.",
		InputSchema: serverGetSchema(),
		OutputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id":          map[string]any{"type": "integer"},
				"name":        map[string]any{"type": "string"},
				"uuid":        map[string]any{"type": "string"},
				"note":        map[string]any{"type": "string"},
				"public_note": map[string]any{"type": "string"},
				"host":        map[string]any{"type": "object"},
				"state":       map[string]any{"type": "object"},
				"geoip":       map[string]any{"type": "object"},
				"last_active": map[string]any{"type": "string", "format": "date-time"},
			},
			"required": []string{"id"},
		},
		RequiredScope: model.ScopeServerRead,
		Handler:       handleServerGet,
	})
}

func handleServerList(c *gin.Context, raw json.RawMessage) (any, error) {
	var args serverListArgs
	if err := decodeToolArgs(raw, &args); err != nil {
		return nil, err
	}
	tok := APITokenFromContext(c)
	if tok == nil {
		return nil, errNoToken
	}

	slist := singleton.ServerShared.GetSortedList()
	now := time.Now()
	const onlineWindow = 30 * time.Second

	out := make([]serverListItem, 0, len(slist))
	for _, s := range slist {
		if s == nil {
			continue
		}
		// 闸 1：复用现有用户权限过滤
		if !s.HasPermission(c) {
			continue
		}
		// 闸 2：PAT 的 server 白名单（若已设置）
		if !tok.CanAccessServer(s.ID) {
			continue
		}
		online := !s.LastActive.IsZero() && now.Sub(s.LastActive) < onlineWindow
		if args.OnlineOnly && !online {
			continue
		}
		item := serverListItem{
			ID:         s.ID,
			Name:       s.Name,
			UUID:       s.UUID,
			Online:     online,
			LastActive: s.LastActive,
		}
		if s.Host != nil {
			item.Platform = s.Host.Platform
			item.Arch = s.Host.Arch
		}
		if s.GeoIP != nil {
			item.IPv4 = s.GeoIP.IP.IPv4Addr
			item.IPv6 = s.GeoIP.IP.IPv6Addr
		}
		out = append(out, item)
	}
	return serverListResult{Servers: out, Count: len(out)}, nil
}

// server.get
type serverGetArgs struct {
	ServerID uint64 `json:"server_id"`
}

func serverGetSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"server_id": map[string]any{
				"type":        "integer",
				"description": "Target server ID.",
			},
		},
		"required": []string{"server_id"},
	}
}

func handleServerGet(c *gin.Context, raw json.RawMessage) (any, error) {
	var args serverGetArgs
	if err := decodeToolArgs(raw, &args); err != nil {
		return nil, err
	}
	s, err := requireServerAccess(c, args.ServerID)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"id":          s.ID,
		"name":        s.Name,
		"uuid":        s.UUID,
		"note":        s.Note,
		"public_note": s.PublicNote,
		"host":        s.Host,
		"state":       s.State,
		"geoip":       s.GeoIP,
		"last_active": s.LastActive,
	}, nil
}
