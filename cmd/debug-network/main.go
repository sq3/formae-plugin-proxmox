// SPDX-License-Identifier: Apache-2.0
// Debug tool to test network API responses

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/platform-engineering-labs/formae-plugin-proxmox/pkg/transport"
)

func main() {
	client, err := transport.NewClient(&transport.ClientConfig{
		ApiUrl:  os.Getenv("PROXMOX_API_URL"),
		TokenID: os.Getenv("PROXMOX_TOKEN_ID"),
		Secret:  os.Getenv("PROXMOX_SECRET"),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create client: %v\n", err)
		os.Exit(1)
	}

	node := os.Getenv("PROXMOX_NODE")
	if node == "" {
		node = "homeserver"
	}

	fmt.Printf("Fetching network interfaces for node: %s\n\n", node)

	path := fmt.Sprintf("/nodes/%s/network", node)
	resp, err := client.Get(context.Background(), path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to get network: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Response Data type: %T\n", resp.Data)
	fmt.Printf("Response DataArray length: %d\n\n", len(resp.DataArray))

	for i, item := range resp.DataArray {
		ifaceData, ok := item.(map[string]interface{})
		if !ok {
			fmt.Printf("Item %d: not a map, type=%T\n", i, item)
			continue
		}

		iface := ifaceData["iface"]
		ifaceType := ifaceData["type"]
		method := ifaceData["method"]

		fmt.Printf("Interface %d:\n", i)
		fmt.Printf("  iface: %v (type: %T)\n", iface, iface)
		fmt.Printf("  type: %v (type: %T)\n", ifaceType, ifaceType)
		fmt.Printf("  method: %v\n", method)

		// Check if it would be considered manageable
		typeStr, _ := ifaceType.(string)
		manageable := isManageableInterfaceType(typeStr)
		fmt.Printf("  manageable: %v\n", manageable)

		// Print full data for debugging
		jsonData, _ := json.MarshalIndent(ifaceData, "  ", "  ")
		fmt.Printf("  full data: %s\n\n", jsonData)
	}
}

func isManageableInterfaceType(ifaceType string) bool {
	manageableTypes := map[string]bool{
		"bridge":     true,
		"bond":       true,
		"vlan":       true,
		"OVSBridge":  true,
		"OVSBond":    true,
		"OVSPort":    true,
		"OVSIntPort": true,
	}
	return manageableTypes[ifaceType]
}
