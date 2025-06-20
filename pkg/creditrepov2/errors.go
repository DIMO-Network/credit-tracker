package creditrepo

import (
	"errors"

	"github.com/lib/pq"
)

const (
	DuplicateKeyError = pq.ErrorCode("23505")
	DeadlockError     = pq.ErrorCode("40P01")
)

// IsDuplicateKeyError checks if the error is a duplicate key error.
func IsDuplicateKeyError(err error) bool {
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && pqErr.Code == DuplicateKeyError
}

// IsDeadlockError checks if the error is a deadlock error.
func IsDeadlockError(err error) bool {
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && pqErr.Code == DeadlockError
}
