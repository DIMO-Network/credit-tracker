package config

import (
	"fmt"
	"os"

	"github.com/DIMO-Network/shared/pkg/db"
	"github.com/caarlos0/env/v11"
	"github.com/ethereum/go-ethereum/common"
	"github.com/joho/godotenv"
)

// Settings contains the application config.
type Settings struct {
	Environment               string         `env:"ENVIRONMENT"`
	LogLevel                  string         `env:"LOG_LEVEL"`
	Port                      int            `env:"PORT"`
	MonPort                   int            `env:"MON_PORT"`
	GRPCPort                  int            `env:"GRPC_PORT"`
	JWKKeySetURL              string         `env:"JWT_KEY_SET_URL"`
	DIMORegistryChainID       uint64         `env:"DIMO_REGISTRY_CHAIN_ID"`
	VehicleNFTContractAddress common.Address `env:"VEHICLE_NFT_CONTRACT_ADDRESS"`
	DB                        db.Settings    `envPrefix:"DB_"`
}

func LoadSettings(filePath string) (*Settings, error) {
	settings := &Settings{}

	// First try to load from settings.yaml
	if _, err := os.Stat(filePath); err == nil {
		err = godotenv.Load(filePath)
		if err != nil {
			return nil, fmt.Errorf("failed to load settings from %s: %w", filePath, err)
		}
	}

	// Then override with environment variables
	if err := env.Parse(settings); err != nil {
		return nil, fmt.Errorf("failed to parse settings from environment variables: %w", err)
	}

	return settings, nil
}
