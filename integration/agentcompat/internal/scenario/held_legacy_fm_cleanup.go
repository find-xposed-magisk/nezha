//go:build linux

package scenario

import "context"

type heldLegacyFMCapabilityCleanup struct {
	Unregister func(context.Context) error
	Absence    func(context.Context) error
	Cancel     func(context.Context) error
}

func pushHeldLegacyFMCapabilityCleanup(stack *heldCleanupStack, cleanup heldLegacyFMCapabilityCleanup) error {
	if err := stack.Push(heldCleanupAction{name: "unregister FM capability", cleanup: cleanup.Unregister}); err != nil {
		return err
	}
	if err := stack.Push(heldCleanupAction{name: "restore FM IOStream baseline and absence", cleanup: cleanup.Absence}); err != nil {
		return err
	}
	return stack.Push(heldCleanupAction{name: "cancel FM capability", cleanup: cleanup.Cancel})
}
