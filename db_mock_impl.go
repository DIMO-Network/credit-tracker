package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	_ "github.com/lib/pq"
)

// CreditGrant represents a grant of credits from blockchain transaction
type CreditGrant struct {
	TxHash          string    `db:"tx_hash"`         // Blockchain transaction hash (primary key)
	LicenseID       string    `db:"license_id"`      // Can be Ethereum address or string identifier
	AssetDID        string    `db:"asset_did"`
	InitialAmount   int64     `db:"initial_amount"`
	RemainingAmount int64     `db:"remaining_amount"`
	ExpiresAt       time.Time `db:"expires_at"`
	Status          string    `db:"status"`          // 'pending', 'confirmed', 'failed'
	BlockNumber     *int64    `db:"block_number"`    // Optional: for additional verification
	CreatedAt       time.Time `db:"created_at"`
	UpdatedAt       time.Time `db:"updated_at"`
}

// CreditTransaction represents a credit usage transaction
type CreditTransaction struct {
	ID             uuid.UUID       `db:"id"`
	LicenseID      string          `db:"license_id"`
	AssetDID       string          `db:"asset_did"`
	TransactionType string         `db:"transaction_type"` // 'debit', 'credit', 'debt_recovery'
	TotalAmount    int64           `db:"total_amount"`
	BalanceAfter   int64           `db:"balance_after"`
	APIEndpoint    *string         `db:"api_endpoint"`
	ReferenceID    *string         `db:"reference_id"`
	Metadata       json.RawMessage `db:"metadata"`
	CreatedAt      time.Time       `db:"created_at"`
}

// CreditTransactionDetail represents which grants were used in a transaction
type CreditTransactionDetail struct {
	ID            uuid.UUID `db:"id"`
	TransactionID uuid.UUID `db:"transaction_id"`    // References credit_transactions.id
	GrantTxHash   string    `db:"grant_tx_hash"`     // References credit_grants.tx_hash
	AmountUsed    int64     `db:"amount_used"`       // How much was deducted from this grant
	CreatedAt     time.Time `db:"created_at"`
}

// DeductionResult represents the result of a credit deduction
type DeductionResult struct {
	Success         bool   `json:"success"`
	Error           string `json:"error,omitempty"`
	Message         string `json:"message,omitempty"`
	PreviousBalance int64  `json:"previous_balance,omitempty"`
	NewBalance      int64  `json:"new_balance,omitempty"`
	AmountDeducted  int64  `json:"amount_deducted,omitempty"`
	TransactionID   string `json:"transaction_id,omitempty"`
}

// CreditService handles credit operations
type CreditService struct {
	db *sql.DB
}

// NewCreditService creates a new credit service
func NewCreditService(db *sql.DB) *CreditService {
	return &CreditService{db: db}
}

// GetOutstandingDebt calculates debt from failed grants (initial_amount - remaining_amount)
func (cs *CreditService) GetOutstandingDebt(ctx context.Context, licenseID, assetDID string) (int64, error) {
	var totalDebt int64
	err := cs.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(initial_amount - remaining_amount), 0) 
		FROM credit_grants 
		WHERE license_id = $1 
		  AND asset_did = $2 
		  AND status = 'failed' 
		  AND remaining_amount < initial_amount`,
		licenseID, assetDID).Scan(&totalDebt)
	
	if err != nil {
		return 0, fmt.Errorf("failed to calculate outstanding debt: %w", err)
	}
	
	return totalDebt, nil
}

// GetActiveGrants retrieves active credit grants for a license/asset, ordered by expiration (FIFO)
// Excludes failed grants to prevent usage of unbacked credits
func (cs *CreditService) GetActiveGrants(ctx context.Context, tx *sql.Tx, licenseID, assetDID string) ([]CreditGrant, error) {
	query := `
		SELECT tx_hash, license_id, asset_did, initial_amount, remaining_amount, 
		       expires_at, status, block_number, created_at, updated_at
		FROM credit_grants 
		WHERE license_id = $1 
		  AND asset_did = $2 
		  AND remaining_amount > 0
		  AND expires_at > NOW()
		  AND status IN ('confirmed', 'pending')  -- Exclude failed grants
		ORDER BY expires_at ASC, created_at ASC
		FOR UPDATE`

	rows, err := tx.QueryContext(ctx, query, licenseID, assetDID)
	if err != nil {
		return nil, fmt.Errorf("failed to query active grants: %w", err)
	}
	defer rows.Close()

	var grants []CreditGrant
	for rows.Next() {
		var grant CreditGrant
		err := rows.Scan(
			&grant.TxHash, &grant.LicenseID, &grant.AssetDID,
			&grant.InitialAmount, &grant.RemainingAmount,
			&grant.ExpiresAt, &grant.Status, &grant.BlockNumber, 
			&grant.CreatedAt, &grant.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan grant: %w", err)
		}
		grants = append(grants, grant)
	}

	return grants, rows.Err()
}

// GetCurrentBalance calculates the current available balance from active grants only
func (cs *CreditService) GetCurrentBalance(ctx context.Context, licenseID, assetDID string) (int64, error) {
	var balance int64
	err := cs.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(remaining_amount), 0) 
		FROM credit_grants 
		WHERE license_id = $1 
		  AND asset_did = $2 
		  AND remaining_amount > 0 
		  AND expires_at > NOW()
		  AND status IN ('confirmed', 'pending')`,  // Exclude failed grants
		licenseID, assetDID).Scan(&balance)
	
	if err != nil {
		return 0, fmt.Errorf("failed to calculate current balance: %w", err)
	}
	
	return balance, nil
}

