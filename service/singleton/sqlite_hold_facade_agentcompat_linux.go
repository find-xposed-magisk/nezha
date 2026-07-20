//go:build agentcompat && linux

package singleton

import "context"

func ArmNextSQLiteHold() (SQLiteHoldReceipt, error) {
	return sqliteAttributionHoldControl.ArmNextSQLiteHold()
}
func WaitSQLiteHoldSelected(ctx context.Context, receipt SQLiteHoldReceipt) (SQLiteHoldReceipt, error) {
	return sqliteAttributionHoldControl.WaitSelected(ctx, receipt)
}
func WaitSQLiteHoldFinalizing(ctx context.Context, receipt SQLiteHoldReceipt) (SQLiteHoldReceipt, error) {
	return sqliteAttributionHoldControl.WaitFinalizing(ctx, receipt)
}
func SnapshotSQLiteHold(receipt SQLiteHoldReceipt) (SQLiteHoldReceipt, error) {
	return sqliteAttributionHoldControl.Snapshot(receipt)
}
func ReleaseSQLiteHold(receipt SQLiteHoldReceipt) (SQLiteHoldReceipt, error) {
	return sqliteAttributionHoldControl.Release(receipt)
}
func AbortSQLiteHold(receipt SQLiteHoldReceipt) (SQLiteHoldReceipt, error) {
	return sqliteAttributionHoldControl.Abort(receipt)
}
