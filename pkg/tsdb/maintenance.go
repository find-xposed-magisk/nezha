package tsdb

import (
	"log"
)

func (db *TSDB) Maintenance() {
	db.mu.RLock()
	defer db.mu.RUnlock()
	if db.closed {
		return
	}
	if !db.acceptsWrites() {
		return
	}

	log.Println("NEZHA>> TSDB starting maintenance (flush)...")
	db.debugFlushSafely()
	log.Println("NEZHA>> TSDB maintenance completed")
}