// GetCurrentBalanceWithLock calculates balance within a transaction with locking
func (cs *CreditService) GetCurrentBalanceWithLock(ctx context.Context, tx *sql.Tx, licenseID, assetDID string) (int64, error) {
	var balance int64
	err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(remaining_amount), 0) 
		FROM credit_grants 
		WHERE license_id = $1 
		  AND asset_did = $2 
		  AND remaining_amount > 0 
		  AND expires_at > NOW()
		  AND status IN ('confirmed', 'pending')`,  -- Exclude failed grants
		licenseID, assetDID).Scan(&balance)
	
	if err != nil {
		return 0, fmt.Errorf("failed to calculate current balance: %w", err)
	}
	
	return balance, nil
}

// DeductCredits deducts credits using FIFO logic with full ACID guarantees
// Blocks usage if there is outstanding debt from failed transactions
func (cs *CreditService) DeductCredits(ctx context.Context, licenseID, assetDID string, amount int64, apiEndpoint, referenceID string) (*DeductionResult, error) {
	// First check for outstanding debt from failed grants
	debt, err := cs.GetOutstandingDebt(ctx, licenseID, assetDID)
	if err != nil {
		return nil, fmt.Errorf("failed to check outstanding debt: %w", err)
	}
	
	if debt > 0 {
		return &DeductionResult{
			Success: false,
			Error:   "outstanding_debt",
			Message: fmt.Sprintf("Cannot use credits. Outstanding debt: %d. Please add credits to clear debt first.", debt),
		}, nil
	}

	// Use serializable isolation level to prevent phantom reads and ensure full consistency
	tx, err := cs.db.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelSerializable,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Calculate current available balance from active grants only
	currentBalance, err := cs.GetCurrentBalanceWithLock(ctx, tx, licenseID, assetDID)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate current balance: %w", err)
	}

	// Check if sufficient balance
	if currentBalance < amount {
		return &DeductionResult{
			Success: false,
			Error:   "insufficient_credits",
			Message: fmt.Sprintf("Insufficient credits. Current: %d, Required: %d", currentBalance, amount),
		}, nil
	}

	// Create transaction record
	transactionID := uuid.New()
	newBalance := currentBalance - amount

	_, err = tx.ExecContext(ctx, `
		INSERT INTO credit_transactions (
			id, license_id, asset_did, transaction_type, total_amount,
			balance_after, api_endpoint, reference_id, created_at
		) VALUES ($1, $2, $3, 'debit', $4, $5, $6, $7, NOW())`,
		transactionID, licenseID, assetDID, -amount, newBalance,
		apiEndpoint, referenceID)
	if err != nil {
		return nil, fmt.Errorf("failed to create transaction record: %w", err)
	}

	// Get active grants in FIFO order (with row-level locking)
	grants, err := cs.GetActiveGrants(ctx, tx, licenseID, assetDID)
	if err != nil {
		return nil, fmt.Errorf("failed to get active grants: %w", err)
	}

	// Deduct from grants using FIFO and record details
	remainingToDeduct := amount

	for _, grant := range grants {
		if remainingToDeduct <= 0 {
			break
		}

		deductionAmount := min(remainingToDeduct, grant.RemainingAmount)
		newGrantAmount := grant.RemainingAmount - deductionAmount

		// Update grant
		_, err = tx.ExecContext(ctx,
			"UPDATE credit_grants SET remaining_amount = $1, updated_at = NOW() WHERE tx_hash = $2",
			newGrantAmount, grant.TxHash)
		if err != nil {
			return nil, fmt.Errorf("failed to update grant %s: %w", grant.TxHash, err)
		}

		// Record which grant was used in this transaction
		_, err = tx.ExecContext(ctx, `
			INSERT INTO credit_transaction_details (
				transaction_id, grant_tx_hash, amount_used, created_at
			) VALUES ($1, $2, $3, NOW())`,
			transactionID, grant.TxHash, deductionAmount)
		if err != nil {
			return nil, fmt.Errorf("failed to record transaction detail: %w", err)
		}

		remainingToDeduct -= deductionAmount
	}

	// Commit the transaction
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return &DeductionResult{
		Success:         true,
		PreviousBalance: currentBalance,
		NewBalance:      newBalance,
		AmountDeducted:  amount,
		TransactionID:   transactionID.String(),
	}, nil
}

// AddCredits adds new credits with 30-day expiration from blockchain transaction
// If confirmed and debt exists, pays back failed grants first
func (cs *CreditService) AddCredits(ctx context.Context, txHash, licenseID, assetDID string, amount int64, status string, blockNumber *int64) error {
	// Use serializable isolation to prevent concurrent modifications
	tx, err := cs.db.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelSerializable,
	})
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Check if this transaction hash already exists (prevent double-processing)
	var exists bool
	err = tx.QueryRowContext(ctx,
		"SELECT EXISTS(SELECT 1 FROM credit_grants WHERE tx_hash = $1)",
		txHash).Scan(&exists)
	if err != nil {
		return fmt.Errorf("failed to check transaction existence: %w", err)
	}
	if exists {
		return fmt.Errorf("transaction %s already processed", txHash)
	}

	// Validate status
	if status != "pending" && status != "confirmed" && status != "failed" {
		return fmt.Errorf("invalid status: %s", status)
	}

	// If confirmed, first recover any failed grants (pay back debt)
	remainingAmount := amount
	if status == "confirmed" {
		// Get failed grants that need recovery (have debt)
		rows, err := tx.QueryContext(ctx, `
			SELECT tx_hash, initial_amount, remaining_amount
			FROM credit_grants 
			WHERE license_id = $1 
			  AND asset_did = $2 
			  AND status = 'failed' 
			  AND remaining_amount < initial_amount
			ORDER BY created_at ASC  -- Recover oldest debts first
			FOR UPDATE`, licenseID, assetDID)
		if err != nil {
			return fmt.Errorf("failed to get failed grants: %w", err)
		}
		defer rows.Close()

		for rows.Next() && remainingAmount > 0 {
			var failedTxHash string
			var initialAmount, currentRemaining int64
			
			err := rows.Scan(&failedTxHash, &initialAmount, &currentRemaining)
			if err != nil {
				return fmt.Errorf("failed to scan failed grant: %w", err)
			}

			debt := initialAmount - currentRemaining
			recovery := min(debt, remainingAmount)

			// Restore the failed grant partially or fully
			newRemaining := currentRemaining + recovery
			_, err = tx.ExecContext(ctx,
				"UPDATE credit_grants SET remaining_amount = $1, updated_at = NOW() WHERE tx_hash = $2",
				newRemaining, failedTxHash)
			if err != nil {
				return fmt.Errorf("failed to restore failed grant: %w", err)
			}

			remainingAmount -= recovery

			// Record debt recovery transaction
			recoveryTxID := uuid.New()
			_, err = tx.ExecContext(ctx, `
				INSERT INTO credit_transactions (
					id, license_id, asset_did, transaction_type, total_amount,
					balance_after, api_endpoint, reference_id, metadata, created_at
				) VALUES ($1, $2, $3, 'debt_recovery', $4, 0, 'debt_recovery', $5, $6, NOW())`,
				recoveryTxID, licenseID, assetDID, recovery, failedTxHash,
				json.RawMessage(fmt.Sprintf(`{"recovered_from": "%s", "amount": %d}`, failedTxHash, recovery)))
			if err != nil {
				return fmt.Errorf("failed to record debt recovery: %w", err)
			}

			// Record recovery detail
			_, err = tx.ExecContext(ctx, `
				INSERT INTO credit_transaction_details (
					transaction_id, grant_tx_hash, amount_used, created_at
				) VALUES ($1, $2, $3, NOW())`,
				recoveryTxID, failedTxHash, recovery)
			if err != nil {
				return fmt.Errorf("failed to record recovery detail: %w", err)
			}
		}

		// Check if we still have debt after this grant
		if remainingAmount == 0 {
			remainingDebt, err := cs.GetOutstandingDebt(ctx, licenseID, assetDID)
			if err == nil && remainingDebt > 0 {
				return fmt.Errorf("insufficient grant amount (%d) to cover total debt (%d)", amount, remainingDebt + amount - remainingAmount)
			}
		}
	}

	// Create new grant with remaining amount (if any)
	if remainingAmount > 0 {
		expiresAt := time.Now().Add(30 * 24 * time.Hour) // 30 days
		_, err = tx.ExecContext(ctx, `
			INSERT INTO credit_grants (
				tx_hash, license_id, asset_did, initial_amount, remaining_amount, 
				expires_at, status, block_number
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			txHash, licenseID, assetDID, remainingAmount, remainingAmount, expiresAt, status, blockNumber)
		if err != nil {
			return fmt.Errorf("failed to create credit grant: %w", err)
		}

		// Only create transaction record if confirmed (pending grants don't affect balance calculations yet)
		if status == "confirmed" {
			// Calculate new total balance
			newTotal, err := cs.GetCurrentBalanceWithLock(ctx, tx, licenseID, assetDID)
			if err != nil {
				return fmt.Errorf("failed to calculate new total balance: %w", err)
			}

			// Create transaction record
			transactionID := uuid.New()
			_, err = tx.ExecContext(ctx, `
				INSERT INTO credit_transactions (
					id, license_id, asset_did, transaction_type, total_amount,
					balance_after, api_endpoint, reference_id, created_at
				) VALUES ($1, $2, $3, 'credit', $4, $5, 'blockchain_burn', $6, NOW())`,
				transactionID, licenseID, assetDID, remainingAmount, newTotal, txHash)
			if err != nil {
				return fmt.Errorf("failed to record credit transaction: %w", err)
			}

			// Record transaction detail
			_, err = tx.ExecContext(ctx, `
				INSERT INTO credit_transaction_details (
					transaction_id, grant_tx_hash, amount_used, created_at
				) VALUES ($1, $2, $3, NOW())`,
				transactionID, txHash, remainingAmount)
			if err != nil {
				return fmt.Errorf("failed to record transaction detail: %w", err)
			}
		}
	} else {
		// All credits went to debt recovery, create a placeholder grant with 0 remaining
		expiresAt := time.Now().Add(30 * 24 * time.Hour)
		_, err = tx.ExecContext(ctx, `
			INSERT INTO credit_grants (
				tx_hash, license_id, asset_did, initial_amount, remaining_amount, 
				expires_at, status, block_number
			) VALUES ($1, $2, $3, $4, 0, $6, $7, $8)`,
			txHash, licenseID, assetDID, amount, expiresAt, status, blockNumber)
		if err != nil {
			return fmt.Errorf("failed to create debt recovery grant: %w", err)
		}
	}

	return tx.Commit()
}

