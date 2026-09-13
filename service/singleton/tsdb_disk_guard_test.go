package singleton

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/pkg/tsdb"
)

func TestInitTSDBContinuesWhenDiskIsFull(t *testing.T) {
	originalConf := Conf
	originalDB := DB
	originalShared := TSDBShared
	originalOpen := openTSDB
	t.Cleanup(func() {
		Conf = originalConf
		DB = originalDB
		TSDBShared = originalShared
		openTSDB = originalOpen
	})

	Conf = &ConfigClass{Config: &model.Config{
		TSDB: model.TSDBConf{DataPath: "/full/tsdb"},
	}}
	DB = nil
	TSDBShared = nil
	openTSDB = func(*tsdb.Config) (*tsdb.TSDB, error) {
		return nil, fmt.Errorf("open failed: %w", tsdb.ErrDiskFull)
	}

	require.NoError(t, InitTSDB())
	require.Nil(t, TSDBShared)
}

func TestInitTSDBStillReturnsNonDiskErrors(t *testing.T) {
	originalConf := Conf
	originalShared := TSDBShared
	originalOpen := openTSDB
	t.Cleanup(func() {
		Conf = originalConf
		TSDBShared = originalShared
		openTSDB = originalOpen
	})

	Conf = &ConfigClass{Config: &model.Config{
		TSDB: model.TSDBConf{DataPath: "/broken/tsdb"},
	}}
	TSDBShared = nil
	openTSDB = func(*tsdb.Config) (*tsdb.TSDB, error) {
		return nil, fmt.Errorf("permission denied")
	}

	require.ErrorContains(t, InitTSDB(), "permission denied")
	require.Nil(t, TSDBShared)
}
