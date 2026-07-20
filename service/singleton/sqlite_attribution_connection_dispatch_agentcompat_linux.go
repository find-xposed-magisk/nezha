//go:build agentcompat && linux

package singleton

import (
	"context"
	"database/sql/driver"
	"errors"
)

func (connection *sqliteAttributionConnection) Exec(query string, values []driver.Value) (driver.Result, error) {
	statement, err := connection.Prepare(query)
	if err != nil {
		return nil, err
	}
	defer statement.Close()
	return statement.Exec(values)
}

func (connection *sqliteAttributionConnection) ExecContext(ctx context.Context, query string, values []driver.NamedValue) (driver.Result, error) {
	statement, err := connection.PrepareContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer statement.Close()
	return statement.(driver.StmtExecContext).ExecContext(ctx, values)
}

func (connection *sqliteAttributionConnection) Query(query string, values []driver.Value) (driver.Rows, error) {
	statement, err := connection.Prepare(query)
	if err != nil {
		return nil, err
	}
	wrapped := statement.(*sqliteAttributionStatement)
	return wrapped.queryOwned(func() (driver.Rows, error) { return wrapped.statement.Query(values) }, statement)
}

func (connection *sqliteAttributionConnection) QueryContext(ctx context.Context, query string, values []driver.NamedValue) (driver.Rows, error) {
	statement, err := connection.PrepareContext(ctx, query)
	if err != nil {
		return nil, err
	}
	return connection.queryContextStatement(ctx, statement.(*sqliteAttributionStatement), values, statement)
}

func (connection *sqliteAttributionConnection) queryContextStatement(ctx context.Context, statement *sqliteAttributionStatement, values []driver.NamedValue, owner driver.Stmt) (driver.Rows, error) {
	contextStatement, ok := statement.statement.(driver.StmtQueryContext)
	if !ok {
		return nil, errors.Join(driver.ErrSkip, owner.Close())
	}
	return statement.queryOwned(func() (driver.Rows, error) { return contextStatement.QueryContext(ctx, values) }, owner)
}
