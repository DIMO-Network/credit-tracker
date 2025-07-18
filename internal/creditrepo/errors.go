package creditrepo

import (
	"errors"

	"github.com/lib/pq"
)

const (
	// DuplicateKeyError is returned when a duplicate key error occurs.
	DuplicateKeyError = pq.ErrorCode("23505")
	// DeadlockError is returned when a deadlock error occurs.
	DeadlockError = pq.ErrorCode("40P01")
)
const (
	// InsufficientCreditsErr is returned when the credit balance is insufficient to perform the operation.
	InsufficientCreditsErr = constError("insufficient credits: would result in negative balance")

	// GrantAlreadyExistsErr is returned when a grant already exists for the given license and asset.
	GrantAlreadyExistsErr = constError("active grant already exists for the given license and asset")
)

type constError string

func (e constError) Error() string {
	return string(e)
}

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
