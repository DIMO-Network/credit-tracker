package creditrepo

import (
	"context"
	"fmt"
	"time"

	"github.com/DIMO-Network/credit-tracker/models"
	"github.com/volatiletech/null/v8"
	"github.com/volatiletech/sqlboiler/v4/queries/qm"
	"golang.org/x/sync/errgroup"
)

var (
	creditSelect = fmt.Sprintf(`
		COALESCE(SUM(CASE WHEN %s = '%s' THEN total_amount ELSE 0 END), 0) - 
		COALESCE(SUM(CASE WHEN %s = '%s' THEN total_amount ELSE 0 END), 0) as usage_count
	`, models.CreditOperationTableColumns.OperationType, OperationTypeDeduction, models.CreditOperationTableColumns.OperationType, OperationTypeRefund)
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

	// Validate date ranges
	if !toDate.IsZero() && fromDate.After(toDate) {
		return nil, fmt.Errorf("fromDate must be before toDate")
	}

	// Validate future dates
	if fromDate.After(time.Now()) {
		return nil, fmt.Errorf("fromDate cannot be in the future")
	}

	// Create error group for parallel execution
	g, ctx := errgroup.WithContext(ctx)

	// Variables to store results
	var assetCount int64
	var creditUsed int64
	var grantCount int64

	// Query 1: Count unique assets accessed during the time period
	g.Go(func() error {
		mods := []qm.QueryMod{
			qm.Select("COUNT(DISTINCT asset_did)"),
			models.CreditOperationWhere.LicenseID.EQ(licenseID),
			models.CreditOperationWhere.CreatedAt.GTE(null.TimeFrom(fromDate)),
		}
		if !toDate.IsZero() {
			mods = append(mods, models.CreditOperationWhere.CreatedAt.LTE(null.TimeFrom(toDate)))
		}

		err := models.CreditOperations(
			mods...,
		).QueryRowContext(ctx, r.db).Scan(&assetCount)
		if err != nil {
			return fmt.Errorf("failed to calculate asset count: %w", err)
		}
		return nil
	})

	// Query 2: Calculate credits used during the time period
	g.Go(func() error {
		mods := []qm.QueryMod{
			qm.Select(creditSelect),
			models.CreditOperationWhere.LicenseID.EQ(licenseID),
			models.CreditOperationWhere.CreatedAt.GTE(null.TimeFrom(fromDate)),
		}
		if !toDate.IsZero() {
			mods = append(mods, models.CreditOperationWhere.CreatedAt.LTE(null.TimeFrom(toDate)))
		}

		err := models.CreditOperations(
			mods...,
		).QueryRowContext(ctx, r.db).Scan(&creditUsed)
		if err != nil {
			return fmt.Errorf("failed to calculate credit usage: %w", err)
		}
		return nil
	})

	// Query 3: Count credit grants purchased during the time period
	g.Go(func() error {
		mods := []qm.QueryMod{
			models.CreditOperationWhere.LicenseID.EQ(licenseID),
			models.CreditOperationWhere.OperationType.EQ(OperationTypeGrantConfirm),
			models.CreditOperationWhere.CreatedAt.GTE(null.TimeFrom(fromDate)),
		}
		if !toDate.IsZero() {
			mods = append(mods, models.CreditOperationWhere.CreatedAt.LTE(null.TimeFrom(toDate)))
		}

		count, err := models.CreditOperations(
			mods...,
		).Count(ctx, r.db)
		if err != nil {
			return fmt.Errorf("failed to count credit grant confirmation operations: %w", err)
		}
		grantCount = count
		return nil
	})

	// Wait for all queries to complete
	if err := g.Wait(); err != nil {
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

	// Validate date ranges
	if !toDate.IsZero() && fromDate.After(toDate) {
		return nil, fmt.Errorf("fromDate must be before toDate")
	}

	// Validate future dates
	if fromDate.After(time.Now()) {
		return nil, fmt.Errorf("fromDate cannot be in the future")
	}

	// Create error group for parallel execution
	g, ctx := errgroup.WithContext(ctx)

	// Variables to store results
	var creditsUsed int64
	var grantsPurchased int64
	var remainingCredits int64

	// Query 1: Calculate credits used during the time period for this specific asset
	g.Go(func() error {
		mods := []qm.QueryMod{
			qm.Select(creditSelect),
			models.CreditOperationWhere.LicenseID.EQ(licenseID),
			models.CreditOperationWhere.AssetDid.EQ(assetDID),
			models.CreditOperationWhere.CreatedAt.GTE(null.TimeFrom(fromDate)),
		}
		if !toDate.IsZero() {
			mods = append(mods, models.CreditOperationWhere.CreatedAt.LTE(null.TimeFrom(toDate)))
		}

		err := models.CreditOperations(
			mods...,
		).QueryRowContext(ctx, r.db).Scan(&creditsUsed)
		if err != nil {
			return fmt.Errorf("failed to calculate credits used: %w", err)
		}
		return nil
	})

	// Query 2: Count credit grants purchased during the time period for this specific asset
	g.Go(func() error {
		grantMods := []qm.QueryMod{
			models.CreditOperationWhere.LicenseID.EQ(licenseID),
			models.CreditOperationWhere.AssetDid.EQ(assetDID),
			models.CreditOperationWhere.OperationType.EQ(OperationTypeGrantConfirm),
			models.CreditOperationWhere.CreatedAt.GTE(null.TimeFrom(fromDate)),
		}
		if !toDate.IsZero() {
			grantMods = append(grantMods, models.CreditOperationWhere.CreatedAt.LTE(null.TimeFrom(toDate)))
		}

		count, err := models.CreditOperations(
			grantMods...,
		).Count(ctx, r.db)
		if err != nil {
			return fmt.Errorf("failed to count credit grants: %w", err)
		}
		grantsPurchased = count
		return nil
	})

	// Query 3: Calculate remaining credits for this license and asset
	g.Go(func() error {
		credits, err := r.GetBalance(ctx, licenseID, assetDID)
		if err != nil {
			return fmt.Errorf("failed to get remaining credits: %w", err)
		}
		remainingCredits = credits
		return nil
	})

	// Wait for all queries to complete
	if err := g.Wait(); err != nil {
		return nil, err
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
