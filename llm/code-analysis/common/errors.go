package common

import (
	"fmt"
	"time"
)

type ErrorCode string

const (
	ErrCodeInvalidRequest  ErrorCode = "INVALID_REQUEST"
	ErrCodeRepoCloneFailed ErrorCode = "REPO_CLONE_FAILED"
	ErrCodeAnalysisFailed  ErrorCode = "ANALYSIS_FAILED"
	ErrCodeGitHubAPIError  ErrorCode = "GITHUB_API_ERROR"
	ErrCodeTimeout         ErrorCode = "TIMEOUT"
	ErrCodeInternalError   ErrorCode = "INTERNAL_ERROR"
	ErrCodeCredentialError ErrorCode = "CREDENTIAL_ERROR"
	ErrCodeFileNotFound    ErrorCode = "FILE_NOT_FOUND"
	ErrCodeSearchFailed    ErrorCode = "SEARCH_FAILED"
)

type AppError struct {
	Code      ErrorCode `json:"code"`
	Message   string    `json:"message"`
	Details   string    `json:"details,omitempty"`
	Cause     error     `json:"-"`
	Timestamp time.Time `json:"timestamp"`
	TraceID   string    `json:"trace_id,omitempty"`
}

func (e *AppError) Error() string {
	return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}

func NewAppError(code ErrorCode, message string, cause error) *AppError {
	return &AppError{
		Code:      code,
		Message:   message,
		Cause:     cause,
		Timestamp: time.Now(),
	}
}

func ErrorBadRequest(message string) *AppError {
	return NewAppError(ErrCodeInvalidRequest, message, nil)
}

func ErrorInternal(message string) *AppError {
	return NewAppError(ErrCodeInternalError, message, nil)
}

func ErrorTimeout(message string) *AppError {
	return NewAppError(ErrCodeTimeout, message, nil)
}

func ErrorRepoCloneFailed(message string, cause error) *AppError {
	return NewAppError(ErrCodeRepoCloneFailed, message, cause)
}

func ErrorAnalysisFailed(message string, cause error) *AppError {
	return NewAppError(ErrCodeAnalysisFailed, message, cause)
}

func ErrorCredential(message string, cause error) *AppError {
	return NewAppError(ErrCodeCredentialError, message, cause)
}

func ErrorFileNotFound(message string) *AppError {
	return NewAppError(ErrCodeFileNotFound, message, nil)
}

func ErrorSearchFailed(message string, cause error) *AppError {
	return NewAppError(ErrCodeSearchFailed, message, cause)
}
