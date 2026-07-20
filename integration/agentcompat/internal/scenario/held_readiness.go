//go:build linux

package scenario

import (
	"errors"
	"fmt"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/agent"
)

var (
	ErrInvalidHeldReadiness                = errors.New("held readiness is invalid")
	ErrHeldReadinessServerID               = errors.New("held readiness server ID is missing")
	ErrHeldReadinessUUID                   = errors.New("held readiness UUID is missing")
	ErrHeldReadinessAgentMismatch          = errors.New("held readiness does not match Agent")
	ErrHeldReadinessVersion                = errors.New("held readiness version is missing")
	ErrHeldReadinessOnline                 = errors.New("held readiness is offline")
	ErrHeldReadinessVersionObserved        = errors.New("held readiness version was not observed")
	ErrHeldReadinessRequestTaskEstablished = errors.New("held readiness RequestTask was not established")
	ErrHeldReadinessStateReceiptObserved   = errors.New("held readiness state receipt was not observed")
)

type HeldReadinessValidationError struct {
	Field string
	cause error
}

func (validationError *HeldReadinessValidationError) Error() string {
	return fmt.Sprintf("held readiness field %q: %s", validationError.Field, validationError.cause)
}

func (validationError *HeldReadinessValidationError) Is(target error) bool {
	return target == ErrInvalidHeldReadiness || target == validationError.cause
}

func validateHeldReadiness(agentInstance *agent.Agent, readiness agent.Readiness) error {
	if agentInstance == nil {
		return ErrInvalidHeldReadiness
	}
	if readiness.ServerID == 0 {
		return newHeldReadinessValidationError("server_id", ErrHeldReadinessServerID)
	}
	if readiness.UUID == "" {
		return newHeldReadinessValidationError("uuid", ErrHeldReadinessUUID)
	}
	return validateHeldReadinessFacts(heldSessionAgentFacts{PID: agentInstance.PID(), UUID: agentInstance.UUID()}, readiness)
}

func validateHeldReadinessFacts(agentFacts heldSessionAgentFacts, readiness agent.Readiness) error {
	if readiness.ServerID == 0 {
		return newHeldReadinessValidationError("server_id", ErrHeldReadinessServerID)
	}
	if readiness.UUID == "" {
		return newHeldReadinessValidationError("uuid", ErrHeldReadinessUUID)
	}
	if readiness.UUID != agentFacts.UUID {
		return newHeldReadinessValidationError("uuid", ErrHeldReadinessAgentMismatch)
	}
	if readiness.Version == "" {
		return newHeldReadinessValidationError("version", ErrHeldReadinessVersion)
	}
	if !readiness.Online {
		return newHeldReadinessValidationError("online", ErrHeldReadinessOnline)
	}
	if !readiness.VersionObserved {
		return newHeldReadinessValidationError("version_observed", ErrHeldReadinessVersionObserved)
	}
	if !readiness.RequestTaskEstablished {
		return newHeldReadinessValidationError("request_task_established", ErrHeldReadinessRequestTaskEstablished)
	}
	if !readiness.StateReceiptObserved {
		return newHeldReadinessValidationError("state_receipt_observed", ErrHeldReadinessStateReceiptObserved)
	}
	return nil
}

func newHeldReadinessValidationError(field string, cause error) error {
	return &HeldReadinessValidationError{Field: field, cause: cause}
}
