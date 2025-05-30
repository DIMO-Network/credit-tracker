package config

import (
	"fmt"
	"os"

	"github.com/caarlos0/env/v11"
	"github.com/ethereum/go-ethereum/common"
	"gopkg.in/yaml.v3"
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
}

func LoadSettings(filePath string) (*Settings, error) {
	settings := &Settings{}

	// First try to load from settings.yaml
	if _, err := os.Stat(filePath); err == nil {
		data, err := os.ReadFile(filePath)
		if err != nil {
			return nil, fmt.Errorf("failed to read settings from %s: %w", filePath, err)
		}

		var yamlMap map[string]string
		if err := yaml.Unmarshal(data, &yamlMap); err != nil {
			return nil, fmt.Errorf("failed to parse settings from %s: %w", filePath, err)
		}

		opts := env.Options{
			Environment: yamlMap,
		}

		if err := env.ParseWithOptions(settings, opts); err != nil {
			return nil, fmt.Errorf("failed to parse settings from %s: %w", filePath, err)
		}
		return settings, nil
	}

	// Then override with environment variables
	if err := env.Parse(settings); err != nil {
		return nil, fmt.Errorf("failed to parse settings from environment variables: %w", err)
	}

	return settings, nil
}