// UpdateGrantStatus updates the status of a credit grant (e.g., pending -> confirmed)
func (cs *CreditService) UpdateGrantStatus(ctx context.Context, txHash, newStatus string) error {
	if newStatus != "pending" && newStatus != "confirmed" && newStatus != "failed" {
		return fmt.Errorf("invalid status: %s", newStatus)
	}

	tx, err := cs.db.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelSerializable,
	})
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Get current grant info
	var grant CreditGrant
	err = tx.QueryRowContext(ctx, `
		SELECT tx_hash, license_id, asset_did, initial_amount, remaining_amount,
		       expires_at, status, block_number, created_at, updated_at
		FROM credit_grants 
		WHERE tx_hash = $1 FOR UPDATE`, txHash).Scan(
		&grant.TxHash, &grant.LicenseID, &grant.AssetDID,
		&grant.InitialAmount, &grant.RemainingAmount,
		&grant.ExpiresAt, &grant.Status, &grant.BlockNumber,
		&grant.CreatedAt, &grant.UpdatedAt)
	if err != nil {
		return fmt.Errorf("failed to get grant: %w", err)
	}

	// Update status
	_, err = tx.ExecContext(ctx,
		"UPDATE credit_grants SET status = $1, updated_at = NOW() WHERE tx_hash = $2",
		newStatus, txHash)
	if err != nil {
		return fmt.Errorf("failed to update grant status: %w", err)
	}

	// If transitioning from pending to confirmed, create transaction records
	if grant.Status == "pending" && newStatus == "confirmed" && grant.RemainingAmount > 0 {
		// Calculate new total balance
		newTotal, err := cs.GetCurrentBalanceWithLock(ctx, tx, grant.LicenseID, grant.AssetDID)
		if err != nil {
			return fmt.Errorf("failed to calculate new total balance: %w", err)
		}

		// Create transaction record
		transactionID := uuid.New()
		_, err = tx.ExecContext(ctx, `
			INSERT INTO credit_transactions (
				id, license_id, asset_did, transaction_type, total_amount,
				balance_after, api_endpoint, reference_id, created_at
			) VALUES ($1, $2, $3, 'credit', $4, $5, 'blockchain_burn', $6, NOW())`,
			transactionID, grant.LicenseID, grant.AssetDID, grant.RemainingAmount, 
			newTotal, txHash)
		if err != nil {
			return fmt.Errorf("failed to record credit transaction: %w", err)
		}

		// Record transaction detail
		_, err = tx.ExecContext(ctx, `
			INSERT INTO credit_transaction_details (
				transaction_id, grant_tx_hash, amount_used, created_at
			) VALUES ($1, $2, $3, NOW())`,
			transactionID, txHash, grant.RemainingAmount)
		if err != nil {
			return fmt.Errorf("failed to record transaction detail: %w", err)
		}
	}

	return tx.Commit()
}

