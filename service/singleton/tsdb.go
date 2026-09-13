package singleton

import (
	"errors"
	"log"
	"time"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/pkg/tsdb"
)

var TSDBShared *tsdb.TSDB

var openTSDB = tsdb.Open

func InitTSDB() error {
	config := &tsdb.Config{
		RetentionDays:      30,
		MinFreeDiskSpaceGB: 1,
		MaxMemoryMB:        256,
	}

	if Conf.TSDB.DataPath != "" {
		config.DataPath = Conf.TSDB.DataPath
	}
	if Conf.TSDB.RetentionDays > 0 {
		config.RetentionDays = Conf.TSDB.RetentionDays
	}
	if Conf.TSDB.MinFreeDiskSpaceGB > 0 {
		config.MinFreeDiskSpaceGB = Conf.TSDB.MinFreeDiskSpaceGB
	}
	if Conf.TSDB.MaxMemoryMB > 0 {
		config.MaxMemoryMB = Conf.TSDB.MaxMemoryMB
	}
	if Conf.TSDB.WriteBufferSize > 0 {
		config.WriteBufferSize = Conf.TSDB.WriteBufferSize
	}
	if Conf.TSDB.WriteBufferFlushInterval > 0 {
		config.WriteBufferFlushInterval = time.Duration(Conf.TSDB.WriteBufferFlushInterval) * time.Second
	}

	if !config.Enabled() {
		log.Println("NEZHA>> TSDB is disabled (tsdb.data_path not configured)")
		if DB != nil {
			return DB.AutoMigrate(model.ServiceHistory{})
		}
		return nil
	}

	TSDBShared = nil
	db, err := openTSDB(config)
	if err != nil {
		if errors.Is(err, tsdb.ErrDiskFull) {
			log.Printf("NEZHA>> Warning: TSDB is unavailable because its disk is full; dashboard and alerts will continue without TSDB writes: %v", err)
			if DB != nil {
				if migrateErr := DB.AutoMigrate(model.ServiceHistory{}); migrateErr != nil {
					log.Printf("NEZHA>> Warning: failed to prepare SQLite service history fallback: %v", migrateErr)
				}
			}
			return nil
		}
		return err
	}
	TSDBShared = db

	log.Println("NEZHA>> TSDB initialized successfully")

	if DB != nil && DB.Migrator().HasTable("service_histories") {
		log.Println("NEZHA>> Dropping legacy service_histories table (TSDB is now enabled). Historical data will NOT be migrated.")
		if err := DB.Migrator().DropTable("service_histories"); err != nil {
			log.Printf("NEZHA>> Warning: failed to drop service_histories table: %v", err)
		}
	}

	return nil
}

func TSDBEnabled() bool {
	return TSDBShared != nil && !TSDBShared.IsClosed()
}

func CloseTSDB() {
	if TSDBShared != nil {
		TSDBShared.Close()
	}
}
