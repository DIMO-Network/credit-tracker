package httphandlers

import (
	"math/big"

	"github.com/DIMO-Network/cloudevent"
	"github.com/DIMO-Network/credit-tracker/internal/auth"
	"github.com/DIMO-Network/credit-tracker/internal/config"
	"github.com/DIMO-Network/credit-tracker/internal/creditservice"
	"github.com/DIMO-Network/credit-tracker/pkg/grpc"
	"github.com/ethereum/go-ethereum/common"
	"github.com/gofiber/fiber/v2"
)

// HTTPController handles VIN VC-related http requests.
type HTTPController struct {
	creditTrackerService *creditservice.CreditTrackerService
	ChainID              uint64
	VehicleContractAddr  common.Address
}

// NewHTTPController creates a new http VCController.
func NewHTTPController(service *creditservice.CreditTrackerService, settings *config.Settings) *HTTPController {
	return &HTTPController{
		creditTrackerService: service,
		ChainID:              settings.DIMORegistryChainID,
		VehicleContractAddr:  settings.VehicleNFTContractAddress,
	}
}

// @Summary Get Developer Credits
// @Description Get the remaining credits for a developer license and asset DID
// @Tags Credits
// @Accept json
// @Produce json
// @Param  developerLicense path string true "Developer License ID"
// @Param  tokenId path string true "Token ID"
// @Success 200 {object} map[string]interface{}
// @Security     BearerAuth
// @Router /v1/credits/{developerLicense}/{tokenId} [get]
func (v *HTTPController) GetDeveloperCredits(fiberCtx *fiber.Ctx) error {
	dexUser, ok := auth.GetDexJWT(fiberCtx)
	if !ok {
		return fiber.NewError(fiber.StatusUnauthorized, "Unauthorized could not determine user")
	}
	developerLicense := fiberCtx.Params("developerLicense")
	tokenIDParam := fiberCtx.Params("tokenId")
	if dexUser.ProviderID != developerLicense {
		return fiber.NewError(fiber.StatusUnauthorized, "Unauthorized developer license does not match")
	}
	tokenID, ok := new(big.Int).SetString(tokenIDParam, 10)
	if !ok {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid token ID")
	}
	req := &grpc.GetBalanceRequest{
		DeveloperLicense: developerLicense,
		AssetDid: cloudevent.ERC721DID{
			ChainID:         v.ChainID,
			ContractAddress: v.VehicleContractAddr,
			TokenID:         tokenID,
		}.String(),
	}

	resp, err := v.creditTrackerService.GetBalance(fiberCtx.Context(), req)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to check credits")
	}

	return fiberCtx.JSON(fiber.Map{
		"remainingCredits": resp.RemainingCredits,
	})
}