// HandleFailedTransaction marks a transaction as failed
// Debt becomes implicit: initial_amount - remaining_amount
func (cs *CreditService) HandleFailedTransaction(ctx context.Context, txHash string) error {
	_, err := cs.db.ExecContext(ctx,
		"UPDATE credit_grants SET status = 'failed', updated_at = NOW() WHERE tx_hash = $1",
		txHash)
	if err != nil {
		return fmt.Errorf("failed to update grant status: %w", err)
	}
	
	// Debt is now implicit: initial_amount - remaining_amount for this failed grant
	return nil
}

// GetTransactionDetails retrieves details about which grants were used in a transaction
func (cs *CreditService) GetTransactionDetails(ctx context.Context, transactionID uuid.UUID) ([]CreditTransactionDetail, error) {
	query := `
		SELECT id, transaction_id, grant_tx_hash, amount_used, created_at
		FROM credit_transaction_details
		WHERE transaction_id = $1
		ORDER BY created_at ASC`

	rows, err := cs.db.QueryContext(ctx, query, transactionID)
	if err != nil {
		return nil, fmt.Errorf("failed to query transaction details: %w", err)
	}
	defer rows.Close()

	var details []CreditTransactionDetail
	for rows.Next() {
		var detail CreditTransactionDetail
		err := rows.Scan(
			&detail.ID, &detail.TransactionID, &detail.GrantTxHash,
			&detail.AmountUsed, &detail.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan transaction detail: %w", err)
		}
		details = append(details, detail)
	}

	return details, rows.Err()
}

