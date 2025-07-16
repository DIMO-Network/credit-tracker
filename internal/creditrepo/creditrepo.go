package creditrepo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/DIMO-Network/credit-tracker/models"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"github.com/volatiletech/null/v8"
	"github.com/volatiletech/sqlboiler/v4/boil"
	"github.com/volatiletech/sqlboiler/v4/queries/qm"
)

const (
	OperationTypeDeduction      = "deduction"
	OperationTypeRefund         = "refund"
	OperationTypeGrantPurchase  = "grant_purchase"
	OperationTypeGrantConfirm   = "grant_confirm"
	OperationTypeDebtSettlement = "debt_settlement"
)

const (
	GrantStatusPending   = "pending"
	GrantStatusConfirmed = "confirmed"
	GrantStatusFailed    = "failed"
)

func New(db *sql.DB) *Repository {
	return &Repository{db: db}
}

type Repository struct {
	db *sql.DB
}

// DeductCredits deducts credits using FIFO logic with full ACID guarantees
// 1. Check for outstanding debt from failed grants
// 2. Check if the current balance is sufficient
// 3. Create a operation record
// 4. Get active grants in FIFO order (with row-level locking)
// 5. Deduct from grants using FIFO and record details
// 6. Commit the operation
func (r *Repository) DeductCredits(ctx context.Context, licenseID, assetDID string, deductionAmount uint64, appName, referenceID string) (*models.CreditOperation, error) {
	return RetryWithDeadlockHandling(ctx, "DeductCredits", func() (*models.CreditOperation, error) {
		return r.deductCreditsInternal(ctx, licenseID, assetDID, deductionAmount, appName, referenceID)
	})
}

// deductCreditsInternal is the internal implementation of DeductCredits
func (r *Repository) deductCreditsInternal(ctx context.Context, licenseID, assetDID string, deductionAmount uint64, appName, referenceID string) (*models.CreditOperation, error) {
	if deductionAmount > math.MaxInt64 {
		return nil, fmt.Errorf("deduction amount is too large must be less than %d", math.MaxInt64)
	}
	amount := int64(deductionAmount)

	// First check for outstanding debt from failed grants
	debt, err := r.getOutstandingDebt(ctx, licenseID, assetDID)
	if err != nil {
		return nil, fmt.Errorf("failed to check outstanding debt: %w", err)
	}

	if debt > 0 {
		return nil, fmt.Errorf("cannot use credits, while there is outstanding debt: %d. Please add credits to clear debt first", debt)
	}

	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelReadCommitted,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer rollbackTx(ctx, tx)

	// Calculate current available balance from active grants only
	grants, err := r.getActiveGrants(ctx, tx, licenseID, assetDID)
	if err != nil {
		return nil, fmt.Errorf("failed to get active grants: %w", err)
	}
	// Note: There is a chance that a grant is inserted here after we pull active grants and before we calculate the current balance.
	// Which we are okay with because at the time of the original operation, the grant was not active.
	currentBalance := int64(0)
	for _, grant := range grants {
		currentBalance += grant.RemainingAmount
	}

	// Check if sufficient balance
	if currentBalance < amount {
		return nil, fmt.Errorf("%w. Current: %d, Required: %d", InsufficientCreditsErr, currentBalance, amount)
	}

	operation := &models.CreditOperation{
		LicenseID:     licenseID,
		AssetDid:      assetDID,
		OperationType: OperationTypeDeduction,
		TotalAmount:   amount,
		AppName:       appName,
		ReferenceID:   referenceID,
		CreatedAt:     null.TimeFrom(time.Now()),
	}

	if err := operation.Insert(ctx, tx, boil.Infer()); err != nil {
		if IsDuplicateKeyError(err) {
			// TODO: Need to get this to the gRPC caller
			return nil, fmt.Errorf("operation already exists: %w", err)
		}
		return nil, fmt.Errorf("failed to create operation record: %w", err)
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
		grant.RemainingAmount = newGrantAmount
		grant.UpdatedAt = null.TimeFrom(time.Now())
		if _, err := grant.Update(ctx, tx, boil.Whitelist(models.CreditGrantColumns.RemainingAmount, models.CreditGrantColumns.UpdatedAt)); err != nil {
			return nil, fmt.Errorf("failed to update grant %s: %w", grant.TXHash, err)
		}

		// Record which grant was used in this operation
		opGrant := &models.CreditOperationGrant{
			ID:            uuid.New().String(),
			AppName:       operation.AppName,
			ReferenceID:   operation.ReferenceID,
			OperationType: operation.OperationType,
			GrantID:       grant.ID,
			AmountUsed:    -deductionAmount,
			CreatedAt:     null.TimeFrom(time.Now()),
		}

		if err := opGrant.Insert(ctx, tx, boil.Infer()); err != nil {
			return nil, fmt.Errorf("failed to record operation grant: %w", err)
		}

		remainingToDeduct -= deductionAmount
	}

	// Commit the transaction
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return operation, nil
}

