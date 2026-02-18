package cberr

import (
	stderrors "errors"
	"fmt"
)

const (
	ErrUnknown            = "ERR_UNKNOWN"
	ErrInvalidArgs        = "ERR_INVALID_ARGS"
	ErrInvalidConfig      = "ERR_INVALID_CONFIG"
	ErrDoctorFailed       = "ERR_DOCTOR_FAILED"
	ErrScopeNotFound      = "ERR_SCOPE_NOT_FOUND"
	ErrScopeCollision     = "ERR_SCOPE_COLLISION"
	ErrProviderMissing    = "ERR_PROVIDER_NOT_REGISTERED"
	ErrBackendMissing     = "ERR_BACKEND_NOT_REGISTERED"
	ErrCapabilityMissing  = "ERR_CAPABILITY_NOT_SUPPORTED"
	ErrSwitchLockFailed   = "ERR_SWITCH_LOCK_FAILED"
	ErrSwitchFailed       = "ERR_SWITCH_FAILED"
	ErrSwitchValidation   = "ERR_SWITCH_VALIDATION_FAILED"
	ErrConfigOverridden   = "ERR_EFFECTIVE_CONFIG_OVERRIDDEN"
	ErrGenerationMismatch = "ERR_ACTIVE_GENERATION_MISMATCH"
	ErrStateReadFailed    = "ERR_STATE_READ_FAILED"
	ErrStateWriteFailed   = "ERR_STATE_WRITE_FAILED"
	ErrDownloadFailed     = "ERR_DOWNLOAD_FAILED"
	ErrChecksumMismatch   = "ERR_CHECKSUM_MISMATCH"
	ErrChecksumParse      = "ERR_CHECKSUM_PARSE"
	ErrExtractFailed      = "ERR_EXTRACT_FAILED"
	ErrLaunchctlFailed    = "ERR_LAUNCHCTL_FAILED"
	ErrServiceFailed      = "ERR_SERVICE_FAILED"
	ErrAuthSyncFailed     = "ERR_AUTH_SYNC_FAILED"
	ErrClaudeApplyFailed  = "ERR_CLAUDE_APPLY_FAILED"
	ErrClaudeRevertFailed = "ERR_CLAUDE_REVERT_FAILED"
	ErrRollbackFailed     = "ERR_ROLLBACK_FAILED"
	ErrPolicyViolation    = "ERR_POLICY_VIOLATION"
)

type CodedError struct {
	Code    string
	Message string
	Err     error
}

func (e *CodedError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message == "" && e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Code, e.Err)
	}
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Err)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *CodedError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func New(code, msg string) error {
	return &CodedError{Code: code, Message: msg}
}

func Wrap(code, msg string, err error) error {
	if err == nil {
		return &CodedError{Code: code, Message: msg}
	}
	return &CodedError{Code: code, Message: msg, Err: err}
}

func Code(err error) string {
	if err == nil {
		return ""
	}
	var coded *CodedError
	if stderrors.As(err, &coded) && coded.Code != "" {
		return coded.Code
	}
	return ErrUnknown
}
