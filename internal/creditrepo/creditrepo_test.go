package creditrepo

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/DIMO-Network/credit-tracker/models"
	"github.com/DIMO-Network/credit-tracker/tests"
	"github.com/ethereum/go-ethereum/common"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/volatiletech/sqlboiler/v4/boil"
	"github.com/volatiletech/sqlboiler/v4/queries/qm"
)

const (
	testAssetID        = "test-asset"
	testAPIEndpoint    = "test-api"
	testTXHash         = "test-tx-hash"
	defaultGrantAmount = int64(50_000)
)

func TestDeductCredits(t *testing.T) {
	t.Parallel() // Mark the main test as parallel

	dbContainer := tests.SetupTestContainer(t)
	dbContainer.TeardownIfLastTest(t)
	db := dbContainer.DB

	repo := New(db)
	ctx := context.Background()

	t.Run("successful deduction", func(t *testing.T) {
		t.Parallel()
		licenseID := "test-license-deduct-success"
		referenceID := uuid.New().String()
		// Setup: Create a confirmed grant
		grant := &models.CreditGrant{
			LicenseID:       licenseID,
			AssetDid:        testAssetID,
			InitialAmount:   defaultGrantAmount,
			RemainingAmount: defaultGrantAmount,
			Status:          GrantStatusConfirmed,
			ExpiresAt:       time.Now().Add(24 * time.Hour),
		}
		err := grant.Insert(ctx, db, boil.Infer())
		require.NoError(t, err)

		apiCost := int64(1)

		// Test: Deduct credits
		_, err = repo.DeductCredits(ctx, licenseID, testAssetID, uint64(apiCost), testAPIEndpoint, referenceID)
		require.NoError(t, err)

		// Verify: Check remaining amount
		grant, err = models.CreditGrants(
			models.CreditGrantWhere.LicenseID.EQ(licenseID),
			models.CreditGrantWhere.AssetDid.EQ(testAssetID),
		).One(ctx, db)
		require.NoError(t, err)
		assert.Equal(t, defaultGrantAmount-apiCost, grant.RemainingAmount)

		// Verify: Check operation record
		operation, err := models.CreditOperations(
			models.CreditOperationWhere.LicenseID.EQ(licenseID),
			models.CreditOperationWhere.AssetDid.EQ(testAssetID),
		).One(ctx, db)
		require.NoError(t, err)
		assert.Equal(t, OperationTypeDeduction, operation.OperationType)
		assert.Equal(t, int64(apiCost), operation.TotalAmount)
	})

	t.Run("successful deduction across multiple grants", func(t *testing.T) {
		t.Parallel()
		licenseID := "test-license-deduct-success-multiple"
		// Setup: Create a confirmed grant
		grant1 := &models.CreditGrant{
			LicenseID:       licenseID,
			AssetDid:        testAssetID,
			InitialAmount:   defaultGrantAmount,
			RemainingAmount: 5,
			Status:          GrantStatusConfirmed,
			ExpiresAt:       time.Now().Add(24 * time.Hour),
		}
		err := grant1.Insert(ctx, db, boil.Infer())
		require.NoError(t, err)

		grant2 := &models.CreditGrant{

			LicenseID:       licenseID,
			AssetDid:        testAssetID,
			InitialAmount:   defaultGrantAmount,
			RemainingAmount: defaultGrantAmount,
			Status:          GrantStatusPending,
			ExpiresAt:       time.Now().Add(48 * time.Hour),
		}
		err = grant2.Insert(ctx, db, boil.Infer())
		require.NoError(t, err)

		apiCost := int64(10)

		// Test: Deduct credits
		_, err = repo.DeductCredits(ctx, licenseID, testAssetID, uint64(apiCost), testAPIEndpoint, uuid.NewString())
		require.NoError(t, err)

		// Verify: Check remaining amount
		grant1, err = models.CreditGrants(
			models.CreditGrantWhere.ID.EQ(grant1.ID),
		).One(ctx, db)
		require.NoError(t, err)
		assert.Equal(t, int64(0), grant1.RemainingAmount)

		grant2, err = models.CreditGrants(
			models.CreditGrantWhere.ID.EQ(grant2.ID),
		).One(ctx, db)
		require.NoError(t, err)
		assert.Equal(t, int64(defaultGrantAmount-5), grant2.RemainingAmount)

		// Verify: Check operation record
		operation, err := models.CreditOperations(
			models.CreditOperationWhere.LicenseID.EQ(licenseID),
			models.CreditOperationWhere.AssetDid.EQ(testAssetID),
		).One(ctx, db)
		require.NoError(t, err)
		assert.Equal(t, OperationTypeDeduction, operation.OperationType)
		assert.Equal(t, int64(apiCost), operation.TotalAmount)
	})

	t.Run("insufficient credits", func(t *testing.T) {
		t.Parallel()
		licenseID := "test-license-deduct-insufficient"
		// Setup: Create a grant with 100 credits
		grant := &models.CreditGrant{
			LicenseID:       licenseID,
			AssetDid:        testAssetID,
			InitialAmount:   defaultGrantAmount,
			RemainingAmount: 0,
			Status:          "confirmed",
			ExpiresAt:       time.Now().Add(24 * time.Hour),
		}
		err := grant.Insert(ctx, db, boil.Infer())
		require.NoError(t, err)

		apiCost := int64(1)
		// Test: Try to deduct more than available
		_, err = repo.DeductCredits(ctx, licenseID, testAssetID, uint64(apiCost), testAPIEndpoint, uuid.NewString())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "insufficient credits")
	})

	t.Run("with outstanding debt", func(t *testing.T) {
		t.Parallel()
		licenseID := "test-license-deduct-debt"
		// Setup: Create a failed grant with debt
		grant := &models.CreditGrant{
			LicenseID:       licenseID,
			AssetDid:        testAssetID,
			InitialAmount:   defaultGrantAmount,
			RemainingAmount: defaultGrantAmount - 500, // 500 debt
			Status:          GrantStatusFailed,
			ExpiresAt:       time.Now().Add(24 * time.Hour),
		}
		err := grant.Insert(ctx, db, boil.Infer())
		require.NoError(t, err)

		apiCost := int64(1)
		// Test: Try to deduct credits
		_, err = repo.DeductCredits(ctx, licenseID, testAssetID, uint64(apiCost), testAPIEndpoint, uuid.NewString())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "outstanding debt")
	})

	t.Run("deduction with expired grant", func(t *testing.T) {
		t.Parallel()
		licenseID := "test-license-deduct-expired"
		// Setup: Create an expired grant and an active grant
		expiredGrant := &models.CreditGrant{
			LicenseID:       licenseID,
			AssetDid:        testAssetID,
			InitialAmount:   defaultGrantAmount,
			RemainingAmount: defaultGrantAmount,
			Status:          GrantStatusConfirmed,
			ExpiresAt:       time.Now().Add(-24 * time.Hour), // Expired
		}
		err := expiredGrant.Insert(ctx, db, boil.Infer())
		require.NoError(t, err)

		activeGrant := &models.CreditGrant{
			LicenseID:       licenseID,
			AssetDid:        testAssetID,
			InitialAmount:   defaultGrantAmount,
			RemainingAmount: defaultGrantAmount,
			Status:          GrantStatusConfirmed,
			ExpiresAt:       time.Now().Add(24 * time.Hour),
		}
		err = activeGrant.Insert(ctx, db, boil.Infer())
		require.NoError(t, err)

		apiCost := int64(10)

		// Test: Deduct credits
		_, err = repo.DeductCredits(ctx, licenseID, testAssetID, uint64(apiCost), testAPIEndpoint, uuid.NewString())
		require.NoError(t, err)

		// Verify: Expired grant should be untouched
		expiredGrant, err = models.CreditGrants(
			models.CreditGrantWhere.ID.EQ(expiredGrant.ID),
		).One(ctx, db)
		require.NoError(t, err)
		assert.Equal(t, defaultGrantAmount, expiredGrant.RemainingAmount)

		// Verify: Active grant should be deducted from
		activeGrant, err = models.CreditGrants(
			models.CreditGrantWhere.ID.EQ(activeGrant.ID),
		).One(ctx, db)
		require.NoError(t, err)
		assert.Equal(t, defaultGrantAmount-apiCost, activeGrant.RemainingAmount)
	})

	t.Run("deduction with multiple pending grants", func(t *testing.T) {
		t.Parallel()
		licenseID := "test-license-deduct-multiple-pending"
		// Setup: Create multiple pending grants
		pendingGrant1 := &models.CreditGrant{
			LicenseID:       licenseID,
			AssetDid:        testAssetID,
			InitialAmount:   defaultGrantAmount,
			RemainingAmount: defaultGrantAmount,
			Status:          GrantStatusPending,
			ExpiresAt:       time.Now().Add(24 * time.Hour),
		}
		err := pendingGrant1.Insert(ctx, db, boil.Infer())
		require.NoError(t, err)

		pendingGrant2 := &models.CreditGrant{
			LicenseID:       licenseID,
			AssetDid:        testAssetID,
			InitialAmount:   defaultGrantAmount,
			RemainingAmount: defaultGrantAmount,
			Status:          GrantStatusPending,
			ExpiresAt:       time.Now().Add(48 * time.Hour),
		}
		err = pendingGrant2.Insert(ctx, db, boil.Infer())
		require.NoError(t, err)

		apiCost := int64(10)

		// Test: Deduct credits
		_, err = repo.DeductCredits(ctx, licenseID, testAssetID, uint64(apiCost), testAPIEndpoint, uuid.NewString())
		require.NoError(t, err)

		// Verify: First pending grant should be used
		pendingGrant1, err = models.CreditGrants(
			models.CreditGrantWhere.ID.EQ(pendingGrant1.ID),
		).One(ctx, db)
		require.NoError(t, err)
		assert.Equal(t, defaultGrantAmount-apiCost, pendingGrant1.RemainingAmount)

		// Verify: Second pending grant should be untouched
		pendingGrant2, err = models.CreditGrants(
			models.CreditGrantWhere.ID.EQ(pendingGrant2.ID),
		).One(ctx, db)
		require.NoError(t, err)
		assert.Equal(t, defaultGrantAmount, pendingGrant2.RemainingAmount)
	})

	t.Run("deduction with zero amount", func(t *testing.T) {
		t.Parallel()
		licenseID := "test-license-deduct-zero"
		// Setup: Create a grant
		grant := &models.CreditGrant{
			LicenseID:       licenseID,
			AssetDid:        testAssetID,
			InitialAmount:   defaultGrantAmount,
			RemainingAmount: defaultGrantAmount,
			Status:          GrantStatusConfirmed,
			ExpiresAt:       time.Now().Add(24 * time.Hour),
		}
		err := grant.Insert(ctx, db, boil.Infer())
		require.NoError(t, err)

		// Test: Deduct zero credits
		_, err = repo.DeductCredits(ctx, licenseID, testAssetID, 0, testAPIEndpoint, uuid.NewString())
		require.NoError(t, err)

		// Verify: Grant should be unchanged
		grant, err = models.CreditGrants(
			models.CreditGrantWhere.ID.EQ(grant.ID),
		).One(ctx, db)
		require.NoError(t, err)
		assert.Equal(t, defaultGrantAmount, grant.RemainingAmount)

		// Verify: Operation should be recorded
		operation, err := models.CreditOperations(
			models.CreditOperationWhere.LicenseID.EQ(licenseID),
			models.CreditOperationWhere.AssetDid.EQ(testAssetID),
		).One(ctx, db)
		require.NoError(t, err)
		assert.Equal(t, OperationTypeDeduction, operation.OperationType)
		assert.Equal(t, int64(0), operation.TotalAmount)
	})

	t.Run("deduction with negative amount", func(t *testing.T) {
		t.Parallel()
		licenseID := "test-license-deduct-negative"
		// Setup: Create a grant
		grant := &models.CreditGrant{
			LicenseID:       licenseID,
			AssetDid:        testAssetID,
			InitialAmount:   defaultGrantAmount,
			RemainingAmount: defaultGrantAmount,
			Status:          GrantStatusConfirmed,
			ExpiresAt:       time.Now().Add(24 * time.Hour),
		}
		err := grant.Insert(ctx, db, boil.Infer())
		require.NoError(t, err)

		// Test: Try to deduct negative amount - this should be handled by the method itself
		// Since uint64 can't represent negative values, we'll test with 0 instead
		_, err = repo.DeductCredits(ctx, licenseID, testAssetID, 0, testAPIEndpoint, uuid.NewString())
		require.NoError(t, err)

		// Verify: Grant should be unchanged
		grant, err = models.CreditGrants(
			models.CreditGrantWhere.ID.EQ(grant.ID),
		).One(ctx, db)
		require.NoError(t, err)
		assert.Equal(t, defaultGrantAmount, grant.RemainingAmount)
	})

	t.Run("duplicate deduction", func(t *testing.T) {
		t.Parallel()
		licenseID := "test-license-deduct-duplicate"
		referenceID := uuid.New().String()
		// Setup: Create a confirmed grant
		grant := &models.CreditGrant{
			LicenseID:       licenseID,
			AssetDid:        testAssetID,
			InitialAmount:   defaultGrantAmount,
			RemainingAmount: defaultGrantAmount,
			Status:          GrantStatusConfirmed,
			ExpiresAt:       time.Now().Add(24 * time.Hour),
		}
		err := grant.Insert(ctx, db, boil.Infer())
		require.NoError(t, err)

		apiCost := int64(1)

		// Test: Deduct credits
		_, err = repo.DeductCredits(ctx, licenseID, testAssetID, uint64(apiCost), testAPIEndpoint, referenceID)
		require.NoError(t, err)

		// Verify: Check remaining amount
		grant, err = models.CreditGrants(
			models.CreditGrantWhere.ID.EQ(grant.ID),
		).One(ctx, db)
		require.NoError(t, err)
		assert.Equal(t, defaultGrantAmount-apiCost, grant.RemainingAmount)

		_, err = repo.DeductCredits(ctx, licenseID, testAssetID, uint64(apiCost), testAPIEndpoint, referenceID)
		require.Error(t, err)
		var pqErr *pq.Error
		if errors.As(err, &pqErr) {
			fmt.Printf("%s: As PostgreSQL error: %#v\n", licenseID, pqErr)
		} else {
			fmt.Printf("%s: PostgreSQL error: %#v\n", licenseID, err)
		}

		// Verify: Grant should be unchanged
		grant, err = models.CreditGrants(
			models.CreditGrantWhere.ID.EQ(grant.ID),
		).One(ctx, db)
		require.NoError(t, err)
		assert.Equal(t, defaultGrantAmount-apiCost, grant.RemainingAmount)
	})
}

