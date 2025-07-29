package httphandlers

import (
	"fmt"
	"net/url"
	"time"

	"github.com/DIMO-Network/credit-tracker/internal/auth"
	"github.com/DIMO-Network/credit-tracker/internal/config"
	"github.com/DIMO-Network/credit-tracker/internal/creditrepo"
	"github.com/ethereum/go-ethereum/common"
	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog"
)

// HTTPController handles VIN VC-related http requests.
type HTTPController struct {
	creditTrackerRepo   *creditrepo.Repository
	ChainID             uint64
	VehicleContractAddr common.Address
}

// NewHTTPController creates a new http VCController.
func NewHTTPController(service *creditrepo.Repository, settings *config.Settings) *HTTPController {
	return &HTTPController{
		creditTrackerRepo:   service,
		ChainID:             settings.DIMORegistryChainID,
		VehicleContractAddr: settings.VehicleNFTContractAddress,
	}
}

// @Summary Get License Usage Report
// @Description Get usage report for a license across all assets
// @Tags Credits
// @Accept json
// @Produce json
// @Param  licenseId path string true "License ID"
// @Param  fromDate query string true "From Date"
// @Param  toDate query string false "To Date"
// @Success 200 {object} creditrepo.LicenseUsageReport
// @Security     BearerAuth
// @Router /v1/credits/{licenseId}/usage [get]
func (v *HTTPController) GetLicenseUsageReport(fiberCtx *fiber.Ctx) error {
	licenseID := fiberCtx.Params("licenseId")
	if err := isExpectedUser(fiberCtx, licenseID); err != nil {
		zerolog.Ctx(fiberCtx.UserContext()).Error().Err(err).Msg("Unauthorized license does not match")
		return fiber.NewError(fiber.StatusUnauthorized, "Unauthorized license does not match")
	}
	fromDateStr := fiberCtx.Query("fromDate")
	toDateStr := fiberCtx.Query("toDate")
	if fromDateStr == "" {
		return fiber.NewError(fiber.StatusBadRequest, "fromDate is required")
	}
	fromDate, err := time.Parse(time.RFC3339, fromDateStr)
	if err != nil {
		zerolog.Ctx(fiberCtx.UserContext()).Error().Err(err).Msg("Invalid fromDate")
		return fiber.NewError(fiber.StatusBadRequest, "Invalid fromDate")
	}
	var toDate time.Time
	if toDateStr != "" {
		toDate, err = time.Parse(time.RFC3339, toDateStr)
		if err != nil {
			zerolog.Ctx(fiberCtx.UserContext()).Error().Err(err).Msg("Invalid toDate")
			return fiber.NewError(fiber.StatusBadRequest, "Invalid toDate")
		}
	}

	resp, err := v.creditTrackerRepo.GetLicenseUsageReport(fiberCtx.Context(), licenseID, fromDate, toDate)
	if err != nil {
		zerolog.Ctx(fiberCtx.UserContext()).Error().Err(err).Msg("Failed to get license usage report")
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get license usage report")
	}

	return fiberCtx.JSON(resp)
}

// @Summary Get License Asset Usage Report
// @Description Get detailed usage report for a specific license and asset
// @Tags Credits
// @Accept json
// @Produce json
// @Param  licenseId path string true "License ID"
// @Param  assetDID path string true "Asset DID"
// @Param  fromDate query string true "From Date"
// @Param  toDate query string false "To Date"
// @Success 200 {object} creditrepo.LicenseAssetUsageReport
// @Security     BearerAuth
// @Router /v1/credits/{licenseId}/assets/{assetId}/usage [get]
func (v *HTTPController) GetLicenseAssetUsageReport(fiberCtx *fiber.Ctx) error {
	licenseID := fiberCtx.Params("licenseId")
	assetDID := fiberCtx.Params("assetID")
	if err := isExpectedUser(fiberCtx, licenseID); err != nil {
		zerolog.Ctx(fiberCtx.UserContext()).Error().Err(err).Msg("Unauthorized license does not match")
		return fiber.NewError(fiber.StatusUnauthorized, "Unauthorized license does not match")
	}

	fromDateStr := fiberCtx.Query("fromDate")
	toDateStr := fiberCtx.Query("toDate")
	fromDate, err := time.Parse(time.RFC3339, fromDateStr)
	if err != nil {
		zerolog.Ctx(fiberCtx.UserContext()).Error().Err(err).Msg("Invalid fromDate")
		return fiber.NewError(fiber.StatusBadRequest, "Invalid fromDate")
	}
	var toDate time.Time
	if toDateStr != "" {
		toDate, err = time.Parse(time.RFC3339, toDateStr)
		if err != nil {
			zerolog.Ctx(fiberCtx.UserContext()).Error().Err(err).Msg("Invalid toDate")
			return fiber.NewError(fiber.StatusBadRequest, "Invalid toDate")
		}
	}
	// unescape the assetDID
	assetDID, err = url.QueryUnescape(assetDID)
	if err != nil {
		zerolog.Ctx(fiberCtx.UserContext()).Error().Err(err).Msg("Invalid assetDID")
		return fiber.NewError(fiber.StatusBadRequest, "Invalid assetDID")
	}
	resp, err := v.creditTrackerRepo.GetLicenseAssetUsageReport(fiberCtx.Context(), licenseID, assetDID, fromDate, toDate)
	if err != nil {
		zerolog.Ctx(fiberCtx.UserContext()).Error().Err(err).Msg("Failed to get asset usage report")
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get asset usage report")
	}

	return fiberCtx.JSON(resp)
}

func isExpectedUser(fiberCtx *fiber.Ctx, licenseID string) error {
	dexUser, ok := auth.GetDexJWT(fiberCtx)
	if !ok {
		return fmt.Errorf("failed to get dex user from context")
	}
	if dexUser.EthereumAddress != licenseID {
		return fmt.Errorf("dex user %s does not match requested license %s", dexUser.EthereumAddress, licenseID)
	}
	return nil
}