// RefundCredits refunds credits using FIFO logic with full ACID guarantees
// 1. find the grant for the given operation that is being refunded
// 2. Add funds back to the grant
// 3. Create a operation record for the refund
// 4. Settle any debt if any
func (r *Repository) RefundCredits(ctx context.Context, appName, referenceID string) (*models.CreditOperation, error) {
	return RetryWithDeadlockHandling(ctx, "RefundCredits", func() (*models.CreditOperation, error) {
		return r.refundCreditsInternal(ctx, appName, referenceID)
	})
}

// refundCreditsInternal is the internal implementation of RefundCredits
func (r *Repository) refundCreditsInternal(ctx context.Context, appName, referenceID string) (*models.CreditOperation, error) {
	// Start a transaction with read committed isolation
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelReadCommitted,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer rollbackTx(ctx, tx)

	// Get grants used in the original operation.
	grants, deductOp, err := r.getGrantsFromOperation(ctx, tx, referenceID, appName)
	if err != nil {
		return nil, fmt.Errorf("failed to get active grants: %w", err)
	}
	refundAmount := deductOp.TotalAmount

	operation := &models.CreditOperation{
		LicenseID:     deductOp.LicenseID,
		AssetDid:      deductOp.AssetDid,
		OperationType: OperationTypeRefund,
		TotalAmount:   refundAmount,
		AppName:       appName,
		ReferenceID:   referenceID,
		CreatedAt:     null.TimeFrom(time.Now()),
	}

	if err := operation.Insert(ctx, tx, boil.Infer()); err != nil {
		if IsDuplicateKeyError(err) {
			// TODO: Need to get this to the gRPC caller
			return nil, fmt.Errorf("operation already exists: %w", err)
		}
		return nil, fmt.Errorf("failed to create operation record: %w", err)
	}

	for _, opGrant := range grants {
		grant := opGrant.GetGrant()
		if grant == nil {
			return nil, fmt.Errorf("grant not found for operation grant %s", opGrant.ID)
		}
		grantRefundAmount := -opGrant.AmountUsed
		// Update grant
		newAmount := grant.RemainingAmount + grantRefundAmount
		if newAmount < grant.RemainingAmount {
			// integer overflow unexpected but can't hurt to check
			return nil, fmt.Errorf("grant refund would cause integer overflow")
		}
		grant.RemainingAmount = newAmount
		grant.UpdatedAt = null.TimeFrom(time.Now())
		if _, err := grant.Update(ctx, tx, boil.Whitelist(models.CreditGrantColumns.RemainingAmount, models.CreditGrantColumns.UpdatedAt)); err != nil {
			return nil, fmt.Errorf("failed to update grant %s: %w", grant.TXHash, err)
		}

		// Record which grant was used in this refund
		grantDetail := &models.CreditOperationGrant{
			ID:            uuid.New().String(),
			AppName:       operation.AppName,
			ReferenceID:   operation.ReferenceID,
			OperationType: operation.OperationType,
			GrantID:       grant.ID,
			AmountUsed:    grantRefundAmount,
			CreatedAt:     null.TimeFrom(time.Now()),
		}

		if err := grantDetail.Insert(ctx, tx, boil.Infer()); err != nil {
			return nil, fmt.Errorf("failed to record transaction detail: %w", err)
		}

	}

	err = r.settleDebt(ctx, tx, deductOp.LicenseID, deductOp.AssetDid, appName, referenceID)
	if err != nil {
		return nil, fmt.Errorf("failed to settle debt: %w", err)
	}

	// Commit the transaction
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return operation, nil
}

