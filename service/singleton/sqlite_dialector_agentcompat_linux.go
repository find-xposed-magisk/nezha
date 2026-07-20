//go:build agentcompat && linux

package singleton

import (
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func openSQLiteDialector(path string) gorm.Dialector {
	registerSQLiteAttributionDriver()
	return sqlite.New(sqlite.Config{DriverName: sqliteAttributionDriverName, DSN: path})
}