func TestRefundCredits(t *testing.T) {
	t.Parallel()
	dbContainer := tests.SetupTestContainer(t)
	dbContainer.TeardownIfLastTest(t)
	db := dbContainer.DB

	repo := New(db)
	ctx := context.Background()

	t.Run("successful refund", func(t *testing.T) {
		t.Parallel()
		licenseID := "test-license-refund-success"
		// Setup: Create a grant with some used credits
		grant := &models.CreditGrant{
			LicenseID:       licenseID,
			AssetDid:        testAssetID,
			InitialAmount:   defaultGrantAmount,
			RemainingAmount: defaultGrantAmount,
			Status:          GrantStatusConfirmed,
			ExpiresAt:       time.Now().Add(24 * time.Hour),
		}
		err := grant.Insert(ctx, db, boil.Infer())
		require.NoError(t, err)

		referenceID := uuid.NewString()
		_, err = repo.DeductCredits(ctx, licenseID, testAssetID, uint64(100), testAPIEndpoint, referenceID)
		require.NoError(t, err)

		grant, err = models.CreditGrants(
			models.CreditGrantWhere.ID.EQ(grant.ID),
		).One(ctx, db)
		require.NoError(t, err)
		require.Equal(t, defaultGrantAmount-100, grant.RemainingAmount)

		// Test: Refund credits
		_, err = repo.RefundCredits(ctx, testAPIEndpoint, referenceID)
		require.NoError(t, err)

		// Verify: Check remaining amount
		grant, err = models.CreditGrants(
			models.CreditGrantWhere.ID.EQ(grant.ID),
		).One(ctx, db)
		require.NoError(t, err)
		require.Equal(t, defaultGrantAmount, grant.RemainingAmount)

		// Verify: Check operation record
		operation, err := models.CreditOperations(
			models.CreditOperationWhere.ReferenceID.EQ(referenceID),
			models.CreditOperationWhere.AppName.EQ(testAPIEndpoint),
			qm.OrderBy(models.CreditOperationColumns.CreatedAt+" DESC"),
		).One(ctx, db)
		require.NoError(t, err)
		assert.Equal(t, OperationTypeRefund, operation.OperationType)
		assert.Equal(t, int64(100), operation.TotalAmount)
		assert.Equal(t, licenseID, operation.LicenseID)
		assert.Equal(t, testAssetID, operation.AssetDid)

		operationGrants, err := models.CreditOperationGrants(
			models.CreditOperationGrantWhere.ReferenceID.EQ(operation.ReferenceID),
			models.CreditOperationGrantWhere.AppName.EQ(operation.AppName),
			models.CreditOperationGrantWhere.OperationType.EQ(OperationTypeRefund),
			qm.Load(models.CreditOperationGrantRels.Grant),
		).All(ctx, db)
		require.NoError(t, err)
		require.Equal(t, 1, len(operationGrants))
		require.Equal(t, grant.ID, operationGrants[0].GetGrant().ID)
		require.Equal(t, int64(100), operationGrants[0].AmountUsed)
	})

	t.Run("refund with multiple grants", func(t *testing.T) {
		t.Parallel()
		licenseID := "test-license-refund-multiple"
		// Setup: Create two grants with different usage
		grant1 := &models.CreditGrant{
			LicenseID:       licenseID,
			AssetDid:        testAssetID,
			InitialAmount:   defaultGrantAmount,
			RemainingAmount: 5,
			Status:          GrantStatusConfirmed,
			ExpiresAt:       time.Now().Add(24 * time.Hour),
		}
		err := grant1.Insert(ctx, db, boil.Infer())
		require.NoError(t, err)

		grant2 := &models.CreditGrant{
			LicenseID:       licenseID,
			AssetDid:        testAssetID,
			InitialAmount:   defaultGrantAmount,
			RemainingAmount: defaultGrantAmount,
			Status:          GrantStatusConfirmed,
			ExpiresAt:       time.Now().Add(48 * time.Hour),
		}
		err = grant2.Insert(ctx, db, boil.Infer())
		require.NoError(t, err)

		referenceID := uuid.NewString()
		_, err = repo.DeductCredits(ctx, licenseID, testAssetID, uint64(10), testAPIEndpoint, referenceID)
		require.NoError(t, err)

		grant1, err = models.CreditGrants(
			models.CreditGrantWhere.ID.EQ(grant1.ID),
		).One(ctx, db)
		require.NoError(t, err)
		assert.Equal(t, int64(0), grant1.RemainingAmount)

		grant2, err = models.CreditGrants(
			models.CreditGrantWhere.ID.EQ(grant2.ID),
		).One(ctx, db)
		require.NoError(t, err)
		assert.Equal(t, defaultGrantAmount-5, grant2.RemainingAmount)

		// Test: Refund credits
		_, err = repo.RefundCredits(ctx, testAPIEndpoint, referenceID)
		require.NoError(t, err)

		// Verify: First grant should be refunded 5 credits
		grant1, err = models.CreditGrants(
			models.CreditGrantWhere.ID.EQ(grant1.ID),
		).One(ctx, db)
		require.NoError(t, err)
		assert.Equal(t, int64(5), grant1.RemainingAmount)

		// Verify: Second grant should be refunded 5 credits
		grant2, err = models.CreditGrants(
			models.CreditGrantWhere.ID.EQ(grant2.ID),
		).One(ctx, db)
		require.NoError(t, err)
		assert.Equal(t, defaultGrantAmount, grant2.RemainingAmount)

		operation, err := models.CreditOperations(
			models.CreditOperationWhere.ReferenceID.EQ(referenceID),
			models.CreditOperationWhere.AppName.EQ(testAPIEndpoint),
			qm.OrderBy(models.CreditOperationColumns.CreatedAt+" DESC"),
		).All(ctx, db)
		require.NoError(t, err)
		require.Equal(t, 2, len(operation))
		assert.Equal(t, OperationTypeRefund, operation[0].OperationType)
		assert.Equal(t, OperationTypeDeduction, operation[1].OperationType)

		operationGrants, err := models.CreditOperationGrants(
			models.CreditOperationGrantWhere.ReferenceID.EQ(operation[0].ReferenceID),
			models.CreditOperationGrantWhere.AppName.EQ(operation[0].AppName),
			models.CreditOperationGrantWhere.OperationType.EQ(OperationTypeRefund),
			qm.Load(models.CreditOperationGrantRels.Grant),
		).All(ctx, db)
		require.NoError(t, err)
		require.Equal(t, 2, len(operationGrants))
		assert.Equal(t, grant1.ID, operationGrants[0].GetGrant().ID)
		assert.Equal(t, int64(5), operationGrants[0].AmountUsed)
		assert.Equal(t, grant2.ID, operationGrants[1].GetGrant().ID)
		assert.Equal(t, int64(5), operationGrants[1].AmountUsed)
	})

	t.Run("refund with expired grant", func(t *testing.T) {
		t.Parallel()
		licenseID := "test-license-refund-expired"
		// Setup: Create an expired grant with used credits
		grant := &models.CreditGrant{
			LicenseID:       licenseID,
			AssetDid:        testAssetID,
			InitialAmount:   defaultGrantAmount,
			RemainingAmount: defaultGrantAmount,
			Status:          GrantStatusConfirmed,
			ExpiresAt:       time.Now().Add(1 * time.Second), // Expired
		}
		err := grant.Insert(ctx, db, boil.Infer())
		require.NoError(t, err)

		referenceID := uuid.NewString()
		_, err = repo.DeductCredits(ctx, licenseID, testAssetID, uint64(100), testAPIEndpoint, referenceID)
		require.NoError(t, err)

		grant, err = models.CreditGrants(
			models.CreditGrantWhere.ID.EQ(grant.ID),
		).One(ctx, db)
		require.NoError(t, err)
		assert.Equal(t, defaultGrantAmount-100, grant.RemainingAmount)

		time.Sleep(1 * time.Second)
		tx, err := db.BeginTx(ctx, nil)
		require.NoError(t, err)
		preRefundBalance, err := repo.calculateBalance(ctx, tx, licenseID, testAssetID)
		require.NoError(t, err)

		// Test: Refund credits
		_, err = repo.RefundCredits(ctx, testAPIEndpoint, referenceID)
		require.NoError(t, err)

		postRefundBalance, err := repo.calculateBalance(ctx, tx, licenseID, testAssetID)
		require.NoError(t, err)
		require.Equal(t, preRefundBalance, postRefundBalance)
		err = tx.Commit()
		require.NoError(t, err)

		grant, err = models.CreditGrants(
			models.CreditGrantWhere.ID.EQ(grant.ID),
		).One(ctx, db)
		require.NoError(t, err)
		assert.Equal(t, defaultGrantAmount, grant.RemainingAmount)

		// Verify: Operation should be recorded
		operation, err := models.CreditOperations(
			models.CreditOperationWhere.ReferenceID.EQ(referenceID),
			models.CreditOperationWhere.AppName.EQ(testAPIEndpoint),
			qm.OrderBy(models.CreditOperationColumns.CreatedAt+" DESC"),
		).One(ctx, db)
		require.NoError(t, err)
		assert.Equal(t, OperationTypeRefund, operation.OperationType)
		assert.Equal(t, int64(100), operation.TotalAmount)
	})

	t.Run("refund with zero amount", func(t *testing.T) {
		t.Parallel()
		licenseID := "test-license-refund-zero"
		// Setup: Create a grant with used credits
		grant := &models.CreditGrant{
			LicenseID:       licenseID,
			AssetDid:        testAssetID,
			InitialAmount:   defaultGrantAmount,
			RemainingAmount: defaultGrantAmount,
			Status:          GrantStatusConfirmed,
			ExpiresAt:       time.Now().Add(24 * time.Hour),
		}
		err := grant.Insert(ctx, db, boil.Infer())
		require.NoError(t, err)

		referenceID := uuid.NewString()
		_, err = repo.DeductCredits(ctx, licenseID, testAssetID, uint64(0), testAPIEndpoint, referenceID)
		require.NoError(t, err)

		grant, err = models.CreditGrants(
			models.CreditGrantWhere.ID.EQ(grant.ID),
		).One(ctx, db)
		require.NoError(t, err)
		assert.Equal(t, defaultGrantAmount, grant.RemainingAmount)

		// Test: Refund zero credits
		_, err = repo.RefundCredits(ctx, testAPIEndpoint, referenceID)
		require.NoError(t, err)

		// Verify: Grant should be unchanged
		grant, err = models.CreditGrants(
			models.CreditGrantWhere.ID.EQ(grant.ID),
		).One(ctx, db)
		require.NoError(t, err)
		assert.Equal(t, defaultGrantAmount, grant.RemainingAmount)

		// Verify: Operation should be recorded
		operation, err := models.CreditOperations(
			models.CreditOperationWhere.LicenseID.EQ(licenseID),
			models.CreditOperationWhere.AssetDid.EQ(testAssetID),
			models.CreditOperationWhere.OperationType.EQ(OperationTypeRefund),
		).One(ctx, db)
		require.NoError(t, err)
		assert.Equal(t, OperationTypeRefund, operation.OperationType)
		assert.Equal(t, int64(0), operation.TotalAmount)

	})

	t.Run("duplicate refund", func(t *testing.T) {
		t.Parallel()
		licenseID := "test-license-refund-duplicate"
		// Setup: Create a grant with used credits
		grant := &models.CreditGrant{
			LicenseID:       licenseID,
			AssetDid:        testAssetID,
			InitialAmount:   defaultGrantAmount,
			RemainingAmount: defaultGrantAmount,
			Status:          GrantStatusConfirmed,
			ExpiresAt:       time.Now().Add(24 * time.Hour),
		}
		err := grant.Insert(ctx, db, boil.Infer())
		require.NoError(t, err)

		referenceID := uuid.NewString()
		_, err = repo.DeductCredits(ctx, licenseID, testAssetID, uint64(100), testAPIEndpoint, referenceID)
		require.NoError(t, err)

		grant, err = models.CreditGrants(
			models.CreditGrantWhere.ID.EQ(grant.ID),
		).One(ctx, db)
		require.NoError(t, err)
		assert.Equal(t, defaultGrantAmount-100, grant.RemainingAmount)

		_, err = repo.RefundCredits(ctx, testAPIEndpoint, referenceID)
		require.NoError(t, err)

		grant, err = models.CreditGrants(
			models.CreditGrantWhere.ID.EQ(grant.ID),
		).One(ctx, db)
		require.NoError(t, err)
		assert.Equal(t, defaultGrantAmount, grant.RemainingAmount)

		_, err = repo.RefundCredits(ctx, testAPIEndpoint, referenceID)
		require.Error(t, err)
		var pqErr *pq.Error
		if errors.As(err, &pqErr) {
			fmt.Printf("%s: As PostgreSQL error: %#v\n", licenseID, pqErr)
		} else {
			fmt.Printf("%s: PostgreSQL error: %#v\n", licenseID, err)
		}

		grant, err = models.CreditGrants(
			models.CreditGrantWhere.ID.EQ(grant.ID),
		).One(ctx, db)
		require.NoError(t, err)
		assert.Equal(t, defaultGrantAmount, grant.RemainingAmount)
	})
}