// CreateGrant creates a new grant for the given license and asset
// 1. Create a new grant record
// 2. Create a new operation record
// 3. Settle any debt if any
func (r *Repository) CreateGrant(ctx context.Context, licenseID, assetDID string, creditAmount uint64, txHash string, mintTime time.Time) (*models.CreditOperation, error) {
	return RetryWithDeadlockHandling(ctx, "CreateGrant", func() (*models.CreditOperation, error) {
		return r.createGrantInternal(ctx, licenseID, assetDID, creditAmount, txHash, mintTime)
	})
}

// createGrantInternal is the internal implementation of CreateGrant
func (r *Repository) createGrantInternal(ctx context.Context, licenseID, assetDID string, creditAmount uint64, txHash string, mintTime time.Time) (*models.CreditOperation, error) {
	if creditAmount == 0 {
		return nil, fmt.Errorf("invalid amount: %d. Amount must be positive", creditAmount)
	}
	if creditAmount > math.MaxInt64 {
		return nil, fmt.Errorf("credit amount is too large must be less than %d", math.MaxInt64)
	}
	amount := int64(creditAmount)

	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelReadCommitted,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer rollbackTx(ctx, tx)

	// Create grant record
	grant := &models.CreditGrant{
		LicenseID:       licenseID,
		AssetDid:        assetDID,
		InitialAmount:   amount,
		RemainingAmount: amount,
		Status:          GrantStatusPending,
		TXHash:          txHash,
		ExpiresAt:       getExpirationDate(mintTime),
	}

	if err := grant.Insert(ctx, tx, boil.Infer()); err != nil {
		return nil, fmt.Errorf("failed to create grant record: %w", err)
	}

	operation := &models.CreditOperation{
		LicenseID:     licenseID,
		AssetDid:      assetDID,
		OperationType: OperationTypeGrantPurchase,
		TotalAmount:   amount,
		AppName:       "credit_tracker",
		ReferenceID:   grant.ID,
		CreatedAt:     null.TimeFrom(time.Now()),
	}

	if err := operation.Insert(ctx, tx, boil.Infer()); err != nil {
		return nil, fmt.Errorf("failed to create operation record: %w", err)
	}

	opGrant := &models.CreditOperationGrant{
		AppName:       operation.AppName,
		ReferenceID:   operation.ReferenceID,
		OperationType: operation.OperationType,
		GrantID:       grant.ID,
		AmountUsed:    amount,
	}
	if err := opGrant.Insert(ctx, tx, boil.Infer()); err != nil {
		return nil, fmt.Errorf("failed to record operation grant: %w", err)
	}

	err = r.settleDebt(ctx, tx, licenseID, assetDID, "credit_tracker", grant.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to settle debt: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return operation, nil
}

// confirmGrant confirms a grant for the given license and asset
// 1. Update the grant record to set the log index
// 2. Create a new operation record
// 3. Settle any debt if any
func (r *Repository) ConfirmGrant(ctx context.Context, licenseID, assetDID string, txHash string, logIndex int, creditAmount uint64, mintTime time.Time) (*models.CreditOperation, error) {
	return RetryWithDeadlockHandling(ctx, "ConfirmGrant", func() (*models.CreditOperation, error) {
		return r.confirmGrantInternal(ctx, licenseID, assetDID, txHash, logIndex, creditAmount, mintTime)
	})
}

// confirmGrantInternal is the internal implementation of ConfirmGrant
func (r *Repository) confirmGrantInternal(ctx context.Context, licenseID, assetDID string, txHash string, logIndex int, creditAmount uint64, mintTime time.Time) (*models.CreditOperation, error) {
	if creditAmount == 0 {
		return nil, fmt.Errorf("invalid amount: %d. Amount must be positive", creditAmount)
	}
	if creditAmount > math.MaxInt64 {
		return nil, fmt.Errorf("credit amount is too large must be less than %d", math.MaxInt64)
	}
	amount := int64(creditAmount)
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelReadCommitted,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer rollbackTx(ctx, tx)

	// get the oldest pending grant that matches the given parameters
	grant, err := models.CreditGrants(
		models.CreditGrantWhere.TXHash.EQ(txHash),
		models.CreditGrantWhere.LicenseID.EQ(licenseID),
		models.CreditGrantWhere.AssetDid.EQ(assetDID),
		models.CreditGrantWhere.Status.EQ(GrantStatusPending),
		qm.OrderBy(models.CreditGrantColumns.CreatedAt+" ASC, "+models.CreditGrantColumns.ID+" ASC"),
		qm.For("UPDATE"),
	).One(ctx, tx)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("failed to find grant: %w", err)
		}
		// create a new grant if there is no matching grant
		grant = &models.CreditGrant{
			LicenseID:       licenseID,
			AssetDid:        assetDID,
			InitialAmount:   amount,
			RemainingAmount: amount,
			TXHash:          txHash,
			Status:          GrantStatusConfirmed,
			LogIndex:        null.IntFrom(logIndex),
			ExpiresAt:       getExpirationDate(mintTime),
			CreatedAt:       null.TimeFrom(time.Now()),
			UpdatedAt:       null.TimeFrom(time.Now()),
		}
		if err := grant.Insert(ctx, tx, boil.Infer()); err != nil {
			return nil, fmt.Errorf("failed to create grant record: %w", err)
		}
	} else {
		grant.LogIndex = null.IntFrom(logIndex)
		grant.Status = GrantStatusConfirmed
		grant.UpdatedAt = null.TimeFrom(time.Now())

		if _, err := grant.Update(ctx, tx, boil.Whitelist(models.CreditGrantColumns.LogIndex, models.CreditGrantColumns.Status, models.CreditGrantColumns.UpdatedAt)); err != nil {
			return nil, fmt.Errorf("failed to update grant: %w", err)
		}
	}

	operation := &models.CreditOperation{
		LicenseID:     licenseID,
		AssetDid:      assetDID,
		OperationType: OperationTypeGrantConfirm,
		TotalAmount:   amount,
		AppName:       "credit_tracker",
		ReferenceID:   grant.ID,
	}
	if err := operation.Insert(ctx, tx, boil.Infer()); err != nil {
		return nil, fmt.Errorf("failed to create operation record: %w", err)
	}

	opGrant := &models.CreditOperationGrant{
		AppName:       operation.AppName,
		ReferenceID:   operation.ReferenceID,
		OperationType: operation.OperationType,
		GrantID:       grant.ID,
		AmountUsed:    amount,
	}
	if err := opGrant.Insert(ctx, tx, boil.Infer()); err != nil {
		return nil, fmt.Errorf("failed to record grant operation: %w", err)
	}

	err = r.settleDebt(ctx, tx, licenseID, assetDID, "credit_tracker", grant.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to settle debt: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return operation, nil
}

