package creditrepo

import (
	"context"
	"testing"
	"time"

	"github.com/DIMO-Network/credit-tracker/tests"
	"github.com/ethereum/go-ethereum/common"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetLicenseUsageReport(t *testing.T) {
	t.Parallel()
	dbContainer := tests.SetupTestContainer(t)
	dbContainer.TeardownIfLastTest(t)
	db := dbContainer.DB

	repo := New(db)
	ctx := context.Background()

	t.Run("basic usage report with single asset", func(t *testing.T) {
		t.Parallel()
		licenseID := "test-license-report-basic"
		assetDID := "test-asset-1"
		fromDate := time.Now().Add(-24 * time.Hour)
		toDate := time.Now().Add(1 * time.Hour)
		now := time.Now()
		_ = now

		// Setup: Create a confirmed grant
		localTextTXHash := common.BytesToAddress([]byte(licenseID))
		_, err := repo.ConfirmGrant(ctx, licenseID, assetDID, localTextTXHash.Hex(), 1, uint64(defaultGrantAmount), time.Now().Add(-12*time.Hour))
		require.NoError(t, err)

		// Setup: Create some deductions within the time period
		referenceID1 := uuid.NewString()
		_, err = repo.DeductCredits(ctx, licenseID, assetDID, 100, testAPIEndpoint, referenceID1)
		require.NoError(t, err)

		referenceID2 := uuid.NewString()
		_, err = repo.DeductCredits(ctx, licenseID, assetDID, 200, testAPIEndpoint, referenceID2)
		require.NoError(t, err)

		// Test: Get usage report
		report, err := repo.GetLicenseUsageReport(ctx, licenseID, fromDate, toDate)
		require.NoError(t, err)

		// Verify: Report should show correct data
		assert.Equal(t, licenseID, report.LicenseID, "Incorrect license ID")
		assert.Equal(t, fromDate.Truncate(time.Second), report.FromDate.Truncate(time.Second), "Incorrect from date")
		assert.Equal(t, toDate.Truncate(time.Second), report.ToDate.Truncate(time.Second), "Incorrect to date")
		assert.Equal(t, int64(1), report.NumOfAssets, "Incorrect number of assets")                           // One unique asset
		assert.Equal(t, int64(1), report.NumOfCreditsGrantsPurchased, "Incorrect number of grants purchased") // One grant confirmed in period
		assert.Equal(t, int64(300), report.NumOfCreditsUsed, "Incorrect number of credits used")              // 100 + 200 deductions
	})

	t.Run("usage report with multiple assets", func(t *testing.T) {
		t.Parallel()
		licenseID := "test-license-report-multiple-assets"
		assetDID1 := "test-asset-1"
		assetDID2 := "test-asset-2"
		fromDate := time.Now().Add(-24 * time.Hour)

		// Setup: Create grants for two assets
		localTextTXHash1 := common.BytesToAddress([]byte(licenseID + "1"))
		_, err := repo.ConfirmGrant(ctx, licenseID, assetDID1, localTextTXHash1.Hex(), 1, uint64(defaultGrantAmount), time.Now().Add(-12*time.Hour))
		require.NoError(t, err)

		localTextTXHash2 := common.BytesToAddress([]byte(licenseID + "2"))
		_, err = repo.ConfirmGrant(ctx, licenseID, assetDID2, localTextTXHash2.Hex(), 2, uint64(defaultGrantAmount), time.Now().Add(-12*time.Hour))
		require.NoError(t, err)

		// Setup: Create deductions for both assets
		referenceID1 := uuid.NewString()
		_, err = repo.DeductCredits(ctx, licenseID, assetDID1, 100, testAPIEndpoint, referenceID1)
		require.NoError(t, err)

		referenceID2 := uuid.NewString()
		_, err = repo.DeductCredits(ctx, licenseID, assetDID2, 150, testAPIEndpoint, referenceID2)
		require.NoError(t, err)

		// Test: Get usage report
		report, err := repo.GetLicenseUsageReport(ctx, licenseID, fromDate, time.Now())
		require.NoError(t, err)

		// Verify: Report should show correct data
		assert.Equal(t, licenseID, report.LicenseID, "Incorrect license ID")
		assert.Equal(t, int64(2), report.NumOfAssets, "Incorrect number of assets")                           // Two unique assets
		assert.Equal(t, int64(2), report.NumOfCreditsGrantsPurchased, "Incorrect number of grants purchased") // Two grants confirmed in period
		assert.Equal(t, int64(250), report.NumOfCreditsUsed, "Incorrect number of credits used")              // 100 + 150 deductions
	})

	t.Run("usage report with time period filtering", func(t *testing.T) {
		t.Parallel()
		licenseID := "test-license-report-time-filter"
		assetDID := "test-asset-time"

		// Setup: Create a grant before the time period
		localTextTXHash := common.BytesToAddress([]byte(licenseID))
		_, err := repo.ConfirmGrant(ctx, licenseID, assetDID, localTextTXHash.Hex(), 1, uint64(defaultGrantAmount), time.Now().Add(-24*time.Hour))
		require.NoError(t, err)

		// Setup: Create deductions outside the time period (before)
		referenceID1 := uuid.NewString()
		_, err = repo.DeductCredits(ctx, licenseID, assetDID, 100, testAPIEndpoint, referenceID1)
		require.NoError(t, err)

		fromDate := time.Now()

		// Setup: Create deductions within the time period
		referenceID2 := uuid.NewString()
		_, err = repo.DeductCredits(ctx, licenseID, assetDID, 200, testAPIEndpoint, referenceID2)
		require.NoError(t, err)

		toDate := time.Now()
		// Setup: Create deductions outside the time period (after)
		referenceID3 := uuid.NewString()
		_, err = repo.DeductCredits(ctx, licenseID, assetDID, 300, testAPIEndpoint, referenceID3)
		require.NoError(t, err)

		// Test: Get usage report for specific time period
		report, err := repo.GetLicenseUsageReport(ctx, licenseID, fromDate, toDate)
		require.NoError(t, err)

		// Verify: Report should only include data within the time period
		assert.Equal(t, licenseID, report.LicenseID, "Incorrect license ID")
		assert.Equal(t, int64(1), report.NumOfAssets, "Incorrect number of assets")                           // One asset accessed in period
		assert.Equal(t, int64(0), report.NumOfCreditsGrantsPurchased, "Incorrect number of grants purchased") // No grants confirmed in period
		assert.Equal(t, int64(200), report.NumOfCreditsUsed, "Incorrect number of credits used")              // Only the deduction within the period
	})

	t.Run("usage report with refunds", func(t *testing.T) {
		t.Parallel()
		licenseID := "test-license-report-refunds"
		assetDID := "test-asset-refunds"
		fromDate := time.Now().Add(-24 * time.Hour)
		toDate := time.Time{} // Zero time

		// Setup: Create a confirmed grant
		localTextTXHash := common.BytesToAddress([]byte(licenseID))
		_, err := repo.ConfirmGrant(ctx, licenseID, assetDID, localTextTXHash.Hex(), 1, uint64(defaultGrantAmount), time.Now().Add(-12*time.Hour))
		require.NoError(t, err)

		// Setup: Create a deduction
		referenceID := uuid.NewString()
		_, err = repo.DeductCredits(ctx, licenseID, assetDID, 200, testAPIEndpoint, referenceID)
		require.NoError(t, err)

		// Setup: Refund the deduction
		_, err = repo.RefundCredits(ctx, testAPIEndpoint, referenceID)
		require.NoError(t, err)

		// Test: Get usage report with zero toDate and refund
		report, err := repo.GetLicenseUsageReport(ctx, licenseID, fromDate, toDate)
		require.NoError(t, err)

		// Verify: Report should show net usage (deduction - refund)
		assert.Equal(t, licenseID, report.LicenseID, "Incorrect license ID")
		assert.Equal(t, int64(1), report.NumOfAssets, "Incorrect number of assets")                           // Asset was accessed
		assert.Equal(t, int64(1), report.NumOfCreditsGrantsPurchased, "Incorrect number of grants purchased") // One grant confirmed in period
		assert.Equal(t, int64(0), report.NumOfCreditsUsed, "Incorrect number of credits used")
	})

	t.Run("usage report with multiple grants in period", func(t *testing.T) {
		t.Parallel()
		licenseID := "test-license-report-multiple-grants"
		assetDID := "test-asset-multiple-grants"
		fromDate := time.Now().Add(-24 * time.Hour)

		// Setup: Create multiple grants within the time period
		localTextTXHash1 := common.BytesToAddress([]byte(licenseID + "1"))
		_, err := repo.ConfirmGrant(ctx, licenseID, assetDID, localTextTXHash1.Hex(), 1, uint64(defaultGrantAmount), time.Now().Add(-12*time.Hour))
		require.NoError(t, err)

		localTextTXHash2 := common.BytesToAddress([]byte(licenseID + "2"))
		_, err = repo.ConfirmGrant(ctx, licenseID, assetDID, localTextTXHash2.Hex(), 2, uint64(defaultGrantAmount), time.Now().Add(-6*time.Hour))
		require.NoError(t, err)

		// Setup: Create some deductions
		referenceID := uuid.NewString()
		_, err = repo.DeductCredits(ctx, licenseID, assetDID, 100, testAPIEndpoint, referenceID)
		require.NoError(t, err)

		// Test: Get usage report
		report, err := repo.GetLicenseUsageReport(ctx, licenseID, fromDate, time.Now())
		require.NoError(t, err)

		// Verify: Report should show correct data
		assert.Equal(t, licenseID, report.LicenseID, "Incorrect license ID")
		assert.Equal(t, int64(1), report.NumOfAssets, "Incorrect number of assets")                           // One unique asset
		assert.Equal(t, int64(2), report.NumOfCreditsGrantsPurchased, "Incorrect number of grants purchased") // Two grants confirmed in period
		assert.Equal(t, int64(100), report.NumOfCreditsUsed, "Incorrect number of credits used")              // One deduction
	})

	t.Run("usage report with no activity in period", func(t *testing.T) {
		t.Parallel()
		licenseID := "test-license-report-no-activity"
		assetDID := "test-asset-no-activity"
		fromDate := time.Now().Add(-6 * time.Hour)
		toDate := time.Now().Add(-3 * time.Hour)

		// Setup: Create a grant outside the time period
		localTextTXHash := common.BytesToAddress([]byte(licenseID))
		_, err := repo.ConfirmGrant(ctx, licenseID, assetDID, localTextTXHash.Hex(), 1, uint64(defaultGrantAmount), time.Now().Add(-12*time.Hour))
		require.NoError(t, err)

		// Setup: Create deductions outside the time period
		referenceID := uuid.NewString()
		_, err = repo.DeductCredits(ctx, licenseID, assetDID, 100, testAPIEndpoint, referenceID)
		require.NoError(t, err)

		// Test: Get usage report for period with no activity
		report, err := repo.GetLicenseUsageReport(ctx, licenseID, fromDate, toDate)
		require.NoError(t, err)

		// Verify: Report should show zero activity
		assert.Equal(t, licenseID, report.LicenseID, "Incorrect license ID")
		assert.Equal(t, int64(0), report.NumOfAssets, "Incorrect number of assets")                           // No assets accessed in period
		assert.Equal(t, int64(0), report.NumOfCreditsGrantsPurchased, "Incorrect number of grants purchased") // No grants confirmed in period
		assert.Equal(t, int64(0), report.NumOfCreditsUsed, "Incorrect number of credits used")                // No deductions in period
	})

	t.Run("usage report with missing fromDate", func(t *testing.T) {
		t.Parallel()
		licenseID := "test-license-report-missing-fromdate"
		fromDate := time.Time{} // Zero time
		toDate := time.Now()

		// Test: Get usage report with missing fromDate
		report, err := repo.GetLicenseUsageReport(ctx, licenseID, fromDate, toDate)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "fromDate and licenseID are required")
		assert.Nil(t, report)
	})

	t.Run("usage report with missing licenseID", func(t *testing.T) {
		t.Parallel()
		licenseID := "" // Empty license ID
		fromDate := time.Now().Add(-24 * time.Hour)
		toDate := time.Now()

		// Test: Get usage report with missing licenseID
		report, err := repo.GetLicenseUsageReport(ctx, licenseID, fromDate, toDate)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "fromDate and licenseID are required")
		assert.Nil(t, report)
	})

	t.Run("usage report with zero toDate", func(t *testing.T) {
		t.Parallel()
		licenseID := "test-license-report-zero-todate"
		assetDID := "test-asset-zero-todate"
		fromDate := time.Now().Add(-24 * time.Hour)
		toDate := time.Time{} // Zero time

		// Setup: Create a confirmed grant
		localTextTXHash := common.BytesToAddress([]byte(licenseID))
		_, err := repo.ConfirmGrant(ctx, licenseID, assetDID, localTextTXHash.Hex(), 1, uint64(defaultGrantAmount), time.Now().Add(-12*time.Hour))
		require.NoError(t, err)

		// Setup: Create some deductions
		referenceID := uuid.NewString()
		_, err = repo.DeductCredits(ctx, licenseID, assetDID, 100, testAPIEndpoint, referenceID)
		require.NoError(t, err)

		// Test: Get usage report with zero toDate (should work, no upper bound)
		report, err := repo.GetLicenseUsageReport(ctx, licenseID, fromDate, toDate)
		require.NoError(t, err)

		// Verify: Report should show correct data
		assert.Equal(t, licenseID, report.LicenseID, "Incorrect license ID")
		assert.Equal(t, int64(1), report.NumOfAssets, "Incorrect number of assets")                           // Asset was accessed
		assert.Equal(t, int64(1), report.NumOfCreditsGrantsPurchased, "Incorrect number of grants purchased") // One grant confirmed in period
		assert.Equal(t, int64(100), report.NumOfCreditsUsed, "Incorrect number of credits used")              // Deduction should be counted
	})

}