// GetFailedGrants retrieves all failed grants with outstanding debt
func (cs *CreditService) GetFailedGrants(ctx context.Context, licenseID, assetDID string) ([]CreditGrant, error) {
	query := `
		SELECT tx_hash, license_id, asset_did, initial_amount, remaining_amount,
		       expires_at, status, block_number, created_at, updated_at
		FROM credit_grants
		WHERE license_id = $1 
		  AND asset_did = $2 
		  AND status = 'failed' 
		  AND remaining_amount < initial_amount
		ORDER BY created_at ASC`

	rows, err := cs.db.QueryContext(ctx, query, licenseID, assetDID)
	if err != nil {
		return nil, fmt.Errorf("failed to query failed grants: %w", err)
	}
	defer rows.Close()

	var grants []CreditGrant
	for rows.Next() {
		var grant CreditGrant
		err := rows.Scan(
			&grant.TxHash, &grant.LicenseID, &grant.AssetDID,
			&grant.InitialAmount, &grant.RemainingAmount,
			&grant.ExpiresAt, &grant.Status, &grant.BlockNumber,
			&grant.CreatedAt, &grant.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan failed grant: %w", err)
		}
		grants = append(grants, grant)
	}

	return grants, rows.Err()
}

// CleanupExpiredGrants removes very old expired grants (optional maintenance)
func (cs *CreditService) CleanupExpiredGrants(ctx context.Context, olderThanDays int) (int64, error) {
	result, err := cs.db.ExecContext(ctx, `
		DELETE FROM credit_grants 
		WHERE expires_at < NOW() - INTERVAL '%d days' 
		  AND remaining_amount = 0`, olderThanDays)
	if err != nil {
		return 0, fmt.Errorf("failed to cleanup expired grants: %w", err)
	}
	
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to get affected rows: %w", err)
	}
	
	return rowsAffected, nil
}

