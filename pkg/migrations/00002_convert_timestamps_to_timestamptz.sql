-- +goose Up
-- +goose StatementBegin
SELECT 'up SQL query';

-- Convert TIMESTAMP columns to TIMESTAMPTZ for proper timezone handling
-- This ensures that timezone information is preserved and handled correctly

-- Convert credit_grants table timestamps
ALTER TABLE credit_grants 
    ALTER COLUMN expires_at TYPE TIMESTAMPTZ,
    ALTER COLUMN created_at TYPE TIMESTAMPTZ,
    ALTER COLUMN updated_at TYPE TIMESTAMPTZ;

-- Convert credit_operations table timestamps
ALTER TABLE credit_operations 
    ALTER COLUMN created_at TYPE TIMESTAMPTZ;

-- Convert credit_operation_grants table timestamps
ALTER TABLE credit_operation_grants 
    ALTER COLUMN created_at TYPE TIMESTAMPTZ;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 'down SQL query';

-- Revert TIMESTAMPTZ columns back to TIMESTAMP
-- Note: This will lose timezone information

-- Revert credit_grants table timestamps
ALTER TABLE credit_grants 
    ALTER COLUMN expires_at TYPE TIMESTAMP,
    ALTER COLUMN created_at TYPE TIMESTAMP,
    ALTER COLUMN updated_at TYPE TIMESTAMP;

-- Revert credit_operations table timestamps
ALTER TABLE credit_operations 
    ALTER COLUMN created_at TYPE TIMESTAMP;

-- Revert credit_operation_grants table timestamps
ALTER TABLE credit_operation_grants 
    ALTER COLUMN created_at TYPE TIMESTAMP;

-- +goose StatementEnd 