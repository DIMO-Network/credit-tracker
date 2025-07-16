package creditrepo

import (
	"context"
	"fmt"
	"time"

	"github.com/DIMO-Network/credit-tracker/models"
	"github.com/volatiletech/null/v8"
	"github.com/volatiletech/sqlboiler/v4/queries/qm"
)

var (
	creditSelect = fmt.Sprintf(`
		SUM(CASE WHEN %s = '%s' THEN total_amount ELSE 0 END) - 
		SUM(CASE WHEN %s = '%s' THEN total_amount ELSE 0 END)
	`, models.CreditOperationWhere.OperationType, OperationTypeDeduction, models.CreditOperationWhere.OperationType, OperationTypeRefund)
)

// UsageReport is a report of the usage of a license
type LicenseUsageReport struct {
	// License ID
	LicenseID string `json:"licenseId"`
	// From date
	FromDate time.Time `json:"fromDate"`
	// To date
	ToDate time.Time `json:"toDate"`
	// Number of assets accessed in a given time period
	NumOfAssets int64 `json:"numOfAssets"`
	// Number of credit grants purchased during the time period
	NumOfCreditsGrantsPurchased int64 `json:"numOfCreditsGrantsPurchased"`
	// Number of credits used during the time period
	NumOfCreditsUsed int64 `json:"numOfCreditsUsed"`
}

type LicenseAssetUsageReport struct {
	// License ID
	LicenseID string `json:"licenseId"`
	// Asset DID
	AssetDID string `json:"assetDid"`
	// From date
	FromDate time.Time `json:"fromDate"`
	// To date
	ToDate time.Time `json:"toDate"`
	// Number of credits used during the time period
	NumOfCreditsUsed int64 `json:"numOfCreditsUsed"`
	// Number of credit grants purchased during the time period
	NumOfCreditsGrantsPurchased int64 `json:"numOfCreditsGrantsPurchased"`
	// Number of credits remaining at the current time, this is not affected by the time period
	CurrentCreditsRemaining int64 `json:"currentCreditsRemaining"`
}

func (r *Repository) GetLicenseUsageReport(ctx context.Context, licenseID string, fromDate time.Time, toDate time.Time) (*LicenseUsageReport, error) {
	if fromDate.IsZero() || licenseID == "" {
		return nil, fmt.Errorf("fromDate and licenseID are required")
	}

	// Count unique assets accessed during the time period
	// We count distinct asset_did from credit_operations where operation_type is 'deduction'
	// and the operation occurred within the specified date range
	mods := []qm.QueryMod{
		qm.Select("COUNT(DISTINCT asset_did)"),
		qm.Select(creditSelect),
		models.CreditOperationWhere.LicenseID.EQ(licenseID),
		models.CreditOperationWhere.CreatedAt.GTE(null.TimeFrom(fromDate)),
	}
	if !toDate.IsZero() {
		mods = append(mods, models.CreditOperationWhere.CreatedAt.LTE(null.TimeFrom(toDate)))
	}
	var assetCount int64
	var creditUsed int64
	err := models.CreditOperations(
		mods...,
	).QueryRowContext(ctx, r.db).Scan(&assetCount)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate asset count: %w", err)
	}

	mods = []qm.QueryMod{
		models.CreditOperationWhere.LicenseID.EQ(licenseID),
		models.CreditOperationWhere.OperationType.EQ(GrantStatusConfirmed),
		models.CreditOperationWhere.CreatedAt.GTE(null.TimeFrom(fromDate)),
	}
	if !toDate.IsZero() {
		mods = append(mods, models.CreditOperationWhere.CreatedAt.LTE(null.TimeFrom(toDate)))
	}
	// Count credit grants purchased during the time period
	// We count grants where status is 'confirmed' and created_at is within the date range
	grantCount, err := models.CreditGrants(
		mods...,
	).Count(ctx, r.db)
	if err != nil {
		return nil, err
	}

	report := &LicenseUsageReport{
		LicenseID:                   licenseID,
		FromDate:                    fromDate,
		ToDate:                      toDate,
		NumOfAssets:                 assetCount,
		NumOfCreditsGrantsPurchased: grantCount,
		NumOfCreditsUsed:            creditUsed,
	}

	return report, nil
}

func (r *Repository) GetLicenseAssetUsageReport(ctx context.Context, licenseID string, assetDID string, fromDate time.Time, toDate time.Time) (*LicenseAssetUsageReport, error) {
	if fromDate.IsZero() || licenseID == "" || assetDID == "" {
		return nil, fmt.Errorf("fromDate, licenseID, and assetDID are required")
	}

	// Calculate credits used during the time period for this specific asset
	// We sum the total_amount from credit_operations where operation_type is 'deduction'
	// and the operation occurred within the specified date range for this license and asset
	mods := []qm.QueryMod{
		qm.Select(creditSelect),
		models.CreditOperationWhere.LicenseID.EQ(licenseID),
		models.CreditOperationWhere.AssetDid.EQ(assetDID),
		models.CreditOperationWhere.CreatedAt.GTE(null.TimeFrom(fromDate)),
	}
	if !toDate.IsZero() {
		mods = append(mods, models.CreditOperationWhere.CreatedAt.LTE(null.TimeFrom(toDate)))
	}
	var creditsUsed int64
	err := models.CreditOperations(
		mods...,
	).QueryRowContext(ctx, r.db).Scan(&creditsUsed)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate credits used: %w", err)
	}

	// Count credit grants purchased during the time period for this specific asset
	// We count grants where status is 'confirmed' and created_at is within the date range
	grantMods := []qm.QueryMod{
		models.CreditGrantWhere.LicenseID.EQ(licenseID),
		models.CreditGrantWhere.AssetDid.EQ(assetDID),
		models.CreditGrantWhere.Status.EQ(GrantStatusConfirmed),
		models.CreditGrantWhere.CreatedAt.GTE(null.TimeFrom(fromDate)),
	}
	if !toDate.IsZero() {
		grantMods = append(grantMods, models.CreditGrantWhere.CreatedAt.LTE(null.TimeFrom(toDate)))
	}
	grantsPurchased, err := models.CreditGrants(
		grantMods...,
	).Count(ctx, r.db)
	if err != nil {
		return nil, fmt.Errorf("failed to count credit grants: %w", err)
	}

	// Calculate remaining credits for this license and asset
	remainingCredits, err := r.GetBalance(ctx, licenseID, assetDID)
	if err != nil {
		return nil, fmt.Errorf("failed to get remaining credits: %w", err)
	}

	report := &LicenseAssetUsageReport{
		LicenseID:                   licenseID,
		AssetDID:                    assetDID,
		FromDate:                    fromDate,
		ToDate:                      toDate,
		NumOfCreditsUsed:            creditsUsed,
		NumOfCreditsGrantsPurchased: grantsPurchased,
		CurrentCreditsRemaining:     remainingCredits,
	}

	return report, nil
}
