# Credit Tracking Service Design Document

## Overview

The Credit Tracking Service manages developer credits for API access through a gRPC interface. It handles credit deduction, automatic top-ups via blockchain token burns, and maintains transaction state for unconfirmed burns.

## Service Flow

### 1. API Access & Credit Deduction

```
Telemetry API → Credit Tracker (gRPC) → Database
"Deduct 10 credits for license X accessing asset Y"
```

- External APIs (telemetry, location, etc.) contact credit tracker via gRPC
- Credit tracker deducts from active grants using FIFO logic
- Returns success/failure to calling API

### 2. Insufficient Funds & Auto Top-Up

```
Insufficient Credits → Initiate DCX Burn → Pending Grant → Continue Operations
```

- When credits insufficient, service automatically burns DCX from developer's wallet
- Creates **pending** grant immediately (before blockchain confirmation)
- Allows continued API usage while transaction confirms (1-2 minutes)
- Developer temporarily goes negative until burn confirms

### 3. Transaction State Management

- **Pending**: Transaction submitted, credits usable, developer may be negative
- **Confirmed**: Transaction finalized on-chain, credits fully backed
- **Failed**: Transaction reverted, **debt must be cleared before further usage**

### 4. Failed Transaction Handling

When a pending transaction fails:

- Developer has used credits that were never actually backed by tokens
- **Debt = `initial_amount - remaining_amount`** for failed grants
- **All API usage blocked** until debt is cleared
- New grants must first restore failed grants to their `initial_amount`

## Database Schema

```sql
-- Credit grants created from blockchain token burns
-- Each grant represents tokens burned on-chain that create usable credits
CREATE TABLE credit_grants (
    tx_hash VARCHAR(66) PRIMARY KEY,              -- Blockchain transaction hash (0x...) - immutable proof
    license_id VARCHAR(255) NOT NULL,             -- License identifier: Ethereum address or string ID
    asset_did VARCHAR(500) NOT NULL,              -- DID string identifying the physical asset/device
    initial_amount BIGINT NOT NULL,               -- Original credit amount granted (never changes)
    remaining_amount BIGINT NOT NULL              -- Current unused credits (decreases with usage)
        CHECK (remaining_amount >= 0),
    expires_at TIMESTAMP NOT NULL,                -- When these credits become unusable (30 days from creation)
    status VARCHAR(20) NOT NULL DEFAULT 'pending' -- Transaction state: 'pending', 'confirmed', 'failed'
        CHECK (status IN ('pending', 'confirmed', 'failed')),
    block_number BIGINT,                          -- Blockchain block number (for verification and ordering)
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP, -- When this record was created in our system
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP  -- Last modification (status changes, remaining_amount updates)
);

-- Credit transactions represent API usage that consumes credits
-- One transaction can consume credits from multiple grants (FIFO)
CREATE TABLE credit_transactions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(), -- Unique transaction identifier
    license_id VARCHAR(255) NOT NULL,             -- License that used the credits
    asset_did VARCHAR(500) NOT NULL,              -- Asset that was accessed
    transaction_type VARCHAR(20) NOT NULL         -- Type: 'debit' (usage), 'credit' (grant confirmation), 'debt_recovery'
        CHECK (transaction_type IN ('debit', 'credit', 'debt_recovery')),
    total_amount BIGINT NOT NULL,                  -- Total credits affected (negative for debit, positive for credit)
    balance_after BIGINT NOT NULL,                 -- Total balance for this license/asset after transaction
    api_endpoint VARCHAR(100),                     -- Which API was called (e.g., 'telemetry', 'location')
    reference_id VARCHAR(255),                     -- External reference (API request ID, order ID, etc.)
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP -- When this transaction occurred
);

-- Junction table tracking which grants were used in each transaction
-- Enables full audit trail of credit consumption across multiple grants
CREATE TABLE credit_transaction_details (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(), -- Unique detail record identifier
    transaction_id UUID NOT NULL                   -- Links to the main transaction record
        REFERENCES credit_transactions(id) ON DELETE CASCADE,
    grant_tx_hash VARCHAR(66) NOT NULL             -- Which grant provided these credits
        REFERENCES credit_grants(tx_hash),
    amount_used BIGINT NOT NULL                     -- How many credits were taken from this specific grant
        CHECK (amount_used > 0),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP -- When this grant usage was recorded
);

-- Performance indexes for common query patterns

-- Fast lookups for calculating balances and finding grants for a license/asset
CREATE INDEX idx_credit_grants_license_asset
    ON credit_grants(license_id, asset_did);

-- Optimized for FIFO queries: active grants ordered by expiration
CREATE INDEX idx_credit_grants_active
    ON credit_grants(license_id, asset_did, expires_at)
    WHERE remaining_amount > 0 AND status IN ('confirmed', 'pending');

-- Quick filtering by transaction status (pending, confirmed, failed)
CREATE INDEX idx_credit_grants_status
    ON credit_grants(status);

-- Block number ordering for blockchain verification and event processing
CREATE INDEX idx_credit_grants_block
    ON credit_grants(block_number)
    WHERE block_number IS NOT NULL;

-- Failed grants with debt (for debt recovery queries)
CREATE INDEX idx_credit_grants_debt
    ON credit_grants(license_id, asset_did, created_at)
    WHERE status = 'failed' AND remaining_amount < initial_amount;

-- Transaction history queries (most recent first)
CREATE INDEX idx_credit_transactions_license_asset
    ON credit_transactions(license_id, asset_did, created_at DESC);

-- Fast lookup of transaction details for audit trails
CREATE INDEX idx_credit_transaction_details_transaction
    ON credit_transaction_details(transaction_id);

-- Finding all transactions that used a specific grant
CREATE INDEX idx_credit_transaction_details_grant
    ON credit_transaction_details(grant_tx_hash);

-- Composite index for efficient grant usage queries
CREATE INDEX idx_credit_transaction_details_grant_amount
    ON credit_transaction_details(grant_tx_hash, amount_used, created_at);
```

## Key Design Features

### FIFO Credit Consumption

Credits consumed oldest-first based on `expires_at`. Single API call can span multiple grants.

### Pending Transaction Support

- Pending grants included in balance calculations
- Enables immediate API usage during blockchain confirmation
- Developer balance can temporarily go negative

### Failed Transaction Debt Recovery

- **Debt = `initial_amount - remaining_amount`** for failed grants
- All API usage blocked until debt is cleared
- New grants must first restore failed grants before adding usable credits

### Transaction State Handling

- **Pending → Confirmed**: Normal flow, balance reconciled
- **Pending → Failed**: Developer has debt, API usage blocked until resolved
- **Developer Must Make Whole**: Cannot use credits until debt cleared by new grants

## ACID Guarantees

- Serializable isolation prevents race conditions
- Row-level locking ensures consistent FIFO ordering
- All credit operations are atomic across multiple grants

## Performance Considerations

- Indexes optimized for license/asset lookups and FIFO ordering
- Direct calculation from grants table (no cached balances)
- Expired grants ignored automatically via `expires_at > NOW()`
