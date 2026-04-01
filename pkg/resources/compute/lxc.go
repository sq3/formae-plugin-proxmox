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

const LXCResourceType = "Proxmox::Compute::LXC"

// lxcProvisioner handles LXC container lifecycle operations
type lxcProvisioner struct {
	client *transport.Client
	node   string
}

var _ prov.Provisioner = &lxcProvisioner{}

// Create creates a new LXC container.
// POST /nodes/{node}/lxc → returns task UPID, polls until done.
func (p *lxcProvisioner) Create(ctx context.Context, request *resource.CreateRequest) (*resource.CreateResult, error) {
	var props map[string]interface{}
	if err := json.Unmarshal(request.Properties, &props); err != nil {
		return lxcCreateFailure(resource.OperationErrorCodeInvalidRequest,
			fmt.Sprintf("failed to parse properties: %v", err)), nil
	}

	node := p.resolveNode(request.TargetConfig)
	if node == "" {
		return lxcCreateFailure(resource.OperationErrorCodeInvalidRequest,
			"node is required but not found in target config or PROXMOX_NODE"), nil
	}

	vmid, ok := extractVMID(props)
	if !ok {
		return lxcCreateFailure(resource.OperationErrorCodeInvalidRequest,
			"vmid is required"), nil
	}

	if _, ok := props["ostemplate"]; !ok {
		return lxcCreateFailure(resource.OperationErrorCodeInvalidRequest,
			"ostemplate is required"), nil
	}

	params := make(map[string]interface{})
	for k, v := range props {
		if v == nil {
			continue
		}
		// Proxmox API expects booleans as 0/1 integers
		params[k] = convertForProxmoxAPI(v)
	}

	path := fmt.Sprintf("/nodes/%s/lxc", node)
	resp, err := p.client.Post(ctx, path, params)
	if err != nil {
		return lxcHandleTransportError(err), nil
	}

	upid, _ := resp.Data["value"].(string)
	if upid != "" {
		if err := p.client.WaitForTask(ctx, node, upid); err != nil {
			return lxcCreateFailure(resource.OperationErrorCodeServiceInternalError,
				fmt.Sprintf("container creation task failed: %v", err)), nil
		}
	}

	nativeID := fmt.Sprintf("%s/%d", node, vmid)

	configPath := fmt.Sprintf("/nodes/%s/lxc/%d/config", node, vmid)
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

// Read retrieves container configuration and merges with current status.
// GET /nodes/{node}/lxc/{vmid}/config + /status/current
func (p *lxcProvisioner) Read(ctx context.Context, request *resource.ReadRequest) (*resource.ReadResult, error) {
	node, vmid, err := parseLXCNativeID(request.NativeID)
	if err != nil {
		return &resource.ReadResult{
			ErrorCode: resource.OperationErrorCodeInvalidRequest,
		}, nil
	}

	configPath := fmt.Sprintf("/nodes/%s/lxc/%s/config", node, vmid)
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

	statusPath := fmt.Sprintf("/nodes/%s/lxc/%s/status/current", node, vmid)
	statusResp, err := p.client.Get(ctx, statusPath)
	if err == nil && statusResp.Data != nil {
		for _, field := range []string{"status", "uptime", "cpu", "mem", "maxmem",
			"disk", "maxdisk", "swap", "maxswap", "netin", "netout", "pid"} {
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

// Update modifies container configuration.
// PUT /nodes/{node}/lxc/{vmid}/config
func (p *lxcProvisioner) Update(ctx context.Context, request *resource.UpdateRequest) (*resource.UpdateResult, error) {
	node, vmid, err := parseLXCNativeID(request.NativeID)
	if err != nil {
		return lxcUpdateFailure(request.NativeID, resource.OperationErrorCodeInvalidRequest,
			fmt.Sprintf("invalid native ID: %v", err)), nil
	}

	var props map[string]interface{}
	if err := json.Unmarshal(request.DesiredProperties, &props); err != nil {
		return lxcUpdateFailure(request.NativeID, resource.OperationErrorCodeInvalidRequest,
			fmt.Sprintf("failed to parse properties: %v", err)), nil
	}

	readOnlyFields := map[string]bool{
		"vmid": true, "ostemplate": true, "status": true, "uptime": true,
		"cpu": true, "mem": true, "maxmem": true, "disk": true, "maxdisk": true,
		"swap": true, "maxswap": true, "netin": true, "netout": true, "pid": true,
		"digest": true, "lxc": true,
	}
	params := make(map[string]interface{})
	for k, v := range props {
		if v == nil || readOnlyFields[k] {
			continue
		}
		// Proxmox API expects booleans as 0/1 integers
		params[k] = convertForProxmoxAPI(v)
	}

	path := fmt.Sprintf("/nodes/%s/lxc/%s/config", node, vmid)
	_, err = p.client.Put(ctx, path, params)
	if err != nil {
		if transportErr, ok := err.(*transport.Error); ok {
			return lxcUpdateFailure(request.NativeID,
				transport.ToResourceErrorCode(transportErr.Code), transportErr.Message), nil
		}
		return lxcUpdateFailure(request.NativeID,
			resource.OperationErrorCodeServiceInternalError, err.Error()), nil
	}

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

// Delete removes a container. Stops it first if running.
// DELETE /nodes/{node}/lxc/{vmid}
func (p *lxcProvisioner) Delete(ctx context.Context, request *resource.DeleteRequest) (*resource.DeleteResult, error) {
	node, vmid, err := parseLXCNativeID(request.NativeID)
	if err != nil {
		return lxcDeleteFailure(request.NativeID, resource.OperationErrorCodeInvalidRequest,
			fmt.Sprintf("invalid native ID: %v", err)), nil
	}

	statusPath := fmt.Sprintf("/nodes/%s/lxc/%s/status/current", node, vmid)
	statusResp, err := p.client.Get(ctx, statusPath)
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
		}
	}

	if statusResp != nil && statusResp.Data != nil {
		status, _ := statusResp.Data["status"].(string)
		if status == "running" {
			stopPath := fmt.Sprintf("/nodes/%s/lxc/%s/status/stop", node, vmid)
			stopResp, err := p.client.Post(ctx, stopPath, nil)
			if err == nil {
				upid, _ := stopResp.Data["value"].(string)
				if upid != "" {
					_ = p.client.WaitForTask(ctx, node, upid)
				}
			}
		}
	}

	deletePath := fmt.Sprintf("/nodes/%s/lxc/%s", node, vmid)
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
			return lxcDeleteFailure(request.NativeID,
				transport.ToResourceErrorCode(transportErr.Code), transportErr.Message), nil
		}
		return lxcDeleteFailure(request.NativeID,
			resource.OperationErrorCodeServiceInternalError, err.Error()), nil
	}

	upid, _ := resp.Data["value"].(string)
	if upid != "" {
		if err := p.client.WaitForTask(ctx, node, upid); err != nil {
			return lxcDeleteFailure(request.NativeID,
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

// List returns all LXC containers on the configured node.
// GET /nodes/{node}/lxc
func (p *lxcProvisioner) List(ctx context.Context, request *resource.ListRequest) (*resource.ListResult, error) {
	node := p.resolveNode(request.TargetConfig)
	if node == "" {
		return nil, fmt.Errorf("node is required for listing containers")
	}

	path := fmt.Sprintf("/nodes/%s/lxc", node)
	resp, err := p.client.Get(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("failed to list containers: %w", err)
	}

	var nativeIDs []string
	for _, item := range resp.DataArray {
		if ct, ok := item.(map[string]interface{}); ok {
			vmid := extractVMIDFromResponse(ct)
			if vmid != "" {
				nativeIDs = append(nativeIDs, fmt.Sprintf("%s/%s", node, vmid))
			}
		}
	}

	return &resource.ListResult{
		NativeIDs: nativeIDs,
	}, nil
}

// Status checks if a container is ready (no lock, creation complete).
// GET /nodes/{node}/lxc/{vmid}/status/current
func (p *lxcProvisioner) Status(ctx context.Context, request *resource.StatusRequest) (*resource.StatusResult, error) {
	node, vmid, err := parseLXCNativeID(request.NativeID)
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

	statusPath := fmt.Sprintf("/nodes/%s/lxc/%s/status/current", node, vmid)
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

	if lock, ok := statusResp.Data["lock"].(string); ok && lock != "" {
		return &resource.StatusResult{
			ProgressResult: &resource.ProgressResult{
				Operation:       resource.OperationCheckStatus,
				OperationStatus: resource.OperationStatusInProgress,
				StatusMessage:   fmt.Sprintf("container is locked: %s", lock),
				RequestID:       request.RequestID,
				NativeID:        request.NativeID,
			},
		}, nil
	}

	configPath := fmt.Sprintf("/nodes/%s/lxc/%s/config", node, vmid)
	configResp, err := p.client.Get(ctx, configPath)

	result := make(map[string]interface{})
	if err == nil && configResp.Data != nil {
		for k, v := range configResp.Data {
			result[k] = v
		}
	}
	result["vmid"] = vmid

	if statusResp.Data != nil {
		for _, field := range []string{"status", "uptime", "cpu", "mem", "maxmem",
			"disk", "maxdisk", "swap", "maxswap", "netin", "netout", "pid"} {
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
func (p *lxcProvisioner) resolveNode(targetConfig json.RawMessage) string {
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

// parseLXCNativeID parses "node/vmid" format
func parseLXCNativeID(nativeID string) (node string, vmid string, err error) {
	parts := strings.SplitN(nativeID, "/", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid native ID format, expected node/vmid: %s", nativeID)
	}
	return parts[0], parts[1], nil
}

// Helper functions for creating failure results

func lxcCreateFailure(code resource.OperationErrorCode, msg string) *resource.CreateResult {
	return &resource.CreateResult{
		ProgressResult: &resource.ProgressResult{
			Operation:       resource.OperationCreate,
			OperationStatus: resource.OperationStatusFailure,
			ErrorCode:       code,
			StatusMessage:   msg,
		},
	}
}

func lxcUpdateFailure(nativeID string, code resource.OperationErrorCode, msg string) *resource.UpdateResult {
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

func lxcDeleteFailure(nativeID string, code resource.OperationErrorCode, msg string) *resource.DeleteResult {
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

func lxcHandleTransportError(err error) *resource.CreateResult {
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
	return lxcCreateFailure(resource.OperationErrorCodeServiceInternalError, err.Error())
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
		LXCResourceType,
		[]resource.Operation{
			resource.OperationCreate,
			resource.OperationRead,
			resource.OperationUpdate,
			resource.OperationDelete,
			resource.OperationList,
			resource.OperationCheckStatus,
		},
		func(client *transport.Client, node string) prov.Provisioner {
			return &lxcProvisioner{client: client, node: node}
		},
	)
}
