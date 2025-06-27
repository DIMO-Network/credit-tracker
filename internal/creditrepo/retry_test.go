package creditrepo

import (
	"context"
	"errors"
	"testing"

	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
)

func TestRetryWithDeadlockHandling_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := RetryWithDeadlockHandling(ctx, "TestFunction", func() (string, error) {
		return "success", nil
	})
	assert.Error(t, err)
	assert.Empty(t, result)
	assert.True(t, errors.Is(err, context.Canceled))
}

func TestRetryWithDeadlockHandling_NonDeadlockError(t *testing.T) {
	ctx := context.Background()
	expectedErr := errors.New("non-deadlock error")
	result, err := RetryWithDeadlockHandling(ctx, "TestFunction", func() (int, error) {
		return 0, expectedErr
	})
	assert.Error(t, err)
	assert.Equal(t, 0, result)
	assert.Equal(t, expectedErr, err)
}

func TestRetryWithDeadlockHandling_DeadlockError(t *testing.T) {
	ctx := context.Background()
	deadlockErr := &pq.Error{Code: DeadlockError}
	attempts := 0
	result, err := RetryWithDeadlockHandling(ctx, "TestFunction", func() (string, error) {
		attempts++
		if attempts <= 2 {
			return "", deadlockErr
		}
		return "success", nil
	})
	assert.NoError(t, err)
	assert.Equal(t, "success", result)
	assert.Equal(t, 3, attempts)
}

func TestRetryWithDeadlockHandling_SuccessOnFirstTry(t *testing.T) {
	ctx := context.Background()
	attempts := 0
	result, err := RetryWithDeadlockHandling(ctx, "TestFunction", func() (string, error) {
		attempts++
		return "success", nil
	})
	assert.NoError(t, err)
	assert.Equal(t, "success", result)
	assert.Equal(t, 1, attempts)
}

func TestIsDeadlockError(t *testing.T) {
	deadlockErr := &pq.Error{Code: DeadlockError}
	assert.True(t, IsDeadlockError(deadlockErr))
	otherErr := &pq.Error{Code: "23505"}
	assert.False(t, IsDeadlockError(otherErr))
	regularErr := errors.New("regular error")
	assert.False(t, IsDeadlockError(regularErr))
	assert.False(t, IsDeadlockError(nil))
}
