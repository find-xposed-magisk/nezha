//go:build linux

package scenario

import "errors"

var (
	ErrHeldSessionSetOperation   = errors.New("held session set operation failed")
	ErrHeldSessionSetHealth      = errors.New("held session set health failed")
	ErrHeldSessionPrematureClose = errors.New("held session closed before set close")
)

type heldSessionSetClassifiedError struct {
	class  error
	causes []error
}

func (err *heldSessionSetClassifiedError) Error() string { return err.class.Error() }

func (err *heldSessionSetClassifiedError) Is(target error) bool {
	if errors.Is(err.class, target) {
		return true
	}
	for _, cause := range err.causes {
		if errors.Is(cause, target) {
			return true
		}
	}
	return false
}

func redactHeldSessionSetError(err error) error {
	if err == nil {
		return nil
	}
	return &heldSessionSetClassifiedError{class: ErrHeldSessionSetOperation, causes: []error{err}}
}

func redactHeldSessionSetHealthError(err error) error {
	if err == nil {
		return nil
	}
	return &heldSessionSetClassifiedError{class: ErrHeldSessionSetHealth, causes: []error{err}}
}
