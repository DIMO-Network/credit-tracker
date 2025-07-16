package httphandlers

import (
	"time"

	"github.com/DIMO-Network/credit-tracker/internal/auth"
	"github.com/DIMO-Network/credit-tracker/internal/config"
	"github.com/DIMO-Network/credit-tracker/internal/creditrepo"
	"github.com/ethereum/go-ethereum/common"
	"github.com/gofiber/fiber/v2"
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
	dexUser, ok := auth.GetDexJWT(fiberCtx)
	if !ok {
		return fiber.NewError(fiber.StatusUnauthorized, "Unauthorized could not determine user")
	}
	licenseID := fiberCtx.Params("licenseId")
	if dexUser.EthereumAddress != licenseID {
		return fiber.NewError(fiber.StatusUnauthorized, "Unauthorized license does not match")
	}
	fromDateStr := fiberCtx.Query("fromDate")
	toDateStr := fiberCtx.Query("toDate")
	fromDate, err := time.Parse(time.RFC3339, fromDateStr)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid fromDate")
	}
	var toDate time.Time
	if toDateStr != "" {
		toDate, err = time.Parse(time.RFC3339, toDateStr)
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "Invalid toDate")
		}
	}

	resp, err := v.creditTrackerRepo.GetLicenseUsageReport(fiberCtx.Context(), licenseID, fromDate, toDate)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to check credits")
	}

	return fiberCtx.JSON(resp)
}

// @Summary Get License Asset Usage Report
// @Description Get detailed usage report for a specific license and asset
// @Tags Credits
// @Accept json
// @Produce json
// @Param  licenseId path string true "License ID"
// @Param  assetId path string true "Asset ID"
// @Param  fromDate query string true "From Date"
// @Param  toDate query string false "To Date"
// @Success 200 {object} creditrepo.LicenseAssetUsageReport
// @Security     BearerAuth
// @Router /v1/credits/{licenseId}/assets/{assetId}/usage [get]
func (v *HTTPController) GetLicenseAssetUsageReport(fiberCtx *fiber.Ctx) error {
	dexUser, ok := auth.GetDexJWT(fiberCtx)
	if !ok {
		return fiber.NewError(fiber.StatusUnauthorized, "Unauthorized could not determine user")
	}
	licenseID := fiberCtx.Params("licenseId")
	assetID := fiberCtx.Params("assetId")
	if dexUser.EthereumAddress != licenseID {
		return fiber.NewError(fiber.StatusUnauthorized, "Unauthorized license does not match")
	}

	fromDateStr := fiberCtx.Query("fromDate")
	toDateStr := fiberCtx.Query("toDate")
	fromDate, err := time.Parse(time.RFC3339, fromDateStr)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid fromDate")
	}
	var toDate time.Time
	if toDateStr != "" {
		toDate, err = time.Parse(time.RFC3339, toDateStr)
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "Invalid toDate")
		}
	}

	resp, err := v.creditTrackerRepo.GetLicenseAssetUsageReport(fiberCtx.Context(), licenseID, assetID, fromDate, toDate)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get asset usage report")
	}

	return fiberCtx.JSON(resp)
}
