//go:build agentcompat && linux

package singleton

import (
	"errors"
)

func (connection *sqliteAttributionConnection) Close() error {
	connection.lifecycleMu.Lock()
	state := connection.transaction
	connection.lifecycleMu.Unlock()
	if state == nil {
		return connection.closeRaw()
	}
	// database/sql may close Conn before Tx reaches Rollback; converge the attribution state here.
	rollbackErr := state.rollback(connection)
	return errors.Join(rollbackErr, connection.closeRaw())
}

func (connection *sqliteAttributionConnection) closeRaw() error {
	if connection.closeRawConnection != nil {
		return connection.closeRawConnection()
	}
	return connection.connection.Close()
}