func TestCreateGrant(t *testing.T) {
	t.Parallel()
	dbContainer := tests.SetupTestContainer(t)
	dbContainer.TeardownIfLastTest(t)
	db := dbContainer.DB

	repo := New(db)
	ctx := context.Background()

	t.Run("successful grant creation", func(t *testing.T) {
		t.Parallel()
		licenseID := "test-license-grant-create"
		// Test: Create a new grant
		grant, err := repo.CreateGrant(ctx, licenseID, testAssetID, uint64(defaultGrantAmount), time.Now())
		require.NoError(t, err)
		_, err = repo.UpdateGrantTxHash(ctx, grant, testTXHash)
		require.NoError(t, err)

		// Verify: Check grant record

		grant, err = models.CreditGrants(
			models.CreditGrantWhere.LicenseID.EQ(licenseID),
			models.CreditGrantWhere.AssetDid.EQ(testAssetID),
		).One(ctx, db)
		require.NoError(t, err)
		assert.Equal(t, GrantStatusPending, grant.Status)
		assert.Equal(t, defaultGrantAmount, grant.InitialAmount)
		assert.Equal(t, defaultGrantAmount, grant.RemainingAmount)
		assert.Equal(t, testTXHash, grant.TXHash)

		// Verify: Check operation record
		operation, err := models.CreditOperations(
			models.CreditOperationWhere.ReferenceID.EQ(grant.ID),
			models.CreditOperationWhere.AppName.EQ("credit_tracker"),
		).One(ctx, db)
		require.NoError(t, err)
		assert.Equal(t, OperationTypeGrantPurchase, operation.OperationType)
		assert.Equal(t, defaultGrantAmount, operation.TotalAmount)

		operationGrants, err := models.CreditOperationGrants(
			models.CreditOperationGrantWhere.ReferenceID.EQ(operation.ReferenceID),
			models.CreditOperationGrantWhere.AppName.EQ(operation.AppName),
			qm.Load(models.CreditOperationGrantRels.Grant),
		).All(ctx, db)
		require.NoError(t, err)
		require.Equal(t, 1, len(operationGrants))
		require.Equal(t, grant.ID, operationGrants[0].GetGrant().ID)
	})

	t.Run("create grant with existing debt", func(t *testing.T) {
		t.Parallel()
		licenseID := "test-license-grant-debt"
		localTextTXHash := common.BytesToAddress([]byte(licenseID))
		// Setup: Create a failed grant with debt
		failedGrant := &models.CreditGrant{
			LicenseID:       licenseID,
			AssetDid:        testAssetID,
			InitialAmount:   defaultGrantAmount,
			RemainingAmount: defaultGrantAmount - 100, // 100 debt
			Status:          GrantStatusFailed,
			ExpiresAt:       time.Now().Add(24 * time.Hour),
		}
		err := failedGrant.Insert(ctx, db, boil.Infer())
		require.NoError(t, err)

		// Test: Create a new grant
		grant, err := repo.CreateGrant(ctx, licenseID, testAssetID, uint64(defaultGrantAmount), time.Now())
		require.NoError(t, err)
		_, err = repo.UpdateGrantTxHash(ctx, grant, localTextTXHash.Hex())
		require.NoError(t, err)

		// Verify: Failed grant should be settled
		failedGrant, err = models.CreditGrants(
			models.CreditGrantWhere.ID.EQ(failedGrant.ID),
		).One(ctx, db)
		require.NoError(t, err)
		assert.Equal(t, defaultGrantAmount, failedGrant.RemainingAmount)

		// Verify: New grant should be created with correct amount
		newGrant, err := models.CreditGrants(
			models.CreditGrantWhere.TXHash.EQ(localTextTXHash.Hex()),
		).One(ctx, db)
		require.NoError(t, err)
		assert.Equal(t, GrantStatusPending, newGrant.Status)
		assert.Equal(t, defaultGrantAmount, newGrant.InitialAmount)
		assert.Equal(t, defaultGrantAmount-100, newGrant.RemainingAmount)
	})

	t.Run("create grant with zero amount", func(t *testing.T) {
		t.Parallel()
		licenseID := "test-license-grant-zero"
		// Test: Create a grant with zero amount
		_, err := repo.CreateGrant(ctx, licenseID, testAssetID, 0, time.Now())
		require.Error(t, err)

		grants, err := models.CreditGrants(
			models.CreditGrantWhere.LicenseID.EQ(licenseID),
		).All(ctx, db)
		require.NoError(t, err)
		require.Equal(t, 0, len(grants))
	})

	t.Run("create grant with negative amount", func(t *testing.T) {
		t.Parallel()
		licenseID := "test-license-grant-negative"
		// Test: Try to create a grant with negative amount - this should be handled by the method itself
		// Since uint64 can't represent negative values, we'll test with 0 instead
		_, err := repo.CreateGrant(ctx, licenseID, testAssetID, 0, time.Now())
		require.Error(t, err)

		grants, err := models.CreditGrants(
			models.CreditGrantWhere.LicenseID.EQ(licenseID),
		).All(ctx, db)
		require.NoError(t, err)
		require.Equal(t, 0, len(grants))
	})
}

