-- +goose Up
-- +goose StatementBegin
SELECT 'up SQL query';
-- Credit grants created from blockchain token burns
-- Each grant represents tokens burned on-chain that create usable credits
CREATE TABLE credit_grants (
    -- Primary key
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(), -- Unique identifier for the grant
    tx_hash VARCHAR(66) NOT NULL,                 -- Blockchain transaction hash (0x...)
    log_index INTEGER CHECK (log_index >= 0),     -- Event log index within the transaction (null for pending grants)

    -- Core grant data
    license_id VARCHAR(255) NOT NULL,             -- License identifier: Ethereum address or string ID
    asset_did VARCHAR(500) NOT NULL,              -- DID string identifying the physical asset/device
    initial_amount BIGINT NOT NULL,               -- Original credit amount granted from a burn (never changes)
    remaining_amount BIGINT NOT NULL              -- Current unused credits (changes based on usage)
        CHECK (remaining_amount >= 0),
    expires_at TIMESTAMP NOT NULL,                -- When these credits become unusable

    -- Blockchain metadata
    block_number BIGINT,                          -- Blockchain block number (for verification and ordering)
    status VARCHAR(20) NOT NULL DEFAULT 'pending' -- Transaction state: 'pending', 'confirmed', 'failed'
        CHECK (status IN ('pending', 'confirmed', 'failed')),

    -- Timestamps
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP, -- When this record was created in our system
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP  -- Last modification (status changes, remaining_amount updates)
);

COMMENT ON TABLE credit_grants IS 'Credit grants created from blockchain token burns. Each grant represents tokens burned on-chain that create usable credits.';
COMMENT ON COLUMN credit_grants.tx_hash IS 'Blockchain transaction hash (0x...)';
COMMENT ON COLUMN credit_grants.log_index IS 'Event log index within the transaction';
COMMENT ON COLUMN credit_grants.license_id IS 'License identifier: Ethereum address or string ID';
COMMENT ON COLUMN credit_grants.asset_did IS 'DID string identifying the physical asset/device';
COMMENT ON COLUMN credit_grants.initial_amount IS 'Original credit amount granted from a burn (never changes)';
COMMENT ON COLUMN credit_grants.remaining_amount IS 'Current unused credits (changes based on usage)';
COMMENT ON COLUMN credit_grants.expires_at IS 'When these credits become unusable';
COMMENT ON COLUMN credit_grants.block_number IS 'Blockchain block number (for verification and ordering)';
COMMENT ON COLUMN credit_grants.status IS 'Transaction state: pending, confirmed, or failed';
COMMENT ON COLUMN credit_grants.created_at IS 'When this record was created in our system';
COMMENT ON COLUMN credit_grants.updated_at IS 'Last modification (status changes, remaining_amount updates)';

-- Credit operations represent API usage that could cause a change in state for the credit grants.
-- One operation can consume credits from one or more grants or refund credits to one or more grants (FIFO)
CREATE TABLE credit_operations (
    -- Primary key
    app_name VARCHAR(100) NOT NULL,               -- Which application made the request (e.g., 'telemetry-api', 'fetch-api')
    reference_id VARCHAR(255) NOT NULL,           -- External reference (API request ID, order ID, etc.)
    operation_type VARCHAR(20) NOT NULL           -- Type: 'deduction' (deducts credits), 'refund' (returns credits), 'grant_purchase' (new grant), 'debt_settlement' (settles previous debt)
        CHECK (operation_type IN ('deduction', 'refund', 'grant_purchase', 'grant_confirm', 'debt_settlement')),
    PRIMARY KEY (app_name, reference_id, operation_type), -- Composite primary key

    -- Core operation data
    license_id VARCHAR(255) NOT NULL,             -- License that used the credits
    asset_did VARCHAR(500) NOT NULL,              -- Asset that was accessed
    total_amount BIGINT NOT NULL,                 -- Total credits affected (negative for debit, positive for credit)

    -- Timestamps
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP -- When this operation occurred
);