// VerifyGrant verifies a credit grant exists for a given transaction hash
func (cs *CreditService) VerifyGrant(ctx context.Context, txHash string) (*CreditGrant, error) {
	var grant CreditGrant
	err := cs.db.QueryRowContext(ctx, `
		SELECT tx_hash, license_id, asset_did, initial_amount, remaining_amount,
		       expires_at, status, block_number, created_at, updated_at
		FROM credit_grants 
		WHERE tx_hash = $1`, txHash).Scan(
		&grant.TxHash, &grant.LicenseID, &grant.AssetDID,
		&grant.InitialAmount, &grant.RemainingAmount,
		&grant.ExpiresAt, &grant.Status, &grant.BlockNumber, 
		&grant.CreatedAt, &grant.UpdatedAt)
	
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("grant not found for transaction %s", txHash)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query grant: %w", err)
	}
	
	return &grant, nil
}

// min returns the minimum of two int64 values
func min(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

// Example usage demonstrating the service
func main() {
	db, err := sql.Open("postgres", "your-connection-string")
	if err != nil {
		panic(err)
	}
	defer db.Close()

	service := NewCreditService(db)
	ctx := context.Background()

	// Example 1: Add pending credits from blockchain transaction
	err = service.AddCredits(ctx, 
		"0x1234567890abcdef1234567890abcdef12345678", // transaction hash
		"0x742D35Cc6634C0532925A3b8D067C7b84C07e764", // license ID
		"did:erc721:0x1234567890abcdef1234567890abcdef12345678:10", // asset DID
		50_000,                                              // amount
		"pending",                                        // status
		nil)                                              // block number (not confirmed yet)
	if err != nil {
		fmt.Printf("Error adding pending credits: %v\n", err)
		return
	}

	// Example 2: Later, confirm the transaction
	err = service.UpdateGrantStatus(ctx,
		"0x1234567890abcdef1234567890abcdef12345678",
		"confirmed")
	if err != nil {
		fmt.Printf("Error confirming transaction: %v\n", err)
		return
	}

	// Example 3: Deduct credits for API usage
	result, err := service.DeductCredits(ctx,
		"0x742D35Cc6634C0532925A3b8D067C7b84C07e764",
		"did:erc721:0x742D35Cc6634C0532925A3b8D067C7b84C07e764:42",
		3,
		"telemetry",
		"telemetry-api-prod-242342")
	if err != nil {
		fmt.Printf("Error deducting credits: %v\n", err)
		return
	}

	fmt.Printf("Deduction result: %+v\n", result)

	// Example 4: Check transaction details
	if result.Success {
		transactionID, _ := uuid.Parse(result.TransactionID)
		details, err := service.GetTransactionDetails(ctx, transactionID)
		if err != nil {
			fmt.Printf("Error getting transaction details: %v\n", err)
			return
		}
		fmt.Printf("Transaction used %d grants\n", len(details))
	}

	// Example 5: Check current balance
	balance, err := service.GetCurrentBalance(ctx, 
		"0x742D35Cc6634C0532925A3b8D067C7b84C07e764",
		"did:erc721:0x742D35Cc6634C0532925A3b8D067C7b84C07e764:42")
	if err != nil {
		fmt.Printf("Error getting balance: %v\n", err)
		return
	}
	fmt.Printf("Current balance: %d\n", balance)

	// Example 6: Check for outstanding debt
	debt, err := service.GetOutstandingDebt(ctx,
		"0x742D35Cc6634C0532925A3b8D067C7b84C07e764",
		"did:erc721:0x742D35Cc6634C0532925A3b8D067C7b84C07e764:42")
	if err != nil {
		fmt.Printf("Error getting debt: %v\n", err)
		return
	}
	if debt > 0 {
		fmt.Printf("Outstanding debt: %d\n", debt)
	}

	// Example 7: Handle failed transaction
	err = service.HandleFailedTransaction(ctx, "0x9876543210fedcba9876543210fedcba98765432")
	if err != nil {
		fmt.Printf("Error handling failed transaction: %v\n", err)
		return
	}
}