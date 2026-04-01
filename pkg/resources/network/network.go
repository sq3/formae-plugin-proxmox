// SPDX-License-Identifier: Apache-2.0

package network

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/platform-engineering-labs/formae-plugin-proxmox/pkg/resources/prov"
	"github.com/platform-engineering-labs/formae-plugin-proxmox/pkg/resources/registry"
	"github.com/platform-engineering-labs/formae-plugin-proxmox/pkg/transport"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

const NetworkResourceType = "Proxmox::Network::Interface"

// networkProvisioner handles network interface lifecycle operations
type networkProvisioner struct {
	client *transport.Client
	node   string
}

var _ prov.Provisioner = &networkProvisioner{}

// resolveNode returns the node name from target config or environment
func (p *networkProvisioner) resolveNode(targetConfig []byte) string {
	if p.node != "" {
		return p.node
	}

	var cfg struct {
		Node string `json:"Node"`
	}
	if len(targetConfig) > 0 {
		if err := json.Unmarshal(targetConfig, &cfg); err == nil && cfg.Node != "" {
			return cfg.Node
		}
	}

	return os.Getenv("PROXMOX_NODE")
}

// parseNativeID extracts node and iface from native ID (format: node/iface)
func parseNativeID(nativeID string) (node, iface string, err error) {
	parts := strings.SplitN(nativeID, "/", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid native ID format, expected 'node/iface': %s", nativeID)
	}
	return parts[0], parts[1], nil
}

// Create creates a new network interface.
// POST /nodes/{node}/network
func (p *networkProvisioner) Create(ctx context.Context, request *resource.CreateRequest) (*resource.CreateResult, error) {
	var props map[string]interface{}
	if err := json.Unmarshal(request.Properties, &props); err != nil {
		return createFailure(resource.OperationErrorCodeInvalidRequest,
			fmt.Sprintf("failed to parse properties: %v", err)), nil
	}

	node := p.resolveNode(request.TargetConfig)
	if node == "" {
		return createFailure(resource.OperationErrorCodeInvalidRequest,
			"node is required but not found in target config or PROXMOX_NODE"), nil
	}

	iface, ok := props["iface"].(string)
	if !ok || iface == "" {
		return createFailure(resource.OperationErrorCodeInvalidRequest,
			"iface (interface name) is required"), nil
	}

	ifaceType, ok := props["type"].(string)
	if !ok || ifaceType == "" {
		return createFailure(resource.OperationErrorCodeInvalidRequest,
			"type is required"), nil
	}

	params := make(map[string]interface{})
	for k, v := range props {
		if v == nil {
			continue
		}
		// Proxmox API expects booleans as 0/1 integers
		params[k] = convertForProxmoxAPI(v)
	}

	path := fmt.Sprintf("/nodes/%s/network", node)
	_, err := p.client.Post(ctx, path, params)
	if err != nil {
		return handleTransportError(err), nil
	}

	// Apply network changes (required for changes to take effect)
	applyPath := fmt.Sprintf("/nodes/%s/network", node)
	_, _ = p.client.Put(ctx, applyPath, nil) // Best effort apply

	nativeID := fmt.Sprintf("%s/%s", node, iface)

	// Read back the created interface configuration
	configPath := fmt.Sprintf("/nodes/%s/network/%s", node, iface)
	configResp, err := p.client.Get(ctx, configPath)
	var propsJSON json.RawMessage
	if err == nil && configResp.Data != nil {
		propsJSON, _ = json.Marshal(configResp.Data)
	}

	return &resource.CreateResult{
		ProgressResult: &resource.ProgressResult{
			Operation:          resource.OperationCreate,
			OperationStatus:    resource.OperationStatusSuccess,
			NativeID:           nativeID,
			ResourceProperties: propsJSON,
		},
	}, nil
}

// Read retrieves network interface configuration.
// GET /nodes/{node}/network/{iface}
func (p *networkProvisioner) Read(ctx context.Context, request *resource.ReadRequest) (*resource.ReadResult, error) {
	node, iface, err := parseNativeID(request.NativeID)
	if err != nil {
		return &resource.ReadResult{
			ErrorCode: resource.OperationErrorCodeInvalidRequest,
		}, nil
	}

	path := fmt.Sprintf("/nodes/%s/network/%s", node, iface)
	resp, err := p.client.Get(ctx, path)
	if err != nil {
		if transportErr, ok := err.(*transport.Error); ok {
			return &resource.ReadResult{
				ErrorCode: transport.ToResourceErrorCode(transportErr.Code),
			}, nil
		}
		return &resource.ReadResult{
			ErrorCode: resource.OperationErrorCodeServiceInternalError,
		}, nil
	}

	propsJSON, _ := json.Marshal(resp.Data)

	return &resource.ReadResult{
		Properties: string(propsJSON),
	}, nil
}

