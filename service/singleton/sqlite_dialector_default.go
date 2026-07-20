//go:build !agentcompat || !linux

package singleton

import (
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func openSQLiteDialector(path string) gorm.Dialector {
	return sqlite.Open(path)
}