func TestConfirmGrant(t *testing.T) {
	t.Parallel()
	dbContainer := tests.SetupTestContainer(t)
	dbContainer.TeardownIfLastTest(t)
	db := dbContainer.DB

	repo := New(db)
	ctx := context.Background()

	t.Run("successful grant confirmation", func(t *testing.T) {
		t.Parallel()
		licenseID := "test-license-grant-confirm"
		// Setup: Create a pending grant

		localTextTXHash := common.BytesToAddress([]byte(licenseID))
		grant, err := repo.CreateGrant(ctx, licenseID, testAssetID, uint64(defaultGrantAmount), time.Now())
		require.NoError(t, err)
		_, err = repo.UpdateGrantTxHash(ctx, grant, localTextTXHash.Hex())
		require.NoError(t, err)

		// Verify: Check grant was created
		_, err = models.CreditGrants(
			models.CreditGrantWhere.TXHash.EQ(localTextTXHash.Hex()),
		).One(ctx, db)
		require.NoError(t, err)

		// Test: Confirm the grant
		_, err = repo.ConfirmGrant(ctx, licenseID, testAssetID, localTextTXHash.Hex(), 100, uint64(defaultGrantAmount), time.Now())
		require.NoError(t, err)

		// Verify: Check grant status
		grants, err := models.CreditGrants(
			models.CreditGrantWhere.TXHash.EQ(localTextTXHash.Hex()),
		).All(ctx, db)
		require.NoError(t, err)
		require.Equal(t, 1, len(grants))
		assert.Equal(t, GrantStatusConfirmed, grants[0].Status)
		assert.Equal(t, int(100), grants[0].LogIndex.Int)
		assert.Equal(t, defaultGrantAmount, grants[0].RemainingAmount)
		assert.Equal(t, defaultGrantAmount, grants[0].InitialAmount)
		assert.Equal(t, licenseID, grants[0].LicenseID)
		assert.Equal(t, testAssetID, grants[0].AssetDid)

		// Verify: Check operation record
		operation, err := models.CreditOperations(
			models.CreditOperationWhere.ReferenceID.EQ(grants[0].ID),
			models.CreditOperationWhere.AppName.EQ("credit_tracker"),
		).One(ctx, db)
		require.NoError(t, err)
		assert.Equal(t, OperationTypeGrantConfirm, operation.OperationType)
		assert.Equal(t, defaultGrantAmount, operation.TotalAmount)

		operationGrants, err := models.CreditOperationGrants(
			models.CreditOperationGrantWhere.ReferenceID.EQ(operation.ReferenceID),
			models.CreditOperationGrantWhere.AppName.EQ(operation.AppName),
			qm.OrderBy(models.CreditOperationGrantColumns.CreatedAt+" DESC"),
			qm.Load(models.CreditOperationGrantRels.Grant),
		).All(ctx, db)
		require.NoError(t, err)
		require.Equal(t, 2, len(operationGrants))
		require.Equal(t, grants[0].ID, operationGrants[0].GetGrant().ID)
		require.Equal(t, defaultGrantAmount, operationGrants[0].AmountUsed)
		require.Equal(t, OperationTypeGrantConfirm, operationGrants[0].OperationType)
		require.Equal(t, defaultGrantAmount, operationGrants[1].AmountUsed)
		require.Equal(t, OperationTypeGrantPurchase, operationGrants[1].OperationType)
	})

	t.Run("confirm non-existent grant", func(t *testing.T) {
		t.Parallel()
		licenseID := "test-license-grant-confirm-new"
		localTextTXHash := common.BytesToAddress([]byte(licenseID))
		// Test: Confirm a non-existent grant
		mintTime := time.Now()
		_, err := repo.ConfirmGrant(ctx, licenseID, testAssetID, localTextTXHash.Hex(), 1, uint64(defaultGrantAmount), mintTime)
		require.NoError(t, err) // Should create a new grant

		// Verify: Check grant was created and confirmed
		grant, err := models.CreditGrants(
			models.CreditGrantWhere.TXHash.EQ(localTextTXHash.Hex()),
		).One(ctx, db)
		require.NoError(t, err)
		assert.Equal(t, GrantStatusConfirmed, grant.Status)
		assert.Equal(t, defaultGrantAmount, grant.InitialAmount)
		assert.Equal(t, GrantStatusConfirmed, grant.Status)
		assert.Equal(t, 1, grant.LogIndex.Int)
		assert.Equal(t, defaultGrantAmount, grant.InitialAmount)
		assert.Equal(t, defaultGrantAmount, grant.RemainingAmount)
		assert.Equal(t, getExpirationDate(mintTime).Truncate(time.Millisecond), grant.ExpiresAt.UTC().Truncate(time.Millisecond))

		// Verify: Check operation record
		operation, err := models.CreditOperations(
			models.CreditOperationWhere.ReferenceID.EQ(grant.ID),
			models.CreditOperationWhere.AppName.EQ("credit_tracker"),
		).One(ctx, db)
		require.NoError(t, err)
		assert.Equal(t, OperationTypeGrantConfirm, operation.OperationType)

		operationGrants, err := models.CreditOperationGrants(
			models.CreditOperationGrantWhere.ReferenceID.EQ(operation.ReferenceID),
			models.CreditOperationGrantWhere.AppName.EQ(operation.AppName),
			qm.OrderBy(models.CreditOperationGrantColumns.CreatedAt+" DESC"),
			qm.Load(models.CreditOperationGrantRels.Grant),
		).All(ctx, db)
		require.NoError(t, err)
		require.Equal(t, 1, len(operationGrants))
		assert.Equal(t, defaultGrantAmount, operationGrants[0].AmountUsed)
		assert.Equal(t, OperationTypeGrantConfirm, operationGrants[0].OperationType)
	})

	t.Run("confirm already confirmed grant", func(t *testing.T) {
		t.Parallel()
		licenseID := "test-license-grant-confirm-twice"
		localTextTXHash := common.BytesToAddress([]byte(licenseID))
		// Test: Confirm a non-existent grant
		mintTime := time.Now()
		_, err := repo.ConfirmGrant(ctx, licenseID, testAssetID, localTextTXHash.Hex(), 1, uint64(defaultGrantAmount), mintTime)
		require.NoError(t, err) // Should create a new grant

		// Verify: Check grant was created and confirmed
		grant, err := models.CreditGrants(
			models.CreditGrantWhere.TXHash.EQ(localTextTXHash.Hex()),
		).One(ctx, db)
		require.NoError(t, err)
		assert.Equal(t, GrantStatusConfirmed, grant.Status)
		assert.Equal(t, defaultGrantAmount, grant.InitialAmount)
		assert.Equal(t, GrantStatusConfirmed, grant.Status)
		assert.Equal(t, 1, grant.LogIndex.Int)
		assert.Equal(t, defaultGrantAmount, grant.InitialAmount)
		assert.Equal(t, defaultGrantAmount, grant.RemainingAmount)
		assert.Equal(t, getExpirationDate(mintTime).Truncate(time.Millisecond), grant.ExpiresAt.UTC().Truncate(time.Millisecond))

		// Test: Try to confirm again
		_, err = repo.ConfirmGrant(ctx, licenseID, testAssetID, localTextTXHash.Hex(), 1, uint64(defaultGrantAmount), mintTime)
		require.Error(t, err)
		// Verify: Grant should be updated with new log index
		grants, err := models.CreditGrants(
			models.CreditGrantWhere.TXHash.EQ(localTextTXHash.Hex()),
		).All(ctx, db)
		require.NoError(t, err)
		require.Equal(t, 1, len(grants))
		assert.Equal(t, GrantStatusConfirmed, grants[0].Status)
	})

}

