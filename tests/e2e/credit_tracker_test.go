package e2e_test

import (
	"context"
	"io"
	"net"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/DIMO-Network/credit-tracker/internal/app"
	"github.com/DIMO-Network/credit-tracker/internal/config"
	ctgrpc "github.com/DIMO-Network/credit-tracker/pkg/grpc"
	"github.com/DIMO-Network/credit-tracker/tests"
	"github.com/ethereum/go-ethereum/common"
	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type TestServer struct {
	rpcServer  *grpc.Server
	rpcPort    string
	app        *fiber.App
	appPort    string
	authServer *mockAuthServer
}

func setupTestServer(t *testing.T) *TestServer {
	// Create test settings
	settings := &config.Settings{
		GRPCPort: 0, // Let the OS choose an available port
	}
	authServer := setupAuthServer(t)
	authServer.TeardownIfLastTest(t)
	settings.JWKKeySetURL = authServer.URL() + "/keys"

	db := tests.SetupTestContainer(t)
	db.TeardownIfLastTest(t)
	settings.DB = db.Settings

	// Create servers
	app, rpcServer, err := app.CreateServers(t.Context(), settings)
	require.NoError(t, err)

	// Start server on random port
	lis, err := net.Listen("tcp", ":0")
	require.NoError(t, err)

	// Get the actual port
	addr := lis.Addr().(*net.TCPAddr)
	port := strconv.Itoa(addr.Port)

	// Start server in background
	go func() {
		if err := rpcServer.Serve(lis); err != nil {
			t.Errorf("failed to serve: %v", err)
		}
	}()
	lis2, err := net.Listen("tcp", ":0")
	addr2 := lis2.Addr().(*net.TCPAddr)
	appPort := strconv.Itoa(addr2.Port)
	require.NoError(t, err)
	go func() {
		if err := app.Listener(lis2); err != nil {
			t.Errorf("failed to serve: %v", err)
		}
	}()

	return &TestServer{
		authServer: authServer,
		rpcServer:  rpcServer,
		rpcPort:    port,
		app:        app,
		appPort:    appPort,
	}
}

func TestCreditTrackerEndToEnd(t *testing.T) {
	// Set up test server
	server := setupTestServer(t)
	defer server.rpcServer.GracefulStop()

	// Connect to the server
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := grpc.NewClient("localhost:"+server.rpcPort, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer conn.Close() //nolint:errcheck

	client := ctgrpc.NewCreditTrackerClient(conn)

	t.Run("DeductCredits", func(t *testing.T) {
		req := &ctgrpc.CreditDeductRequest{
			AssetDid:         "did:erc721:80002:0x45fbCD3ef7361d156e8b16F5538AE36DEdf61Da8:123",
			DeveloperLicense: "test-license",
			Amount:           10,
		}

		_, err := client.DeductCredits(ctx, req)
		require.NoError(t, err)
	})
}

func TestCreditTrackerBasicAuth(t *testing.T) {
	// Set up test server
	server := setupTestServer(t)
	defer server.app.Shutdown()

	// Connect to the server
	devAddress := common.HexToAddress("0x1234567890123456789012345678901234567890")
	req := httptest.NewRequestWithContext(t.Context(), "GET", "/v1/credits/"+devAddress.String()+"/usage?fromDate=2025-01-01T00:00:00Z", nil)
	token, err := server.authServer.CreateToken(t, devAddress)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := server.app.Test(req)
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, fiber.StatusOK, resp.StatusCode, string(body))
}
