package application

import (
	"errors"
	"fmt"
)

type Code string

const (
	CodeInvalidRequest        Code = "invalid_request"
	CodeNotFound              Code = "not_found"
	CodeAlreadyExists         Code = "already_exists"
	CodeOffline               Code = "offline"
	CodeStaleTarget           Code = "stale_target"
	CodeScopeViolation        Code = "scope_violation"
	CodeUnboundTarget         Code = "unbound_target"
	CodeMissingCapability     Code = "missing_capability"
	CodeEnvironmentMismatch   Code = "environment_mismatch"
	CodeOwnerInstanceMismatch Code = "owner_instance_mismatch"
	CodeFixtureRequired       Code = "fixture_required"
	CodeUnsupportedProtocol   Code = "unsupported_protocol"
	CodeUnknownOperation      Code = "unknown_operation"
	CodeUncertainOutcome      Code = "uncertain_outcome"
	CodeOperationFailed       Code = "operation_failed"
)

type Error struct {
	Code    Code
	Message string
	Cause   error
}

func NewError(code Code, message string, cause error) *Error {
	return &Error{Code: code, Message: message, Cause: cause}
}

func (err *Error) Error() string {
	if err.Cause == nil {
		return fmt.Sprintf("%s: %s", err.Code, err.Message)
	}
	return fmt.Sprintf("%s: %s: %v", err.Code, err.Message, err.Cause)
}

func (err *Error) Unwrap() error { return err.Cause }

func CodeOf(err error) Code {
	if err == nil {
		return ""
	}
	var typed *Error
	if errors.As(err, &typed) {
		return typed.Code
	}
	return CodeOperationFailed
}

func failureOf(err error) *Failure {
	if err == nil {
		return nil
	}
	return &Failure{Code: CodeOf(err), Message: err.Error()}
}