func TestFIFOOrdering(t *testing.T) {
	t.Parallel()
	dbContainer := tests.SetupTestContainer(t)
	dbContainer.TeardownIfLastTest(t)
	db := dbContainer.DB

	repo := New(db)
	ctx := context.Background()

	t.Run("deduction follows FIFO order", func(t *testing.T) {
		t.Parallel()
		licenseID := "test-license-fifo"
		localTextTXHash := common.BytesToAddress([]byte(licenseID))
		localTextTXHash2 := common.BytesToAddress([]byte(licenseID + "2"))

		_, err := repo.ConfirmGrant(ctx, licenseID, testAssetID, localTextTXHash.Hex(), 1, uint64(defaultGrantAmount), time.Now().Add(-time.Hour))
		require.NoError(t, err)
		_, err = repo.ConfirmGrant(ctx, licenseID, testAssetID, localTextTXHash2.Hex(), 2, uint64(defaultGrantAmount), time.Now())
		require.NoError(t, err)

		apiCost := int64(10)

		// Test: Deduct credits
		_, err = repo.DeductCredits(ctx, licenseID, testAssetID, uint64(apiCost), testAPIEndpoint, uuid.NewString())
		require.NoError(t, err)

		// Verify: Old grant should be deducted from first
		oldGrant, err := models.CreditGrants(
			models.CreditGrantWhere.TXHash.EQ(localTextTXHash.Hex()),
		).One(ctx, db)
		require.NoError(t, err)
		assert.Equal(t, defaultGrantAmount-apiCost, oldGrant.RemainingAmount)

		// Verify: New grant should be untouched
		newGrant, err := models.CreditGrants(
			models.CreditGrantWhere.TXHash.EQ(localTextTXHash2.Hex()),
		).One(ctx, db)
		require.NoError(t, err)
		assert.Equal(t, defaultGrantAmount, newGrant.RemainingAmount)
	})

	t.Run("FIFO with multiple grants", func(t *testing.T) {
		t.Parallel()
		licenseID := "test-license-fifo-multiple"
		localTextTXHash := common.BytesToAddress([]byte(licenseID))
		localTextTXHash2 := common.BytesToAddress([]byte(licenseID + "2"))
		localTextTXHash3 := common.BytesToAddress([]byte(licenseID + "3"))

		// Setup: Create three grants with different expiration dates
		_, err := repo.ConfirmGrant(ctx, licenseID, testAssetID, localTextTXHash.Hex(), 1, uint64(defaultGrantAmount), time.Now().Add(-time.Hour*2))
		require.NoError(t, err)
		_, err = repo.ConfirmGrant(ctx, licenseID, testAssetID, localTextTXHash2.Hex(), 2, uint64(defaultGrantAmount), time.Now().Add(-time.Hour))
		require.NoError(t, err)
		_, err = repo.ConfirmGrant(ctx, licenseID, testAssetID, localTextTXHash3.Hex(), 3, uint64(defaultGrantAmount), time.Now())
		require.NoError(t, err)

		apiCost := int64(defaultGrantAmount + 100) // More than one grant's worth

		// Test: Deduct credits
		_, err = repo.DeductCredits(ctx, licenseID, testAssetID, uint64(apiCost), testAPIEndpoint, uuid.NewString())
		require.NoError(t, err)

		// Verify: First grant should be fully depleted
		grant1, err := models.CreditGrants(
			models.CreditGrantWhere.TXHash.EQ(localTextTXHash.Hex()),
		).One(ctx, db)
		require.NoError(t, err)
		assert.Equal(t, int64(0), grant1.RemainingAmount)

		// Verify: Second grant should be partially depleted
		grant2, err := models.CreditGrants(
			models.CreditGrantWhere.TXHash.EQ(localTextTXHash2.Hex()),
		).One(ctx, db)
		require.NoError(t, err)
		assert.Equal(t, defaultGrantAmount-100, grant2.RemainingAmount)

		// Verify: Third grant should be untouched
		grant3, err := models.CreditGrants(
			models.CreditGrantWhere.TXHash.EQ(localTextTXHash3.Hex()),
		).One(ctx, db)
		require.NoError(t, err)
		assert.Equal(t, defaultGrantAmount, grant3.RemainingAmount)
	})
}

