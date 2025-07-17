-- +goose Up
-- +goose StatementBegin
SELECT 'up SQL query';

-- NEW INDEXES FOR REPORT QUERIES

-- Optimized for license-wide queries: license + operation_type + date range
-- Covers GetLicenseUsageReport queries that don't filter by asset_did
CREATE INDEX idx_credit_operations_report
    ON credit_operations(license_id, operation_type, created_at DESC);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 'down SQL query';
DROP INDEX idx_credit_operations_report;
-- +goose StatementEnd 