// GetBalance returns the balance for the given license and asset
// 1. Get the outstanding debt
// 2. If the debt is positive, return the debt
// 3. If the debt is negative, return the balance
// 4. If the debt is zero, return the balance
func (r *Repository) GetBalance(ctx context.Context, licenseID, assetDID string) (int64, error) {
	return RetryWithDeadlockHandling(ctx, "GetBalance", func() (int64, error) {
		return r.getBalanceInternal(ctx, licenseID, assetDID)
	})
}

// getBalanceInternal is the internal implementation of GetBalance
func (r *Repository) getBalanceInternal(ctx context.Context, licenseID, assetDID string) (int64, error) {
	debt, err := r.getOutstandingDebt(ctx, licenseID, assetDID)
	if err != nil {
		return 0, fmt.Errorf("failed to get outstanding debt: %w", err)
	}
	if debt < 0 {
		return -debt, nil
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelReadCommitted,
	})
	if err != nil {
		return 0, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer rollbackTx(ctx, tx)

	balance, err := r.calculateBalance(ctx, tx, licenseID, assetDID)
	if err != nil {
		return 0, fmt.Errorf("failed to calculate balance: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return balance, nil
}

// calculateBalance calculates the balance for the given license and asset
func (r *Repository) calculateBalance(ctx context.Context, tx *sql.Tx, licenseID, assetDID string) (int64, error) {
	// use sql to add up the remaining amount of all confirmed/pending grants that are not expired
	var sum int64
	err := models.CreditGrants(
		qm.Select("COALESCE(SUM(remaining_amount), 0)"),
		models.CreditGrantWhere.LicenseID.EQ(licenseID),
		models.CreditGrantWhere.AssetDid.EQ(assetDID),
		models.CreditGrantWhere.Status.IN([]string{GrantStatusConfirmed, GrantStatusPending}),
		models.CreditGrantWhere.ExpiresAt.GT(time.Now()),
		models.CreditGrantWhere.RemainingAmount.GT(0),
	).QueryRowContext(ctx, tx).Scan(&sum)
	if err != nil {
		return 0, fmt.Errorf("failed to calculate balance: %w", err)
	}

	return sum, nil
}

// getActiveGrants retrieves active credit grants for a license/asset, ordered by expiration (FIFO)
func (r *Repository) getActiveGrants(ctx context.Context, tx *sql.Tx, licenseID, assetDID string) ([]*models.CreditGrant, error) {
	grants, err := models.CreditGrants(
		models.CreditGrantWhere.LicenseID.EQ(licenseID),
		models.CreditGrantWhere.AssetDid.EQ(assetDID),
		models.CreditGrantWhere.RemainingAmount.GT(0),
		models.CreditGrantWhere.ExpiresAt.GT(time.Now()),
		models.CreditGrantWhere.Status.IN([]string{GrantStatusConfirmed, GrantStatusPending}),
		qm.OrderBy(models.CreditGrantColumns.ExpiresAt+" ASC, "+models.CreditGrantColumns.CreatedAt+" ASC, "+models.CreditGrantColumns.ID+" ASC"),
		qm.For("UPDATE"),
	).All(ctx, tx)

	if err != nil {
		return nil, fmt.Errorf("failed to query active grants: %w", err)
	}

	return grants, nil
}

// getFailedGrants retrieves all failed grants with outstanding debt
func (r *Repository) getFailedGrants(ctx context.Context, tx *sql.Tx, licenseID, assetDID string) ([]*models.CreditGrant, error) {
	grants, err := models.CreditGrants(
		models.CreditGrantWhere.LicenseID.EQ(licenseID),
		models.CreditGrantWhere.AssetDid.EQ(assetDID),
		models.CreditGrantWhere.Status.EQ(GrantStatusFailed),
		qm.Where(models.CreditGrantColumns.RemainingAmount+" < "+models.CreditGrantColumns.InitialAmount),
		qm.OrderBy(models.CreditGrantColumns.CreatedAt+" ASC, "+models.CreditGrantColumns.ID+" ASC"),
		qm.For("UPDATE"),
	).All(ctx, tx)
	if err != nil {
		return nil, fmt.Errorf("failed to query failed grants: %w", err)
	}

	return grants, nil
}

// getOutstandingDebt calculates debt from failed grants (initial_amount - remaining_amount)
func (r *Repository) getOutstandingDebt(ctx context.Context, licenseID, assetDID string) (int64, error) {
	var totalDebt int64
	err := models.CreditGrants(
		qm.Select("COALESCE(SUM(initial_amount - remaining_amount), 0) as total_debt"),
		models.CreditGrantWhere.LicenseID.EQ(licenseID),
		models.CreditGrantWhere.AssetDid.EQ(assetDID),
		models.CreditGrantWhere.Status.EQ(GrantStatusFailed),
		qm.Where(models.CreditGrantColumns.RemainingAmount+" < "+models.CreditGrantColumns.InitialAmount),
	).QueryRowContext(ctx, r.db).Scan(&totalDebt)

	if err != nil {
		return 0, fmt.Errorf("failed to calculate outstanding debt: %w", err)
	}

	return totalDebt, nil
}

// settleDebt settles any debt for the given license and asset
// Gets all grants and moves remaining balance from all active grants to any failed grants that do not have inital == remaining
// 1. Get all failed grants that have debt oldest first
// 2. Get all active grants
// 3. For each failed grant, try to settle from active grants
// 4. If we were able to settle any amount, update the failed grant
// 5. Update the operation with the final balance
func (r *Repository) settleDebt(ctx context.Context, tx *sql.Tx, licenseID, assetDID, appName, referenceID string) error {
	debt, err := r.getOutstandingDebt(ctx, licenseID, assetDID)
	if err != nil {
		return fmt.Errorf("failed to get outstanding debt: %w", err)
	}
	if debt == 0 {
		// no debt to settle
		return nil
	}

	balance, err := r.calculateBalance(ctx, tx, licenseID, assetDID)
	if err != nil {
		return fmt.Errorf("failed to calculate balance: %w", err)
	}
	if balance == 0 {
		// no available balance to settle debt
		return nil
	}

	operation := &models.CreditOperation{
		LicenseID:     licenseID,
		AssetDid:      assetDID,
		OperationType: OperationTypeDebtSettlement,
		TotalAmount:   min(debt, balance), // either all the debt is settled or all the balance is used
		AppName:       appName,
		ReferenceID:   referenceID,
		CreatedAt:     null.TimeFrom(time.Now()),
	}
	if err := operation.Insert(ctx, tx, boil.Infer()); err != nil {
		return fmt.Errorf("failed to create operation record: %w", err)
	}

	// Get All failed grants
	failedGrants, err := r.getFailedGrants(ctx, tx, licenseID, assetDID)
	if err != nil {
		return err
	}

	// Get All active grants
	activeGrants, err := r.getActiveGrants(ctx, tx, licenseID, assetDID)
	if err != nil {
		return err
	}

	// For each failed grant, try to settle from active grants
	for _, failedGrant := range failedGrants {
		grantDebt := failedGrant.InitialAmount - failedGrant.RemainingAmount
		if grantDebt <= 0 {
			continue
		}

		// Try to settle from active grants
		remainingToSettle := grantDebt
		for _, activeGrant := range activeGrants {
			if remainingToSettle <= 0 {
				break
			}

			// deduct as much as possible from the active grant
			availableAmount := min(remainingToSettle, activeGrant.RemainingAmount)
			if availableAmount <= 0 {
				continue
			}

			activeGrant.RemainingAmount -= availableAmount
			activeGrant.UpdatedAt = null.TimeFrom(time.Now())
			_, err := activeGrant.Update(ctx, tx, boil.Whitelist(models.CreditGrantColumns.RemainingAmount, models.CreditGrantColumns.UpdatedAt))
			if err != nil {
				return fmt.Errorf("failed to update active grant %s: %w", activeGrant.TXHash, err)
			}

			// Record the transfer in operation grants
			grantDetail := &models.CreditOperationGrant{
				ID:            uuid.New().String(),
				AppName:       operation.AppName,
				ReferenceID:   operation.ReferenceID,
				OperationType: operation.OperationType,
				GrantID:       activeGrant.ID,
				AmountUsed:    availableAmount,
				CreatedAt:     null.TimeFrom(time.Now()),
			}

			if err := grantDetail.Insert(ctx, tx, boil.Infer()); err != nil {
				return fmt.Errorf("failed to record transfer detail: %w", err)
			}

			remainingToSettle -= availableAmount
		}

		// If we did not settle any amount then there are no more active grants to settle from
		if remainingToSettle >= grantDebt {
			zerolog.Ctx(ctx).Debug().Msgf("No more active grants to settle from for failed grant %s", failedGrant.TXHash)
			break
		}

		// If we were able to settle any amount, update the failed grant
		amountSettled := grantDebt - remainingToSettle
		newAmount := failedGrant.RemainingAmount + amountSettled
		if newAmount < failedGrant.RemainingAmount {
			// integer overflow unexpected but can't hurt to check
			return fmt.Errorf("debt settlement would cause integer overflow for grant %s", failedGrant.ID)
		}
		failedGrant.RemainingAmount = newAmount
		failedGrant.UpdatedAt = null.TimeFrom(time.Now())
		if _, err := failedGrant.Update(ctx, tx, boil.Whitelist(models.CreditGrantColumns.RemainingAmount, models.CreditGrantColumns.UpdatedAt)); err != nil {
			return fmt.Errorf("failed to update failed grant %s: %w", failedGrant.TXHash, err)
		}

		// Record the settlement in operation grants
		grantDetail := &models.CreditOperationGrant{
			ID:            uuid.New().String(),
			AppName:       operation.AppName,
			ReferenceID:   operation.ReferenceID,
			OperationType: operation.OperationType,
			GrantID:       failedGrant.ID,
			AmountUsed:    amountSettled,
			CreatedAt:     null.TimeFrom(time.Now()),
		}

		if err := grantDetail.Insert(ctx, tx, boil.Infer()); err != nil {
			return fmt.Errorf("failed to record settlement detail: %w", err)
		}
	}

	return nil
}

func (r *Repository) getGrantsFromOperation(ctx context.Context, tx *sql.Tx, referenceID, appName string) ([]*models.CreditOperationGrant, *models.CreditOperation, error) {
	// Verify the operation exists
	operation, err := models.CreditOperations(
		models.CreditOperationWhere.ReferenceID.EQ(referenceID),
		models.CreditOperationWhere.AppName.EQ(appName),
		models.CreditOperationWhere.OperationType.EQ(OperationTypeDeduction),
	).One(ctx, tx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, fmt.Errorf("referenced deduction operation not found: %w", err)
		}
		return nil, nil, fmt.Errorf("failed to check if operation exists: %w", err)
	}

	operationGrants, err := models.CreditOperationGrants(
		models.CreditOperationGrantWhere.AppName.EQ(appName),
		models.CreditOperationGrantWhere.ReferenceID.EQ(referenceID),
		qm.Load(models.CreditOperationGrantRels.Grant),
	).All(ctx, tx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, fmt.Errorf("referenced grants from deduction operation not found: %w", err)
		}
		return nil, nil, fmt.Errorf("failed to get operation grants: %w", err)
	}

	return operationGrants, operation, nil
}

// expire on the same date in the next month
func getExpirationDate(mintTime time.Time) time.Time {
	return mintTime.UTC().AddDate(0, 1, 0)
}

// rollbackTx is a helper function to handle transaction rollback with error checking
func rollbackTx(ctx context.Context, tx *sql.Tx) {
	if tx == nil {
		return
	}
	if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		zerolog.Ctx(ctx).Error().Err(err).Msg("failed to rollback transaction")
	}
}