func TestConcurrentOperations(t *testing.T) {
	t.Parallel()
	dbContainer := tests.SetupTestContainer(t)
	dbContainer.TeardownIfLastTest(t)
	db := dbContainer.DB

	repo := New(db)
	ctx := context.Background()

	t.Run("concurrent deductions", func(t *testing.T) {
		t.Parallel()
		licenseID := "test-license-concurrent"
		localTextTXHash := common.BytesToAddress([]byte(licenseID))
		// Setup: Create a grant with sufficient credits
		_, err := repo.ConfirmGrant(ctx, licenseID, testAssetID, localTextTXHash.Hex(), 1, uint64(defaultGrantAmount), time.Now())
		require.NoError(t, err)

		// Test: Perform concurrent deductions
		done := make(chan error, 2)
		go func() {
			_, err := repo.DeductCredits(ctx, licenseID, testAssetID, 300, testAPIEndpoint, uuid.NewString())
			done <- err
		}()
		go func() {
			_, err := repo.DeductCredits(ctx, licenseID, testAssetID, 400, testAPIEndpoint, uuid.NewString())
			done <- err
		}()

		// Wait for both operations to complete
		err1 := <-done
		err2 := <-done

		// Verify: At least one operation should succeed
		assert.True(t, err1 == nil || err2 == nil)

		// Verify: Total deductions should not exceed available credits
		grant, err := models.CreditGrants(
			models.CreditGrantWhere.TXHash.EQ(localTextTXHash.Hex()),
		).One(ctx, db)
		require.NoError(t, err)
		assert.Equal(t, defaultGrantAmount-700, grant.RemainingAmount)
	})

	t.Run("concurrent refunds", func(t *testing.T) {
		t.Parallel()
		licenseID := "test-license-concurrent-refund"
		localTextTXHash := common.BytesToAddress([]byte(licenseID))

		_, err := repo.ConfirmGrant(ctx, licenseID, testAssetID, localTextTXHash.Hex(), 1, uint64(defaultGrantAmount), time.Now())
		require.NoError(t, err)

		referenceID1 := uuid.NewString()
		_, err = repo.DeductCredits(ctx, licenseID, testAssetID, 1000, testAPIEndpoint, referenceID1)
		require.NoError(t, err)

		referenceID2 := uuid.NewString()
		_, err = repo.DeductCredits(ctx, licenseID, testAssetID, 200, testAPIEndpoint, referenceID2)
		require.NoError(t, err)

		// Test: Perform concurrent refunds
		done := make(chan error, 2)
		go func() {
			_, err := repo.RefundCredits(ctx, testAPIEndpoint, referenceID1)
			done <- err
		}()
		go func() {
			_, err := repo.RefundCredits(ctx, testAPIEndpoint, referenceID2)
			done <- err
		}()

		// Wait for both operations to complete
		err1 := <-done
		err2 := <-done

		// Verify: Both operations should succeed
		require.NoError(t, err1)
		require.NoError(t, err2)

		// Verify: Total refunds should not exceed initial amount
		grants, err := models.CreditGrants(
			models.CreditGrantWhere.TXHash.EQ(localTextTXHash.Hex()),
		).All(ctx, db)
		require.NoError(t, err)
		require.Equal(t, 1, len(grants))
		assert.LessOrEqual(t, grants[0].RemainingAmount, defaultGrantAmount)
	})

	t.Run("concurrent grant confirmations", func(t *testing.T) {
		t.Parallel()
		licenseID := "test-license-concurrent-confirm"
		localTextTXHash := common.BytesToAddress([]byte(licenseID))

		// Test: Perform concurrent confirmations
		done := make(chan error, 2)
		go func() {
			_, err := repo.ConfirmGrant(ctx, licenseID, testAssetID, localTextTXHash.Hex(), 1, uint64(defaultGrantAmount), time.Now())
			done <- err
		}()
		go func() {
			_, err := repo.ConfirmGrant(ctx, licenseID, testAssetID, localTextTXHash.Hex(), 2, uint64(defaultGrantAmount), time.Now())
			done <- err
		}()

		// Wait for both operations to complete
		err1 := <-done
		err2 := <-done

		// Verify: Both operations should succeed
		require.NoError(t, err1)
		require.NoError(t, err2)

		// Verify: Grant should be confirmed with the latest log index
		grants, err := models.CreditGrants(
			models.CreditGrantWhere.TXHash.EQ(localTextTXHash.Hex()),
			qm.OrderBy(models.CreditGrantColumns.LogIndex+" DESC"),
		).All(ctx, db)
		require.NoError(t, err)
		require.Equal(t, 2, len(grants))
		assert.Equal(t, GrantStatusConfirmed, grants[0].Status)
		assert.Equal(t, 2, grants[0].LogIndex.Int)
		assert.Equal(t, GrantStatusConfirmed, grants[1].Status)
		assert.Equal(t, 1, grants[1].LogIndex.Int)
	})
}
