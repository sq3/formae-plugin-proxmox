// SPDX-License-Identifier: Apache-2.0

package config

import (
	"encoding/json"
	"fmt"
	"os"
)

// Config holds Proxmox VE API configuration.
// ApiUrl can be stored in target config (non-sensitive).
// Credentials (TokenID, Secret) are always read from environment variables.
type Config struct {
	// Stored in target config (non-sensitive)
	ApiUrl string `json:"ApiUrl"` // e.g., https://pve.example.com:8006
	Node   string `json:"Node"`   // Default Proxmox node name

	// Read from environment variables only (never stored)
	TokenID string `json:"-"` // From PROXMOX_TOKEN_ID (format: user@realm!tokenname)
	Secret  string `json:"-"` // From PROXMOX_SECRET (API token UUID)
}

// FromTargetConfig extracts Proxmox configuration from target config JSON.
// Only ApiUrl and Node are read from target config.
// Credentials are always read from environment variables.
func FromTargetConfig(targetConfig json.RawMessage) (*Config, error) {
	var cfg Config

	if len(targetConfig) > 0 {
		if err := json.Unmarshal(targetConfig, &cfg); err != nil {
			return nil, fmt.Errorf("failed to unmarshal target config: %w", err)
		}
	}

	// Fall back to environment variables for non-sensitive config
	if cfg.ApiUrl == "" {
		cfg.ApiUrl = os.Getenv("PROXMOX_API_URL")
	}
	if cfg.Node == "" {
		cfg.Node = os.Getenv("PROXMOX_NODE")
	}

	// Credentials are ALWAYS read from environment variables
	cfg.TokenID = os.Getenv("PROXMOX_TOKEN_ID")
	cfg.Secret = os.Getenv("PROXMOX_SECRET")

	return &cfg, nil
}

// Validate checks that required Proxmox API fields are set
func (c *Config) Validate() error {
	if c.ApiUrl == "" {
		return fmt.Errorf("PROXMOX_API_URL environment variable is required")
	}
	if c.TokenID == "" {
		return fmt.Errorf("PROXMOX_TOKEN_ID environment variable is required (format: user@realm!tokenname)")
	}
	if c.Secret == "" {
		return fmt.Errorf("PROXMOX_SECRET environment variable is required")
	}
	if c.Node == "" {
		return fmt.Errorf("PROXMOX_NODE environment variable is required")
	}
	return nil
}
