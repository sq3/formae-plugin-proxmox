// SPDX-License-Identifier: Apache-2.0
// Test tool to verify List operations work for all resource types

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/platform-engineering-labs/formae-plugin-proxmox/pkg/config"
	"github.com/platform-engineering-labs/formae-plugin-proxmox/pkg/resources/registry"
	"github.com/platform-engineering-labs/formae-plugin-proxmox/pkg/transport"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"

	// Import resource packages to trigger init() registration
	_ "github.com/platform-engineering-labs/formae-plugin-proxmox/pkg/resources/compute"
	_ "github.com/platform-engineering-labs/formae-plugin-proxmox/pkg/resources/network"
	_ "github.com/platform-engineering-labs/formae-plugin-proxmox/pkg/resources/storage"
)

func main() {
	// Build target config JSON
	targetConfig := map[string]interface{}{
		"Type":   "Proxmox",
		"ApiUrl": os.Getenv("PROXMOX_API_URL"),
		"Node":   os.Getenv("PROXMOX_NODE"),
	}
	targetConfigBytes, _ := json.Marshal(targetConfig)

	cfg, err := config.FromTargetConfig(targetConfigBytes)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create config: %v\n", err)
		os.Exit(1)
	}

	client, err := transport.NewClient(&transport.ClientConfig{
		ApiUrl:  cfg.ApiUrl,
		TokenID: cfg.TokenID,
		Secret:  cfg.Secret,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create client: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Registered resource types:")
	for _, rt := range registry.ResourceTypes() {
		fmt.Printf("  - %s\n", rt)
	}
	fmt.Println()

	// Test List for each registered type
	for _, resourceType := range registry.ResourceTypes() {
		fmt.Printf("Testing List for %s...\n", resourceType)

		factory, ok := registry.GetFactory(resourceType)
		if !ok {
			fmt.Printf("  ERROR: No factory found\n")
			continue
		}

		provisioner := factory(client, cfg.Node)

		request := &resource.ListRequest{
			ResourceType: resourceType,
			TargetConfig: targetConfigBytes,
		}

		result, err := provisioner.List(context.Background(), request)
		if err != nil {
			fmt.Printf("  ERROR: %v\n", err)
			continue
		}

		fmt.Printf("  Found %d resources:\n", len(result.NativeIDs))
		for _, id := range result.NativeIDs {
			fmt.Printf("    - %s\n", id)
		}
		fmt.Println()
	}
}
