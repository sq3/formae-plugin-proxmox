// SPDX-License-Identifier: Apache-2.0

package compute

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/platform-engineering-labs/formae-plugin-proxmox/pkg/resources/prov"
	"github.com/platform-engineering-labs/formae-plugin-proxmox/pkg/resources/registry"
	"github.com/platform-engineering-labs/formae-plugin-proxmox/pkg/transport"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

const VMResourceType = "Proxmox::Compute::VM"

// vmProvisioner handles QEMU VM lifecycle operations
type vmProvisioner struct {
	client *transport.Client
	node   string
}

var _ prov.Provisioner = &vmProvisioner{}

// Create creates a new QEMU VM.
// POST /nodes/{node}/qemu → returns task UPID, polls until done.
func (p *vmProvisioner) Create(ctx context.Context, request *resource.CreateRequest) (*resource.CreateResult, error) {
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

	// vmid is required
	vmid, ok := extractVMID(props)
	if !ok {
		return createFailure(resource.OperationErrorCodeInvalidRequest,
			"vmid is required"), nil
	}

	// Build request parameters
	params := make(map[string]interface{})
	for k, v := range props {
		if v == nil {
			continue
		}
		// Proxmox API expects booleans as 0/1 integers
		params[k] = convertForProxmoxAPI(v)
	}

	path := fmt.Sprintf("/nodes/%s/qemu", node)
	resp, err := p.client.Post(ctx, path, params)
	if err != nil {
		return handleTransportError(err, resource.OperationCreate), nil
	}

	// Response contains UPID as task identifier
	upid, _ := resp.Data["value"].(string)
	if upid != "" {
		if err := p.client.WaitForTask(ctx, node, upid); err != nil {
			return createFailure(resource.OperationErrorCodeServiceInternalError,
				fmt.Sprintf("VM creation task failed: %v", err)), nil
		}
	}

	nativeID := fmt.Sprintf("%s/%d", node, vmid)

	// Read the created VM config to return properties
	configPath := fmt.Sprintf("/nodes/%s/qemu/%d/config", node, vmid)
	configResp, err := p.client.Get(ctx, configPath)
	var propsJSON json.RawMessage
	if err == nil && configResp.Data != nil {
		configResp.Data["vmid"] = vmid
		propsJSON, _ = json.Marshal(configResp.Data)
	}

	return &resource.CreateResult{
		ProgressResult: &resource.ProgressResult{
			Operation:          resource.OperationCreate,
			OperationStatus:    resource.OperationStatusInProgress,
			NativeID:           nativeID,
			ResourceProperties: propsJSON,
		},
	}, nil
}

// Read retrieves VM configuration and merges with current status.
// GET /nodes/{node}/qemu/{vmid}/config + /status/current
func (p *vmProvisioner) Read(ctx context.Context, request *resource.ReadRequest) (*resource.ReadResult, error) {
	node, vmid, err := parseNativeID(request.NativeID)
	if err != nil {
		return &resource.ReadResult{
			ErrorCode: resource.OperationErrorCodeInvalidRequest,
		}, nil
	}

	// Get VM configuration
	configPath := fmt.Sprintf("/nodes/%s/qemu/%s/config", node, vmid)
	configResp, err := p.client.Get(ctx, configPath)
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

	result := configResp.Data
	if result == nil {
		result = make(map[string]interface{})
	}
	result["vmid"] = vmid

	// Merge status info
	statusPath := fmt.Sprintf("/nodes/%s/qemu/%s/status/current", node, vmid)
	statusResp, err := p.client.Get(ctx, statusPath)
	if err == nil && statusResp.Data != nil {
		// Add status fields as read-only properties
		for _, field := range []string{"status", "uptime", "cpu", "mem", "maxmem", "maxdisk",
			"netin", "netout", "diskread", "diskwrite", "pid", "qmpstatus"} {
			if v, ok := statusResp.Data[field]; ok {
				result[field] = v
			}
		}
	}

	propsJSON, _ := json.Marshal(result)
	return &resource.ReadResult{
		Properties: string(propsJSON),
	}, nil
}

