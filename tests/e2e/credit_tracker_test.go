package e2e_test

import (
	"context"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/DIMO-Network/credit-tracker/internal/app"
	"github.com/DIMO-Network/credit-tracker/internal/config"
	ctgrpc "github.com/DIMO-Network/credit-tracker/pkg/grpc"
	"github.com/DIMO-Network/credit-tracker/tests"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func setupTestServer(t *testing.T) (*grpc.Server, string) {
	// Create test settings
	settings := &config.Settings{
		GRPCPort: 0, // Let the OS choose an available port
	}
	authServer := setupAuthServer(t)
	defer authServer.Close()
	settings.JWKKeySetURL = authServer.URL() + "/keys"

	db := tests.SetupTestContainer(t)
	defer db.TeardownIfLastTest(t)
	settings.DB = db.Settings

	// Create servers
	_, rpcServer, err := app.CreateServers(t.Context(), settings)
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

	return rpcServer, port
}

func TestCreditTrackerEndToEnd(t *testing.T) {
	// Set up test server
	rpcServer, port := setupTestServer(t)
	defer rpcServer.GracefulStop()

	// Connect to the server
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := grpc.NewClient("localhost:"+port, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer conn.Close() //nolint:errcheck

	client := ctgrpc.NewCreditTrackerClient(conn)

	t.Run("GetBalance", func(t *testing.T) {
		req := &ctgrpc.GetBalanceRequest{
			AssetDid:         "did:erc721:80002:0x45fbCD3ef7361d156e8b16F5538AE36DEdf61Da8:123",
			DeveloperLicense: "test-license",
		}

		resp, err := client.GetBalance(ctx, req)
		require.NoError(t, err)
		assert.Equal(t, int64(0), resp.RemainingCredits)
	})

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