func TestGetLicenseAssetUsageReport(t *testing.T) {
	t.Parallel()
	dbContainer := tests.SetupTestContainer(t)
	dbContainer.TeardownIfLastTest(t)
	db := dbContainer.DB

	repo := New(db)
	ctx := context.Background()

	t.Run("basic asset usage report", func(t *testing.T) {
		t.Parallel()
		licenseID := "test-license-asset-report-basic"
		assetDID := "test-asset-basic"
		fromDate := time.Now().Add(-24 * time.Hour)
		toDate := time.Now().Add(1 * time.Hour)

		// Setup: Create a confirmed grant
		localTextTXHash := common.BytesToAddress([]byte(licenseID))
		_, err := repo.ConfirmGrant(ctx, licenseID, assetDID, localTextTXHash.Hex(), 1, uint64(defaultGrantAmount), time.Now().Add(-12*time.Hour))
		require.NoError(t, err)

		// Setup: Create some deductions
		referenceID1 := uuid.NewString()
		_, err = repo.DeductCredits(ctx, licenseID, assetDID, 100, testAPIEndpoint, referenceID1)
		require.NoError(t, err)

		referenceID2 := uuid.NewString()
		_, err = repo.DeductCredits(ctx, licenseID, assetDID, 200, testAPIEndpoint, referenceID2)
		require.NoError(t, err)

		// Test: Get asset usage report
		report, err := repo.GetLicenseAssetUsageReport(ctx, licenseID, assetDID, fromDate, toDate)
		require.NoError(t, err)

		// Verify: Report should show correct data
		assert.Equal(t, licenseID, report.LicenseID, "Incorrect license ID")
		assert.Equal(t, assetDID, report.AssetDID, "Incorrect asset DID")
		assert.Equal(t, fromDate.Truncate(time.Second), report.FromDate.Truncate(time.Second), "Incorrect from date")
		assert.Equal(t, toDate.Truncate(time.Second), report.ToDate.Truncate(time.Second), "Incorrect to date")
		assert.Equal(t, int64(300), report.NumOfCreditsUsed, "Incorrect number of credits used")                      // 100 + 200 deductions
		assert.Equal(t, int64(1), report.NumOfCreditsGrantsPurchased, "Incorrect number of grants purchased")         // One grant confirmed in period
		assert.Equal(t, int64(defaultGrantAmount-300), report.CurrentCreditsRemaining, "Incorrect remaining credits") // Remaining after deductions
	})

	t.Run("asset usage report with time period filtering", func(t *testing.T) {
		t.Parallel()
		licenseID := "test-license-asset-report-time-filter"
		assetDID := "test-asset-time-filter"

		// Setup: Create a grant before the time period
		localTextTXHash := common.BytesToAddress([]byte(licenseID))
		_, err := repo.ConfirmGrant(ctx, licenseID, assetDID, localTextTXHash.Hex(), 1, uint64(defaultGrantAmount), time.Now().Add(-24*time.Hour))
		require.NoError(t, err)

		// Setup: Create deductions outside the time period (before)
		referenceID1 := uuid.NewString()
		_, err = repo.DeductCredits(ctx, licenseID, assetDID, 100, testAPIEndpoint, referenceID1)
		require.NoError(t, err)

		fromDate := time.Now()

		// Setup: Create deductions within the time period
		referenceID2 := uuid.NewString()
		_, err = repo.DeductCredits(ctx, licenseID, assetDID, 200, testAPIEndpoint, referenceID2)
		require.NoError(t, err)

		toDate := time.Now()

		// Setup: Create deductions outside the time period (after)
		referenceID3 := uuid.NewString()
		_, err = repo.DeductCredits(ctx, licenseID, assetDID, 300, testAPIEndpoint, referenceID3)
		require.NoError(t, err)

		// Test: Get asset usage report for specific time period
		report, err := repo.GetLicenseAssetUsageReport(ctx, licenseID, assetDID, fromDate, toDate)
		require.NoError(t, err)

		// Verify: Report should only include data within the time period
		assert.Equal(t, licenseID, report.LicenseID, "Incorrect license ID")
		assert.Equal(t, assetDID, report.AssetDID, "Incorrect asset DID")
		assert.Equal(t, int64(200), report.NumOfCreditsUsed, "Incorrect number of credits used")                      // Only the deduction within the period
		assert.Equal(t, int64(0), report.NumOfCreditsGrantsPurchased, "Incorrect number of grants purchased")         // No grants confirmed in period
		assert.Equal(t, int64(defaultGrantAmount-600), report.CurrentCreditsRemaining, "Incorrect remaining credits") // Remaining after all deductions
	})

	t.Run("asset usage report with refunds", func(t *testing.T) {
		t.Parallel()
		licenseID := "test-license-asset-report-refunds"
		assetDID := "test-asset-refunds"
		fromDate := time.Now().Add(-24 * time.Hour)
		var toDate time.Time

		// Setup: Create a confirmed grant
		localTextTXHash := common.BytesToAddress([]byte(licenseID))
		_, err := repo.ConfirmGrant(ctx, licenseID, assetDID, localTextTXHash.Hex(), 1, uint64(defaultGrantAmount), time.Now().Add(-12*time.Hour))
		require.NoError(t, err)

		// Setup: Create a deduction
		referenceID := uuid.NewString()
		_, err = repo.DeductCredits(ctx, licenseID, assetDID, 500, testAPIEndpoint, referenceID)
		require.NoError(t, err)

		// Setup: Refund the deduction
		_, err = repo.RefundCredits(ctx, testAPIEndpoint, referenceID)
		require.NoError(t, err)

		// Test: Get asset usage report
		report, err := repo.GetLicenseAssetUsageReport(ctx, licenseID, assetDID, fromDate, toDate)
		require.NoError(t, err)

		// Verify: Report should show net usage (deduction - refund)
		assert.Equal(t, licenseID, report.LicenseID, "Incorrect license ID")
		assert.Equal(t, assetDID, report.AssetDID, "Incorrect asset DID")
		assert.Equal(t, int64(0), report.NumOfCreditsUsed, "Incorrect number of credits used")                    // 500 - 500 = 0 net usage
		assert.Equal(t, int64(1), report.NumOfCreditsGrantsPurchased, "Incorrect number of grants purchased")     // One grant confirmed in period
		assert.Equal(t, int64(defaultGrantAmount), report.CurrentCreditsRemaining, "Incorrect remaining credits") // Full amount remaining after refund
	})

	t.Run("asset usage report with missing parameters", func(t *testing.T) {
		t.Parallel()
		fromDate := time.Now().Add(-24 * time.Hour)
		toDate := time.Now()

		// Test: Missing fromDate
		report, err := repo.GetLicenseAssetUsageReport(ctx, "test-license", "test-asset", time.Time{}, toDate)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "fromDate, licenseID, and assetDID are required")
		assert.Nil(t, report)

		// Test: Missing licenseID
		report, err = repo.GetLicenseAssetUsageReport(ctx, "", "test-asset", fromDate, toDate)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "fromDate, licenseID, and assetDID are required")
		assert.Nil(t, report)

		// Test: Missing assetDID
		report, err = repo.GetLicenseAssetUsageReport(ctx, "test-license", "", fromDate, toDate)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "fromDate, licenseID, and assetDID are required")
		assert.Nil(t, report)
	})

	t.Run("asset usage report with no activity", func(t *testing.T) {
		t.Parallel()
		licenseID := "test-license-asset-report-no-activity"
		assetDID := "test-asset-no-activity"
		fromDate := time.Now().Add(-6 * time.Hour)
		toDate := time.Now().Add(-3 * time.Hour)

		// Setup: Create a grant outside the time period
		localTextTXHash := common.BytesToAddress([]byte(licenseID))
		_, err := repo.ConfirmGrant(ctx, licenseID, assetDID, localTextTXHash.Hex(), 1, uint64(defaultGrantAmount), time.Now().Add(-12*time.Hour))
		require.NoError(t, err)

		// Setup: Create deductions outside the time period
		referenceID := uuid.NewString()
		_, err = repo.DeductCredits(ctx, licenseID, assetDID, 100, testAPIEndpoint, referenceID)
		require.NoError(t, err)

		// Test: Get asset usage report for period with no activity
		report, err := repo.GetLicenseAssetUsageReport(ctx, licenseID, assetDID, fromDate, toDate)
		require.NoError(t, err)

		// Verify: Report should show zero activity but current remaining credits
		assert.Equal(t, licenseID, report.LicenseID, "Incorrect license ID")
		assert.Equal(t, assetDID, report.AssetDID, "Incorrect asset DID")
		assert.Equal(t, int64(0), report.NumOfCreditsUsed, "Incorrect number of credits used")                        // No deductions in period
		assert.Equal(t, int64(0), report.NumOfCreditsGrantsPurchased, "Incorrect number of grants purchased")         // No grants confirmed in period
		assert.Equal(t, int64(defaultGrantAmount-100), report.CurrentCreditsRemaining, "Incorrect remaining credits") // Current balance after deductions
	})

	t.Run("asset usage report with zero toDate", func(t *testing.T) {
		t.Parallel()
		licenseID := "test-license-asset-report-zero-todate"
		assetDID := "test-asset-zero-todate"
		fromDate := time.Now().Add(-24 * time.Hour)
		toDate := time.Time{} // Zero time

		// Setup: Create a confirmed grant
		localTextTXHash := common.BytesToAddress([]byte(licenseID))
		_, err := repo.ConfirmGrant(ctx, licenseID, assetDID, localTextTXHash.Hex(), 1, uint64(defaultGrantAmount), time.Now().Add(-12*time.Hour))
		require.NoError(t, err)

		// Setup: Create some deductions
		referenceID := uuid.NewString()
		_, err = repo.DeductCredits(ctx, licenseID, assetDID, 100, testAPIEndpoint, referenceID)
		require.NoError(t, err)

		// Test: Get asset usage report with zero toDate (should work, no upper bound)
		report, err := repo.GetLicenseAssetUsageReport(ctx, licenseID, assetDID, fromDate, toDate)
		require.NoError(t, err)

		// Verify: Report should show correct data
		assert.Equal(t, licenseID, report.LicenseID, "Incorrect license ID")
		assert.Equal(t, assetDID, report.AssetDID, "Incorrect asset DID")
		assert.Equal(t, int64(100), report.NumOfCreditsUsed, "Incorrect number of credits used")                      // Deduction should be counted
		assert.Equal(t, int64(1), report.NumOfCreditsGrantsPurchased, "Incorrect number of grants purchased")         // One grant confirmed in period
		assert.Equal(t, int64(defaultGrantAmount-100), report.CurrentCreditsRemaining, "Incorrect remaining credits") // Remaining after deduction
	})

	t.Run("asset usage report with zero toDate and refund", func(t *testing.T) {
		t.Parallel()
		licenseID := "test-license-asset-report-zero-todate-refund"
		assetDID := "test-asset-zero-todate-refund"
		fromDate := time.Now().Add(-24 * time.Hour)
		toDate := time.Time{} // Zero time

		// Setup: Create a confirmed grant
		localTextTXHash := common.BytesToAddress([]byte(licenseID))
		_, err := repo.ConfirmGrant(ctx, licenseID, assetDID, localTextTXHash.Hex(), 1, uint64(defaultGrantAmount), time.Now().Add(-12*time.Hour))
		require.NoError(t, err)

		// Setup: Create a deduction
		referenceID := uuid.NewString()
		_, err = repo.DeductCredits(ctx, licenseID, assetDID, 200, testAPIEndpoint, referenceID)
		require.NoError(t, err)

		// Setup: Refund the deduction
		_, err = repo.RefundCredits(ctx, testAPIEndpoint, referenceID)
		require.NoError(t, err)

		// Test: Get asset usage report with zero toDate and refund
		report, err := repo.GetLicenseAssetUsageReport(ctx, licenseID, assetDID, fromDate, toDate)
		require.NoError(t, err)

		// Verify: Report should show net usage (deduction - refund)
		assert.Equal(t, licenseID, report.LicenseID, "Incorrect license ID")
		assert.Equal(t, assetDID, report.AssetDID, "Incorrect asset DID")
		assert.Equal(t, int64(0), report.NumOfCreditsUsed, "Incorrect number of credits used")                    // 200 - 200 = 0 net usage after refund
		assert.Equal(t, int64(1), report.NumOfCreditsGrantsPurchased, "Incorrect number of grants purchased")     // One grant confirmed in period
		assert.Equal(t, int64(defaultGrantAmount), report.CurrentCreditsRemaining, "Incorrect remaining credits") // Full amount remaining after refund
	})
}
