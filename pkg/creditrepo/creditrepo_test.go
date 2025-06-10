package creditrepo_test

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DIMO-Network/credit-tracker/pkg/creditrepo"
)

func TestRepository_UpdateCredits(t *testing.T) {
	tests := []struct {
		name             string
		developerLicense string
		assetDid         string
		amount           int64
		wantErr          bool
		wantAmount       int64
	}{
		{
			name:             "valid positive amount",
			developerLicense: "dev1",
			assetDid:         "asset1",
			amount:           100,
			wantErr:          false,
			wantAmount:       100,
		},
		{
			name:             "zero amount",
			developerLicense: "dev1",
			assetDid:         "asset2",
			amount:           0,
			wantErr:          false,
			wantAmount:       0,
		},
		{
			name:             "negative amount",
			developerLicense: "dev1",
			assetDid:         "asset3",
			amount:           -50,
			wantErr:          true,
			wantAmount:       0,
		},
		{
			name:             "update existing amount",
			developerLicense: "dev1",
			assetDid:         "asset1",
			amount:           200,
			wantErr:          false,
			wantAmount:       200,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := creditrepo.New()
			got, err := repo.UpdateCredits(context.Background(), tt.developerLicense, tt.assetDid, tt.amount)

			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantAmount, got)

			// Verify the amount was actually stored
			stored, err := repo.GetCredits(context.Background(), tt.developerLicense, tt.assetDid)
			require.NoError(t, err)
			assert.Equal(t, tt.wantAmount, stored)
		})
	}
}

func TestRepository_GetCredits(t *testing.T) {
	tests := []struct {
		name             string
		developerLicense string
		assetDid         string
		setupAmount      int64
		wantAmount       int64
	}{
		{
			name:             "existing credits",
			developerLicense: "dev1",
			assetDid:         "asset1",
			setupAmount:      100,
			wantAmount:       100,
		},
		{
			name:             "non-existent developer",
			developerLicense: "dev2",
			assetDid:         "asset1",
			setupAmount:      0,
			wantAmount:       0,
		},
		{
			name:             "non-existent asset",
			developerLicense: "dev1",
			assetDid:         "asset2",
			setupAmount:      0,
			wantAmount:       0,
		},
		{
			name:             "zero credits",
			developerLicense: "dev1",
			assetDid:         "asset3",
			setupAmount:      0,
			wantAmount:       0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := creditrepo.New()

			// Setup initial state if needed
			if tt.setupAmount > 0 {
				_, err := repo.UpdateCredits(context.Background(), tt.developerLicense, tt.assetDid, tt.setupAmount)
				require.NoError(t, err)
			}

			got, err := repo.GetCredits(context.Background(), tt.developerLicense, tt.assetDid)
			require.NoError(t, err)
			assert.Equal(t, tt.wantAmount, got)
		})
	}
}

func TestRepository_ConcurrentAccess(t *testing.T) {
	repo := creditrepo.New()
	developerLicense := "dev1"
	assetDid := "asset1"
	iterations := 1000

	// Concurrent writes
	t.Run("concurrent writes", func(t *testing.T) {
		var wg sync.WaitGroup
		for i := 0; i < iterations; i++ {
			wg.Add(1)
			go func(amount int64) {
				defer wg.Done()
				_, err := repo.UpdateCredits(context.Background(), developerLicense, assetDid, amount)
				require.NoError(t, err)
			}(int64(i))
		}
		wg.Wait()

		// Verify final value
		got, err := repo.GetCredits(context.Background(), developerLicense, assetDid)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, got, int64(0))
	})

	// Concurrent reads
	t.Run("concurrent reads", func(t *testing.T) {
		var wg sync.WaitGroup
		for i := 0; i < iterations; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				got, err := repo.GetCredits(context.Background(), developerLicense, assetDid)
				require.NoError(t, err)
				assert.GreaterOrEqual(t, got, int64(0))
			}()
		}
		wg.Wait()
	})

	// Mixed reads and writes
	t.Run("mixed reads and writes", func(t *testing.T) {
		var wg sync.WaitGroup
		for i := 0; i < iterations; i++ {
			wg.Add(2)
			go func(amount int64) {
				defer wg.Done()
				_, err := repo.UpdateCredits(context.Background(), developerLicense, assetDid, amount)
				require.NoError(t, err)
			}(int64(i))
			go func() {
				defer wg.Done()
				got, err := repo.GetCredits(context.Background(), developerLicense, assetDid)
				require.NoError(t, err)
				assert.GreaterOrEqual(t, got, int64(0))
			}()
		}
		wg.Wait()
	})
}