// Update modifies VM configuration.
// PUT /nodes/{node}/qemu/{vmid}/config
func (p *vmProvisioner) Update(ctx context.Context, request *resource.UpdateRequest) (*resource.UpdateResult, error) {
	node, vmid, err := parseNativeID(request.NativeID)
	if err != nil {
		return updateFailure(request.NativeID, resource.OperationErrorCodeInvalidRequest,
			fmt.Sprintf("invalid native ID: %v", err)), nil
	}

	var props map[string]interface{}
	if err := json.Unmarshal(request.DesiredProperties, &props); err != nil {
		return updateFailure(request.NativeID, resource.OperationErrorCodeInvalidRequest,
			fmt.Sprintf("failed to parse properties: %v", err)), nil
	}

	// Filter out read-only and nil properties
	params := make(map[string]interface{})
	readOnlyFields := map[string]bool{
		"vmid": true, "status": true, "uptime": true, "cpu": true,
		"mem": true, "maxmem": true, "maxdisk": true, "netin": true,
		"netout": true, "diskread": true, "diskwrite": true, "pid": true,
		"qmpstatus": true, "digest": true,
	}
	for k, v := range props {
		if v == nil || readOnlyFields[k] {
			continue
		}
		// Proxmox API expects booleans as 0/1 integers
		params[k] = convertForProxmoxAPI(v)
	}

	path := fmt.Sprintf("/nodes/%s/qemu/%s/config", node, vmid)
	_, err = p.client.Put(ctx, path, params)
	if err != nil {
		if transportErr, ok := err.(*transport.Error); ok {
			return updateFailure(request.NativeID,
				transport.ToResourceErrorCode(transportErr.Code), transportErr.Message), nil
		}
		return updateFailure(request.NativeID,
			resource.OperationErrorCodeServiceInternalError, err.Error()), nil
	}

	// Read back the updated config
	configResp, err := p.client.Get(ctx, path)
	var propsJSON json.RawMessage
	if err == nil && configResp.Data != nil {
		configResp.Data["vmid"] = vmid
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

// Delete removes a VM. Stops it first if running.
// DELETE /nodes/{node}/qemu/{vmid}
func (p *vmProvisioner) Delete(ctx context.Context, request *resource.DeleteRequest) (*resource.DeleteResult, error) {
	node, vmid, err := parseNativeID(request.NativeID)
	if err != nil {
		return deleteFailure(request.NativeID, resource.OperationErrorCodeInvalidRequest,
			fmt.Sprintf("invalid native ID: %v", err)), nil
	}

	// Check if VM is running and stop it first
	statusPath := fmt.Sprintf("/nodes/%s/qemu/%s/status/current", node, vmid)
	statusResp, err := p.client.Get(ctx, statusPath)
	if err != nil {
		if transportErr, ok := err.(*transport.Error); ok {
			if transportErr.Code == transport.ErrorCodeResourceNotFound {
				// Already deleted
				return &resource.DeleteResult{
					ProgressResult: &resource.ProgressResult{
						Operation:       resource.OperationDelete,
						OperationStatus: resource.OperationStatusSuccess,
						NativeID:        request.NativeID,
					},
				}, nil
			}
		}
	}

	if statusResp != nil && statusResp.Data != nil {
		status, _ := statusResp.Data["status"].(string)
		if status == "running" {
			// Stop the VM first
			stopPath := fmt.Sprintf("/nodes/%s/qemu/%s/status/stop", node, vmid)
			stopResp, err := p.client.Post(ctx, stopPath, nil)
			if err == nil {
				upid, _ := stopResp.Data["value"].(string)
				if upid != "" {
					_ = p.client.WaitForTask(ctx, node, upid)
				}
			}
		}
	}

	// Delete the VM
	deletePath := fmt.Sprintf("/nodes/%s/qemu/%s", node, vmid)
	resp, err := p.client.Delete(ctx, deletePath)
	if err != nil {
		if transportErr, ok := err.(*transport.Error); ok {
			if transportErr.Code == transport.ErrorCodeResourceNotFound {
				return &resource.DeleteResult{
					ProgressResult: &resource.ProgressResult{
						Operation:       resource.OperationDelete,
						OperationStatus: resource.OperationStatusSuccess,
						NativeID:        request.NativeID,
					},
				}, nil
			}
			return deleteFailure(request.NativeID,
				transport.ToResourceErrorCode(transportErr.Code), transportErr.Message), nil
		}
		return deleteFailure(request.NativeID,
			resource.OperationErrorCodeServiceInternalError, err.Error()), nil
	}

	// Wait for delete task to complete
	upid, _ := resp.Data["value"].(string)
	if upid != "" {
		if err := p.client.WaitForTask(ctx, node, upid); err != nil {
			return deleteFailure(request.NativeID,
				resource.OperationErrorCodeServiceInternalError,
				fmt.Sprintf("delete task failed: %v", err)), nil
		}
	}

	return &resource.DeleteResult{
		ProgressResult: &resource.ProgressResult{
			Operation:       resource.OperationDelete,
			OperationStatus: resource.OperationStatusSuccess,
			NativeID:        request.NativeID,
		},
	}, nil
}

// List returns all VMs on the configured node.
// GET /nodes/{node}/qemu
func (p *vmProvisioner) List(ctx context.Context, request *resource.ListRequest) (*resource.ListResult, error) {
	node := p.resolveNode(request.TargetConfig)
	if node == "" {
		return nil, fmt.Errorf("node is required for listing VMs")
	}

	path := fmt.Sprintf("/nodes/%s/qemu", node)
	resp, err := p.client.Get(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("failed to list VMs: %w", err)
	}

	var nativeIDs []string
	for _, item := range resp.DataArray {
		if vm, ok := item.(map[string]interface{}); ok {
			vmid := extractVMIDFromResponse(vm)
			if vmid != "" {
				nativeIDs = append(nativeIDs, fmt.Sprintf("%s/%s", node, vmid))
			}
		}
	}

	return &resource.ListResult{
		NativeIDs: nativeIDs,
	}, nil
}

// Status checks if a VM is ready (no lock, creation complete).
// GET /nodes/{node}/qemu/{vmid}/status/current
func (p *vmProvisioner) Status(ctx context.Context, request *resource.StatusRequest) (*resource.StatusResult, error) {
	node, vmid, err := parseNativeID(request.NativeID)
	if err != nil {
		return &resource.StatusResult{
			ProgressResult: &resource.ProgressResult{
				Operation:       resource.OperationCheckStatus,
				OperationStatus: resource.OperationStatusFailure,
				ErrorCode:       resource.OperationErrorCodeInvalidRequest,
				StatusMessage:   fmt.Sprintf("invalid native ID: %v", err),
				RequestID:       request.RequestID,
				NativeID:        request.NativeID,
			},
		}, nil
	}

	statusPath := fmt.Sprintf("/nodes/%s/qemu/%s/status/current", node, vmid)
	statusResp, err := p.client.Get(ctx, statusPath)
	if err != nil {
		if transportErr, ok := err.(*transport.Error); ok {
			return &resource.StatusResult{
				ProgressResult: &resource.ProgressResult{
					Operation:       resource.OperationCheckStatus,
					OperationStatus: resource.OperationStatusFailure,
					ErrorCode:       transport.ToResourceErrorCode(transportErr.Code),
					StatusMessage:   transportErr.Message,
					RequestID:       request.RequestID,
					NativeID:        request.NativeID,
				},
			}, nil
		}
		return &resource.StatusResult{
			ProgressResult: &resource.ProgressResult{
				Operation:       resource.OperationCheckStatus,
				OperationStatus: resource.OperationStatusFailure,
				ErrorCode:       resource.OperationErrorCodeServiceInternalError,
				StatusMessage:   err.Error(),
				RequestID:       request.RequestID,
				NativeID:        request.NativeID,
			},
		}, nil
	}

	// Check if VM has a lock (still being created/modified)
	if lock, ok := statusResp.Data["lock"].(string); ok && lock != "" {
		return &resource.StatusResult{
			ProgressResult: &resource.ProgressResult{
				Operation:       resource.OperationCheckStatus,
				OperationStatus: resource.OperationStatusInProgress,
				StatusMessage:   fmt.Sprintf("VM is locked: %s", lock),
				RequestID:       request.RequestID,
				NativeID:        request.NativeID,
			},
		}, nil
	}

	// VM is ready - read full config and merge with status
	configPath := fmt.Sprintf("/nodes/%s/qemu/%s/config", node, vmid)
	configResp, err := p.client.Get(ctx, configPath)

	result := make(map[string]interface{})
	if err == nil && configResp.Data != nil {
		for k, v := range configResp.Data {
			result[k] = v
		}
	}
	result["vmid"] = vmid

	// Merge status fields
	if statusResp.Data != nil {
		for _, field := range []string{"status", "uptime", "cpu", "mem", "maxmem", "maxdisk",
			"netin", "netout", "diskread", "diskwrite", "pid", "qmpstatus"} {
			if v, ok := statusResp.Data[field]; ok {
				result[field] = v
			}
		}
	}

	propsJSON, _ := json.Marshal(result)

	return &resource.StatusResult{
		ProgressResult: &resource.ProgressResult{
			Operation:          resource.OperationCheckStatus,
			OperationStatus:    resource.OperationStatusSuccess,
			RequestID:          request.RequestID,
			NativeID:           request.NativeID,
			ResourceProperties: propsJSON,
		},
	}, nil
}

// resolveNode gets the node from target config or falls back to the provisioner default
func (p *vmProvisioner) resolveNode(targetConfig json.RawMessage) string {
	if len(targetConfig) > 0 {
		var cfg map[string]interface{}
		if json.Unmarshal(targetConfig, &cfg) == nil {
			for _, field := range []string{"Node", "node"} {
				if val, ok := cfg[field].(string); ok && val != "" {
					return val
				}
			}
		}
	}
	return p.node
}

// parseNativeID parses "node/vmid" format
func parseNativeID(nativeID string) (node string, vmid string, err error) {
	parts := strings.SplitN(nativeID, "/", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid native ID format, expected node/vmid: %s", nativeID)
	}
	return parts[0], parts[1], nil
}

// extractVMID extracts vmid from properties as an integer
func extractVMID(props map[string]interface{}) (int, bool) {
	switch v := props["vmid"].(type) {
	case float64:
		return int(v), true
	case int:
		return v, true
	case json.Number:
		n, err := v.Int64()
		if err == nil {
			return int(n), true
		}
	}
	return 0, false
}

// extractVMIDFromResponse extracts vmid from a list response entry
func extractVMIDFromResponse(vm map[string]interface{}) string {
	switch v := vm["vmid"].(type) {
	case float64:
		return fmt.Sprintf("%d", int(v))
	case string:
		return v
	default:
		return fmt.Sprintf("%v", v)
	}
}

// Helper functions for creating failure results

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

func deleteFailure(nativeID string, code resource.OperationErrorCode, msg string) *resource.DeleteResult {
	return &resource.DeleteResult{
		ProgressResult: &resource.ProgressResult{
			Operation:       resource.OperationDelete,
			OperationStatus: resource.OperationStatusFailure,
			ErrorCode:       code,
			StatusMessage:   msg,
			NativeID:        nativeID,
		},
	}
}

func handleTransportError(err error, operation resource.Operation) *resource.CreateResult {
	if transportErr, ok := err.(*transport.Error); ok {
		return &resource.CreateResult{
			ProgressResult: &resource.ProgressResult{
				Operation:       operation,
				OperationStatus: resource.OperationStatusFailure,
				ErrorCode:       transport.ToResourceErrorCode(transportErr.Code),
				StatusMessage:   transportErr.Message,
			},
		}
	}
	return createFailure(resource.OperationErrorCodeServiceInternalError, err.Error())
}

func init() {
	registry.Register(
		VMResourceType,
		[]resource.Operation{
			resource.OperationCreate,
			resource.OperationRead,
			resource.OperationUpdate,
			resource.OperationDelete,
			resource.OperationList,
			resource.OperationCheckStatus,
		},
		func(client *transport.Client, node string) prov.Provisioner {
			return &vmProvisioner{client: client, node: node}
		},
	)
}