// Update modifies network interface configuration.
// PUT /nodes/{node}/network/{iface}
func (p *networkProvisioner) Update(ctx context.Context, request *resource.UpdateRequest) (*resource.UpdateResult, error) {
	node, iface, err := parseNativeID(request.NativeID)
	if err != nil {
		return updateFailure(request.NativeID, resource.OperationErrorCodeInvalidRequest,
			err.Error()), nil
	}

	var props map[string]interface{}
	if err := json.Unmarshal(request.DesiredProperties, &props); err != nil {
		return updateFailure(request.NativeID, resource.OperationErrorCodeInvalidRequest,
			fmt.Sprintf("failed to parse properties: %v", err)), nil
	}

	// Filter out read-only and create-only properties
	readOnlyFields := map[string]bool{
		"iface":  true,
		"type":   true,
		"active": true,
		"method": true,
	}
	params := make(map[string]interface{})
	for k, v := range props {
		if v == nil || readOnlyFields[k] {
			continue
		}
		// Proxmox API expects booleans as 0/1 integers
		params[k] = convertForProxmoxAPI(v)
	}

	path := fmt.Sprintf("/nodes/%s/network/%s", node, iface)
	_, err = p.client.Put(ctx, path, params)
	if err != nil {
		if transportErr, ok := err.(*transport.Error); ok {
			return updateFailure(request.NativeID,
				transport.ToResourceErrorCode(transportErr.Code), transportErr.Message), nil
		}
		return updateFailure(request.NativeID,
			resource.OperationErrorCodeServiceInternalError, err.Error()), nil
	}

	// Apply network changes
	applyPath := fmt.Sprintf("/nodes/%s/network", node)
	_, _ = p.client.Put(ctx, applyPath, nil)

	// Read back updated configuration
	configResp, err := p.client.Get(ctx, path)
	var propsJSON json.RawMessage
	if err == nil && configResp.Data != nil {
		propsJSON, _ = json.Marshal(configResp.Data)
	}

	return &resource.UpdateResult{
		ProgressResult: &resource.ProgressResult{
			Operation:          resource.OperationUpdate,
			OperationStatus:    resource.OperationStatusSuccess,
			NativeID:           request.NativeID,
			ResourceProperties: propsJSON,
		},
	}, nil
}

// Delete removes a network interface.
// DELETE /nodes/{node}/network/{iface}
func (p *networkProvisioner) Delete(ctx context.Context, request *resource.DeleteRequest) (*resource.DeleteResult, error) {
	node, iface, err := parseNativeID(request.NativeID)
	if err != nil {
		return &resource.DeleteResult{
			ProgressResult: &resource.ProgressResult{
				Operation:       resource.OperationDelete,
				OperationStatus: resource.OperationStatusFailure,
				ErrorCode:       resource.OperationErrorCodeInvalidRequest,
				StatusMessage:   err.Error(),
			},
		}, nil
	}

	path := fmt.Sprintf("/nodes/%s/network/%s", node, iface)
	_, err = p.client.Delete(ctx, path)
	if err != nil {
		if transportErr, ok := err.(*transport.Error); ok {
			// If already deleted, treat as success
			if transportErr.Code == transport.ErrorCodeResourceNotFound {
				return &resource.DeleteResult{
					ProgressResult: &resource.ProgressResult{
						Operation:       resource.OperationDelete,
						OperationStatus: resource.OperationStatusSuccess,
						NativeID:        request.NativeID,
					},
				}, nil
			}
			return &resource.DeleteResult{
				ProgressResult: &resource.ProgressResult{
					Operation:       resource.OperationDelete,
					OperationStatus: resource.OperationStatusFailure,
					ErrorCode:       transport.ToResourceErrorCode(transportErr.Code),
					StatusMessage:   transportErr.Message,
					NativeID:        request.NativeID,
				},
			}, nil
		}
		return &resource.DeleteResult{
			ProgressResult: &resource.ProgressResult{
				Operation:       resource.OperationDelete,
				OperationStatus: resource.OperationStatusFailure,
				ErrorCode:       resource.OperationErrorCodeServiceInternalError,
				StatusMessage:   err.Error(),
				NativeID:        request.NativeID,
			},
		}, nil
	}

	// Apply network changes
	applyPath := fmt.Sprintf("/nodes/%s/network", node)
	_, _ = p.client.Put(ctx, applyPath, nil)

	return &resource.DeleteResult{
		ProgressResult: &resource.ProgressResult{
			Operation:       resource.OperationDelete,
			OperationStatus: resource.OperationStatusSuccess,
			NativeID:        request.NativeID,
		},
	}, nil
}