COMMENT ON TABLE credit_operations IS 'Credit operations represent changes to credit balances. Operations are processed in chronological order.';
COMMENT ON COLUMN credit_operations.license_id IS 'License that used the credits';
COMMENT ON COLUMN credit_operations.asset_did IS 'Asset that was accessed';
COMMENT ON COLUMN credit_operations.operation_type IS 'Type: deduction (deducts credits), refund (returns credits), grant_purchase (new grant), debt_settlement (settles previous debt)';
COMMENT ON COLUMN credit_operations.total_amount IS 'Total credits affected (negative for debit, positive for credit)';
COMMENT ON COLUMN credit_operations.app_name IS 'Which application made the request (e.g., telemetry-api, fetch-api)';
COMMENT ON COLUMN credit_operations.reference_id IS 'External reference (API request ID, order ID, etc.)';
COMMENT ON COLUMN credit_operations.created_at IS 'When this operation occurred';

-- Junction table tracking which grants were used in each operation
-- Enables full audit trail of credit consumption across multiple grants
CREATE TABLE credit_operation_grants (
    -- Primary key
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(), -- Unique detail record identifier

    -- Foreign keys
    app_name VARCHAR(100) NOT NULL,               -- Links to the main operation record
    reference_id VARCHAR(255) NOT NULL,           -- Links to the main operation record
    operation_type VARCHAR(20) NOT NULL,          -- Links to the main operation record
    grant_id UUID NOT NULL                        -- Links to the credit grant
        REFERENCES credit_grants(id) ON DELETE CASCADE,
    FOREIGN KEY (app_name, reference_id, operation_type)          -- Composite foreign key to credit_operations
        REFERENCES credit_operations(app_name, reference_id, operation_type) ON DELETE CASCADE,

    -- Core data
    amount_used BIGINT NOT NULL ,                  -- How many credits were used added or taken from this specific grant
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP -- When this grant usage was recorded
);

COMMENT ON TABLE credit_operation_grants IS 'Junction table tracking which grants were used in each operation. Enables full audit trail of credit consumption across multiple grants.';
COMMENT ON COLUMN credit_operation_grants.id IS 'Unique detail record identifier';
COMMENT ON COLUMN credit_operation_grants.app_name IS 'Links to the main operation record';
COMMENT ON COLUMN credit_operation_grants.reference_id IS 'Links to the main operation record';
COMMENT ON COLUMN credit_operation_grants.operation_type IS 'Links to the main operation record';
COMMENT ON COLUMN credit_operation_grants.grant_id IS 'Links to the credit grant';
COMMENT ON COLUMN credit_operation_grants.amount_used IS 'How many credits were taken from this specific grant';
COMMENT ON COLUMN credit_operation_grants.created_at IS 'When this grant usage was recorded';

-- Performance indexes for common query patterns

-- Ensure uniqueness of confirmed grants
CREATE UNIQUE INDEX unique_confirmed_grants 
    ON credit_grants(tx_hash, log_index) 
    WHERE log_index IS NOT NULL;


-- Optimized for FIFO queries: active grants ordered by expiration
CREATE INDEX idx_credit_grants_active
    ON credit_grants(license_id, asset_did, status, expires_at, remaining_amount)
    WHERE remaining_amount > 0 AND status IN ('confirmed', 'pending');

-- Block number ordering for blockchain verification and event processing
CREATE INDEX idx_credit_grants_block
    ON credit_grants(block_number)
    WHERE block_number IS NOT NULL;

-- Failed grants with debt (for debt recovery queries)
CREATE INDEX idx_credit_grants_debt
    ON credit_grants(license_id, asset_did, created_at)
    WHERE status = 'failed' AND remaining_amount < initial_amount;

-- Transaction history queries (most recent first)
CREATE INDEX idx_credit_operations_license_asset
    ON credit_operations(license_id, asset_did, created_at DESC);

-- Finding all operations that used a specific grant
CREATE INDEX idx_credit_operation_grants_grant
    ON credit_operation_grants(grant_id);

-- Finding all grants used in a specific operation
CREATE INDEX idx_credit_operation_grants_operation
    ON credit_operation_grants(app_name, reference_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 'down SQL query';
DROP TABLE credit_grants;
DROP TABLE credit_operations;
DROP TABLE credit_operation_grants;
DROP INDEX idx_credit_grants_active;
DROP INDEX idx_credit_grants_block;
DROP INDEX idx_credit_grants_debt;
DROP INDEX idx_credit_operation_grants_grant;
DROP INDEX idx_credit_operation_grants_operation;
DROP INDEX unique_confirmed_grants;
-- +goose StatementEnd
