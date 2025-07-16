package app

import (
	"context"
	"errors"
	"strings"

	"github.com/DIMO-Network/credit-tracker/internal/auth"
	"github.com/DIMO-Network/credit-tracker/internal/config"
	"github.com/DIMO-Network/credit-tracker/internal/controllers/ctrlerrors"
	"github.com/DIMO-Network/credit-tracker/internal/controllers/httphandlers"
	"github.com/DIMO-Network/credit-tracker/internal/controllers/rpc"
	"github.com/DIMO-Network/credit-tracker/internal/creditrepo"
	"github.com/DIMO-Network/credit-tracker/internal/events"
	ctgrpc "github.com/DIMO-Network/credit-tracker/pkg/grpc"
	"github.com/DIMO-Network/shared/pkg/db"
	"github.com/DIMO-Network/shared/pkg/middleware/metrics"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/gofiber/swagger"
	grpc_middleware "github.com/grpc-ecosystem/go-grpc-middleware"
	grpc_ctxtags "github.com/grpc-ecosystem/go-grpc-middleware/tags"
	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/recovery"
	grpc_prometheus "github.com/grpc-ecosystem/go-grpc-prometheus"
	"github.com/rs/zerolog"
	"google.golang.org/grpc"
)

// CreateServers creates a new fiber app and grpc server with the given settings.
func CreateServers(ctx context.Context, settings *config.Settings) (*fiber.App, *grpc.Server, error) {
	ctrl, rpcCtrl, err := createControllers(ctx, settings)
	if err != nil {
		return nil, nil, err
	}
	app := setupHttpServer(ctx, settings, ctrl)
	rpc := setupRPCServer(settings, rpcCtrl)
	return app, rpc, nil
}

func setupHttpServer(ctx context.Context, settings *config.Settings, ctrl *httphandlers.HTTPController) *fiber.App {
	app := fiber.New(fiber.Config{
		ErrorHandler: func(c *fiber.Ctx, err error) error {
			return ErrorHandler(c, err)
		},
		DisableStartupMessage: true,
	})
	logger := zerolog.Ctx(ctx)
	app.Use(func(c *fiber.Ctx) error {
		userCtx := logger.With().Str("httpPath", strings.TrimPrefix(c.Path(), "/")).
			Str("httpMethod", c.Method()).Logger().WithContext(c.UserContext())
		c.SetUserContext(userCtx)
		return c.Next()
	})
	app.Use(recover.New(recover.Config{
		Next:              nil,
		EnableStackTrace:  true,
		StackTraceHandler: nil,
	}))

	app.Get("/swagger/*", swagger.HandlerDefault)
	jwtAuth := auth.Middleware(settings)
	authenticated := app.Group("", jwtAuth)
	authenticated.Get("/v1/credits/:licenseId/usage", ctrl.GetLicenseUsageReport)
	authenticated.Get("/v1/credits/:licenseId/assets/:assetId/usage", ctrl.GetLicenseAssetUsageReport)

	return app
}

func setupRPCServer(settings *config.Settings, rpcCtrl *rpc.CreditTrackerServer) *grpc.Server {
	grpcPanic := metrics.GRPCPanicker{}
	server := grpc.NewServer(
		grpc.UnaryInterceptor(grpc_middleware.ChainUnaryServer(
			// metrics.GRPCMetricsAndLogMiddleware(logger),
			grpc_ctxtags.UnaryServerInterceptor(),
			grpc_prometheus.UnaryServerInterceptor,
			recovery.UnaryServerInterceptor(recovery.WithRecoveryHandler(grpcPanic.GRPCPanicRecoveryHandler)),
		)),
		grpc.StreamInterceptor(grpc_prometheus.StreamServerInterceptor),
	)
	ctgrpc.RegisterCreditTrackerServer(server, rpcCtrl)
	return server
}

// ErrorHandler custom handler to log recovered errors using our logger and return json instead of string
func ErrorHandler(ctx *fiber.Ctx, err error) error {
	code := fiber.StatusInternalServerError // Default 500 statuscode
	message := "Internal error."

	var fiberErr *fiber.Error
	var ctrlErr ctrlerrors.Error
	if errors.As(err, &fiberErr) {
		code = fiberErr.Code
		message = fiberErr.Message
	} else if errors.As(err, &ctrlErr) {
		message = ctrlErr.ExternalMsg
		if ctrlErr.Code != 0 {
			code = ctrlErr.Code
		}
	}

	// log all errors except 404
	if code != fiber.StatusNotFound {
		logger := zerolog.Ctx(ctx.UserContext())
		logger.Err(err).Int("httpStatusCode", code).
			Str("httpPath", strings.TrimPrefix(ctx.Path(), "/")).
			Str("httpMethod", ctx.Method()).
			Msg("caught an error from http request")
	}

	return ctx.Status(code).JSON(codeResp{Code: code, Message: message})
}

type codeResp struct {
	Message string `json:"message"`
	Code    int    `json:"code"`
}

// HealthCheck godoc
// @Summary Show the status of server.
// @Description get the status of server.
// @Tags root
// @Accept */*
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router / [get]
func HealthCheck(ctx *fiber.Ctx) error {
	res := map[string]any{
		"data": "Server is up and running",
	}

	return ctx.JSON(res)
}

// createControllers creates a new controllers with the given settings.
func createControllers(ctx context.Context, settings *config.Settings) (*httphandlers.HTTPController, *rpc.CreditTrackerServer, error) {
	pdb := db.NewDbConnectionFromSettings(ctx, &settings.DB, true)
	logger := zerolog.Ctx(ctx)
	pdb.WaitForDB(*logger)

	repo := creditrepo.New(pdb.DBS().GetWriterConn())
	contractProcessor := events.NewContractProcessor(repo)
	server := rpc.NewServer(repo, contractProcessor)
	ctrl := httphandlers.NewHTTPController(repo, settings)

	return ctrl, server, nil
}
