package creditrepo

import (
	"context"
	"time"

	"github.com/rs/zerolog"
)

const (
	defaultWait = time.Millisecond
)

// RetryWithDeadlockHandling is a generic retry function that handles deadlock errors
// It retries until context is cancelled or a non-deadlock error occurs
func RetryWithDeadlockHandling[T any](
	ctx context.Context,
	funcName string,
	operation func() (T, error),
) (T, error) {
	var lastErr error
	var result T
	attempt := 0

	for {
		attempt++

		// Check if context is cancelled before each attempt
		if err := ctx.Err(); err != nil {
			var zero T
			return zero, err
		}

		result, lastErr = operation()

		// If no error, return the result
		if lastErr == nil {
			return result, nil
		}

		// If it's not a deadlock error, return immediately
		if !IsDeadlockError(lastErr) {
			return result, lastErr
		}

		// Log the deadlock error
		logger := zerolog.Ctx(ctx)
		logger.Warn().
			Err(lastErr).
			Str("function", funcName).
			Int("attempt", attempt).
			Msg("Deadlock detected, retrying operation")

		// Wait with context cancellation support
		select {
		case <-ctx.Done():
			var zero T
			return zero, ctx.Err()
		case <-time.After(defaultWait):
			// Continue to next attempt
		}
	}
}
