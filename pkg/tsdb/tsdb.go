package tsdb

import (
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/VictoriaMetrics/VictoriaMetrics/lib/storage"
)

// ErrDiskFull is returned when VictoriaMetrics cannot initialize because the
// filesystem containing the TSDB has run out of space.
var ErrDiskFull = errors.New("TSDB disk is full")

type readOnlyChecker interface {
	IsReadOnly() bool
}

// TSDB 封装 VictoriaMetrics 存储
type TSDB struct {
	storage *storage.Storage
	config  *Config
	mu      sync.RWMutex
	closed  bool

	writer *bufferedWriter

	readOnly     readOnlyChecker
	addRowsFn    func([]storage.MetricRow, uint8)
	debugFlushFn func()

	readOnlyObserved atomic.Bool
	diskFullPaused   atomic.Bool
}

// InitGlobalSettings 初始化 VictoriaMetrics 包级别的全局设置。
// 这些设置是进程级别的，应在 Open() 之前调用且只调用一次。
func InitGlobalSettings(config *Config) {
	memBytes := int(config.MaxMemoryMB * 1024 * 1024)
	storage.SetTSIDCacheSize(memBytes * 35 / 100)
	storage.SetMetricNameCacheSize(memBytes * 10 / 100)
	storage.SetTagFiltersCacheSize(memBytes * 5 / 100)
	storage.SetMetadataStorageSize(memBytes * 1 / 100)

	storage.SetDedupInterval(config.DedupInterval)
	storage.SetFreeDiskSpaceLimit(config.MinFreeDiskSpaceBytes())
	storage.SetDataFlushInterval(5 * time.Second)
}

// Open 打开或创建 TSDB 存储
func Open(config *Config) (db *TSDB, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			if diskErr := diskFullPanicError(recovered); diskErr != nil {
				db = nil
				err = diskErr
				return
			}
			panic(recovered)
		}
	}()

	if config == nil {
		config = DefaultConfig()
	}

	config.Validate()

	dataPath := config.DataPath
	if !filepath.IsAbs(dataPath) {
		absPath, err := filepath.Abs(dataPath)
		if err != nil {
			return nil, fmt.Errorf("failed to get absolute path: %w", err)
		}
		dataPath = absPath
	}

	InitGlobalSettings(config)

	opts := storage.OpenOptions{
		Retention: time.Duration(config.RetentionDays) * 24 * time.Hour,
	}

	stor := storage.MustOpenStorage(dataPath, opts)

	db = &TSDB{
		storage:      stor,
		config:       config,
		readOnly:     stor,
		addRowsFn:    stor.AddRows,
		debugFlushFn: stor.DebugFlush,
	}

	db.writer = newBufferedWriter(db, config.WriteBufferSize, config.WriteBufferFlushInterval)

	log.Printf("NEZHA>> TSDB opened at %s, retention: %d days, min free disk: %.1f GB, max memory: %d MB",
		dataPath, config.RetentionDays, config.MinFreeDiskSpaceGB, config.MaxMemoryMB)

	return db, nil
}

func diskFullPanicError(recovered any) error {
	message := strings.ToLower(fmt.Sprint(recovered))
	for _, marker := range []string{
		"no space left on device",
		"disk quota exceeded",
		"not enough space on the disk",
	} {
		if strings.Contains(message, marker) {
			return fmt.Errorf("%w: %v", ErrDiskFull, recovered)
		}
	}
	return nil
}

// WritesPaused reports whether new samples are currently being discarded.
// Queries remain available while VictoriaMetrics is in read-only mode.
func (db *TSDB) WritesPaused() bool {
	if db.diskFullPaused.Load() {
		return true
	}
	return db.readOnly != nil && db.readOnly.IsReadOnly()
}

func (db *TSDB) acceptsWrites() bool {
	if db.diskFullPaused.Load() {
		return false
	}

	readOnly := db.readOnly != nil && db.readOnly.IsReadOnly()
	if readOnly {
		if db.readOnlyObserved.CompareAndSwap(false, true) {
			log.Println("NEZHA>> TSDB writes paused because free disk space is below the configured limit; dashboard, queries, and alerts remain available")
		}
		return false
	}

	if db.readOnlyObserved.CompareAndSwap(true, false) {
		log.Println("NEZHA>> TSDB writes resumed after free disk space recovered")
	}
	return true
}

func (db *TSDB) pauseWritesAfterDiskFull(recovered any) {
	if db.diskFullPaused.CompareAndSwap(false, true) {
		log.Printf("NEZHA>> TSDB disk is full; disabling TSDB writes until dashboard restart while keeping dashboard, queries, and alerts running: %v", recovered)
	}
}

func (db *TSDB) addRowsSafely(rows []storage.MetricRow) {
	if len(rows) == 0 || !db.acceptsWrites() {
		return
	}

	defer func() {
		if recovered := recover(); recovered != nil {
			if diskFullPanicError(recovered) == nil {
				panic(recovered)
			}
			db.pauseWritesAfterDiskFull(recovered)
		}
	}()
	db.addRowsFn(rows, 64)
}

func (db *TSDB) debugFlushSafely() {
	if !db.acceptsWrites() {
		return
	}

	defer func() {
		if recovered := recover(); recovered != nil {
			if diskFullPanicError(recovered) == nil {
				panic(recovered)
			}
			db.pauseWritesAfterDiskFull(recovered)
		}
	}()
	db.debugFlushFn()
}

// Close 关闭 TSDB 存储
func (db *TSDB) Close() error {
	db.mu.Lock()
	defer db.mu.Unlock()

	if db.closed {
		return nil
	}

	if db.writer != nil {
		db.writer.stop()
	}

	db.storage.MustClose()
	db.closed = true
	log.Println("NEZHA>> TSDB closed")
	return nil
}

// Storage 返回底层存储对象（用于高级查询）
func (db *TSDB) Storage() *storage.Storage {
	return db.storage
}

// Config 返回配置
func (db *TSDB) Config() *Config {
	return db.config
}

// IsClosed 检查是否已关闭
func (db *TSDB) IsClosed() bool {
	db.mu.RLock()
	defer db.mu.RUnlock()
	return db.closed
}

// Flush 强制刷盘（主要用于测试）
func (db *TSDB) Flush() {
	db.mu.RLock()
	defer db.mu.RUnlock()
	if db.closed {
		return
	}

	if db.writer != nil {
		db.writer.flush()
	}
	db.debugFlushSafely()
}