// Status checks the current state of a network interface.
func (p *networkProvisioner) Status(ctx context.Context, request *resource.StatusRequest) (*resource.StatusResult, error) {
	node, iface, err := parseNativeID(request.NativeID)
	if err != nil {
		return &resource.StatusResult{
			ProgressResult: &resource.ProgressResult{
				Operation:       resource.OperationCheckStatus,
				OperationStatus: resource.OperationStatusFailure,
				ErrorCode:       resource.OperationErrorCodeInvalidRequest,
				StatusMessage:   err.Error(),
			},
		}, nil
	}

	path := fmt.Sprintf("/nodes/%s/network/%s", node, iface)
	resp, err := p.client.Get(ctx, path)
	if err != nil {
		if transportErr, ok := err.(*transport.Error); ok {
			if transportErr.Code == transport.ErrorCodeResourceNotFound {
				return &resource.StatusResult{
					ProgressResult: &resource.ProgressResult{
						Operation:       resource.OperationCheckStatus,
						OperationStatus: resource.OperationStatusFailure,
						ErrorCode:       resource.OperationErrorCodeNotFound,
						NativeID:        request.NativeID,
					},
				}, nil
			}
		}
		return &resource.StatusResult{
			ProgressResult: &resource.ProgressResult{
				Operation:       resource.OperationCheckStatus,
				OperationStatus: resource.OperationStatusFailure,
				ErrorCode:       resource.OperationErrorCodeServiceInternalError,
				StatusMessage:   err.Error(),
				NativeID:        request.NativeID,
			},
		}, nil
	}

	propsJSON, _ := json.Marshal(resp.Data)

	return &resource.StatusResult{
		ProgressResult: &resource.ProgressResult{
			Operation:          resource.OperationCheckStatus,
			OperationStatus:    resource.OperationStatusSuccess,
			NativeID:           request.NativeID,
			ResourceProperties: propsJSON,
		},
	}, nil
}

// List returns all network interfaces on the node.
// GET /nodes/{node}/network
func (p *networkProvisioner) List(ctx context.Context, request *resource.ListRequest) (*resource.ListResult, error) {
	node := p.resolveNode(request.TargetConfig)
	if node == "" {
		return nil, fmt.Errorf("node is required for listing network interfaces")
	}

	path := fmt.Sprintf("/nodes/%s/network", node)
	resp, err := p.client.Get(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("failed to list network interfaces: %w", err)
	}

	var nativeIDs []string
	for _, item := range resp.DataArray {
		ifaceData, ok := item.(map[string]interface{})
		if !ok {
			continue
		}

		iface, _ := ifaceData["iface"].(string)
		if iface == "" {
			continue
		}

		// Only include configurable interface types (bridges, bonds, vlans)
		ifaceType, _ := ifaceData["type"].(string)
		if !isManageableInterfaceType(ifaceType) {
			continue
		}

		nativeID := fmt.Sprintf("%s/%s", node, iface)
		nativeIDs = append(nativeIDs, nativeID)
	}

	return &resource.ListResult{
		NativeIDs: nativeIDs,
	}, nil
}

// isManageableInterfaceType returns true for interface types that can be managed
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

// Helper functions

func createFailure(code resource.OperationErrorCode, msg string) *resource.CreateResult {
	return &resource.CreateResult{
		ProgressResult: &resource.ProgressResult{
			Operation:       resource.OperationCreate,
			OperationStatus: resource.OperationStatusFailure,
			ErrorCode:       code,
			StatusMessage:   msg,
		},
	}
}

func updateFailure(nativeID string, code resource.OperationErrorCode, msg string) *resource.UpdateResult {
	return &resource.UpdateResult{
		ProgressResult: &resource.ProgressResult{
			Operation:       resource.OperationUpdate,
			OperationStatus: resource.OperationStatusFailure,
			ErrorCode:       code,
			StatusMessage:   msg,
			NativeID:        nativeID,
		},
	}
}

func handleTransportError(err error) *resource.CreateResult {
	if transportErr, ok := err.(*transport.Error); ok {
		return &resource.CreateResult{
			ProgressResult: &resource.ProgressResult{
				Operation:       resource.OperationCreate,
				OperationStatus: resource.OperationStatusFailure,
				ErrorCode:       transport.ToResourceErrorCode(transportErr.Code),
				StatusMessage:   transportErr.Message,
			},
		}
	}
	return createFailure(resource.OperationErrorCodeServiceInternalError, err.Error())
}

// convertForProxmoxAPI converts values to Proxmox API compatible formats.
// Proxmox expects booleans as 0/1 integers, not true/false.
func convertForProxmoxAPI(v interface{}) interface{} {
	switch val := v.(type) {
	case bool:
		if val {
			return 1
		}
		return 0
	default:
		return v
	}
}

func init() {
	registry.Register(
		NetworkResourceType,
		[]resource.Operation{
			resource.OperationCreate,
			resource.OperationRead,
			resource.OperationUpdate,
			resource.OperationDelete,
			resource.OperationList,
			resource.OperationCheckStatus,
		},
		func(client *transport.Client, node string) prov.Provisioner {
			return &networkProvisioner{client: client, node: node}
		},
	)
}
