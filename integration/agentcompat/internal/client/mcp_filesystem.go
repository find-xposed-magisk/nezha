package client

import "encoding/json"

type WhoAmIResult struct {
	UserID    uint64   `json:"user_id"`
	IsAdmin   bool     `json:"is_admin"`
	TokenID   uint64   `json:"token_id"`
	TokenName string   `json:"token_name"`
	Scopes    []string `json:"scopes"`
	ServerIDs []uint64 `json:"server_ids"`
}

type ServerListArguments struct {
	OnlineOnly bool `json:"online_only"`
}

type ServerListItem struct {
	ID     uint64 `json:"id"`
	Name   string `json:"name"`
	UUID   string `json:"uuid"`
	Online bool   `json:"online"`
}

type ServerListResult struct {
	Servers []ServerListItem `json:"servers"`
	Count   int              `json:"count"`
}

type ServerGetArguments struct {
	ServerID uint64 `json:"server_id"`
}

type ServerGetResult struct {
	ID    uint64          `json:"id"`
	Name  string          `json:"name"`
	UUID  string          `json:"uuid"`
	Host  json.RawMessage `json:"host"`
	State json.RawMessage `json:"state"`
}

type FsListArguments struct {
	ServerID   uint64 `json:"server_id"`
	Path       string `json:"path"`
	ShowHidden bool   `json:"show_hidden"`
}

type FsEntry struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	Size       int64  `json:"size"`
	Mode       string `json:"mode"`
	MTime      int64  `json:"mtime"`
	IsSymlink  bool   `json:"is_symlink"`
	LinkTarget string `json:"link_target"`
}

type FsListResult struct {
	Entries   []FsEntry `json:"entries"`
	Truncated bool      `json:"truncated"`
	Total     int       `json:"total"`
}

type FsReadArguments struct {
	ServerID uint64 `json:"server_id"`
	Path     string `json:"path"`
	Offset   int64  `json:"offset"`
	Length   int64  `json:"length"`
	Encoding string `json:"encoding"`
}

type FsReadResult struct {
	Content   string `json:"content"`
	Encoding  string `json:"encoding"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256"`
	Truncated bool   `json:"truncated"`
}

type FsWriteArguments struct {
	ServerID      uint64 `json:"server_id"`
	Path          string `json:"path"`
	Content       string `json:"content"`
	Encoding      string `json:"encoding"`
	Mode          string `json:"mode"`
	IfMatchSHA256 string `json:"if_match_sha256"`
	CreateDirs    bool   `json:"create_dirs"`
}

type FsWriteResult struct {
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	Error  string `json:"error"`
}

type FsDeleteArguments struct {
	ServerID  uint64 `json:"server_id"`
	Path      string `json:"path"`
	Recursive bool   `json:"recursive"`
}

type FsDeleteResult struct {
	DeletedCount int `json:"deleted_count"`
}
