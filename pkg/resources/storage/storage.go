// SPDX-License-Identifier: Apache-2.0

package storage

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/platform-engineering-labs/formae-plugin-proxmox/pkg/resources/prov"
	"github.com/platform-engineering-labs/formae-plugin-proxmox/pkg/resources/registry"
	"github.com/platform-engineering-labs/formae-plugin-proxmox/pkg/transport"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

const StorageResourceType = "Proxmox::Storage::Storage"

// storageProvisioner handles storage configuration lifecycle operations
type storageProvisioner struct {
	client *transport.Client
	node   string
}

var _ prov.Provisioner = &storageProvisioner{}

// Create creates a new storage configuration.
// POST /storage
func (p *storageProvisioner) Create(ctx context.Context, request *resource.CreateRequest) (*resource.CreateResult, error) {
	var props map[string]interface{}
	if err := json.Unmarshal(request.Properties, &props); err != nil {
		return createFailure(resource.OperationErrorCodeInvalidRequest,
			fmt.Sprintf("failed to parse properties: %v", err)), nil
	}

	storageID, ok := props["storage"].(string)
	if !ok || storageID == "" {
		return createFailure(resource.OperationErrorCodeInvalidRequest,
			"storage identifier is required"), nil
	}

	storageType, ok := props["type"].(string)
	if !ok || storageType == "" {
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

	path := "/storage"
	_, err := p.client.Post(ctx, path, params)
	if err != nil {
		return handleTransportError(err), nil
	}

	// Read back the created storage configuration
	configPath := fmt.Sprintf("/storage/%s", storageID)
	configResp, err := p.client.Get(ctx, configPath)
	var propsJSON json.RawMessage
	if err == nil && configResp.Data != nil {
		propsJSON, _ = json.Marshal(configResp.Data)
	}

	return &resource.CreateResult{
		ProgressResult: &resource.ProgressResult{
			Operation:          resource.OperationCreate,
			OperationStatus:    resource.OperationStatusSuccess,
			NativeID:           storageID,
			ResourceProperties: propsJSON,
		},
	}, nil
}

// Read retrieves storage configuration.
// GET /storage/{storage}
func (p *storageProvisioner) Read(ctx context.Context, request *resource.ReadRequest) (*resource.ReadResult, error) {
	storageID := request.NativeID
	if storageID == "" {
		return &resource.ReadResult{
			ErrorCode: resource.OperationErrorCodeInvalidRequest,
		}, nil
	}

	path := fmt.Sprintf("/storage/%s", storageID)
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

// Update modifies storage configuration.
// PUT /storage/{storage}
func (p *storageProvisioner) Update(ctx context.Context, request *resource.UpdateRequest) (*resource.UpdateResult, error) {
	storageID := request.NativeID
	if storageID == "" {
		return updateFailure(request.NativeID, resource.OperationErrorCodeInvalidRequest,
			"storage ID is required"), nil
	}

	var props map[string]interface{}
	if err := json.Unmarshal(request.DesiredProperties, &props); err != nil {
		return updateFailure(request.NativeID, resource.OperationErrorCodeInvalidRequest,
			fmt.Sprintf("failed to parse properties: %v", err)), nil
	}

	// Filter out read-only and create-only properties
	readOnlyFields := map[string]bool{
		"storage": true,
		"type":    true,
		"digest":  true,
	}
	params := make(map[string]interface{})
	for k, v := range props {
		if v == nil || readOnlyFields[k] {
			continue
		}
		// Proxmox API expects booleans as 0/1 integers
		params[k] = convertForProxmoxAPI(v)
	}

	path := fmt.Sprintf("/storage/%s", storageID)
	_, err := p.client.Put(ctx, path, params)
	if err != nil {
		if transportErr, ok := err.(*transport.Error); ok {
			return updateFailure(request.NativeID,
				transport.ToResourceErrorCode(transportErr.Code), transportErr.Message), nil
		}
		return updateFailure(request.NativeID,
			resource.OperationErrorCodeServiceInternalError, err.Error()), nil
	}

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

// Delete removes a storage configuration.
// DELETE /storage/{storage}
func (p *storageProvisioner) Delete(ctx context.Context, request *resource.DeleteRequest) (*resource.DeleteResult, error) {
	storageID := request.NativeID
	if storageID == "" {
		return &resource.DeleteResult{
			ProgressResult: &resource.ProgressResult{
				Operation:       resource.OperationDelete,
				OperationStatus: resource.OperationStatusFailure,
				ErrorCode:       resource.OperationErrorCodeInvalidRequest,
				StatusMessage:   "storage ID is required",
			},
		}, nil
	}

	path := fmt.Sprintf("/storage/%s", storageID)
	_, err := p.client.Delete(ctx, path)
	if err != nil {
		if transportErr, ok := err.(*transport.Error); ok {
			// If already deleted, treat as success
			if transportErr.Code == transport.ErrorCodeResourceNotFound {
				return &resource.DeleteResult{
					ProgressResult: &resource.ProgressResult{
						Operation:       resource.OperationDelete,
						OperationStatus: resource.OperationStatusSuccess,
						NativeID:        storageID,
					},
				}, nil
			}
			return &resource.DeleteResult{
				ProgressResult: &resource.ProgressResult{
					Operation:       resource.OperationDelete,
					OperationStatus: resource.OperationStatusFailure,
					ErrorCode:       transport.ToResourceErrorCode(transportErr.Code),
					StatusMessage:   transportErr.Message,
					NativeID:        storageID,
				},
			}, nil
		}
		return &resource.DeleteResult{
			ProgressResult: &resource.ProgressResult{
				Operation:       resource.OperationDelete,
				OperationStatus: resource.OperationStatusFailure,
				ErrorCode:       resource.OperationErrorCodeServiceInternalError,
				StatusMessage:   err.Error(),
				NativeID:        storageID,
			},
		}, nil
	}

	return &resource.DeleteResult{
		ProgressResult: &resource.ProgressResult{
			Operation:       resource.OperationDelete,
			OperationStatus: resource.OperationStatusSuccess,
			NativeID:        storageID,
		},
	}, nil
}

// Status checks the current state of a storage configuration.
func (p *storageProvisioner) Status(ctx context.Context, request *resource.StatusRequest) (*resource.StatusResult, error) {
	storageID := request.NativeID
	if storageID == "" {
		return &resource.StatusResult{
			ProgressResult: &resource.ProgressResult{
				Operation:       resource.OperationCheckStatus,
				OperationStatus: resource.OperationStatusFailure,
				ErrorCode:       resource.OperationErrorCodeInvalidRequest,
				StatusMessage:   "storage ID is required",
			},
		}, nil
	}

	path := fmt.Sprintf("/storage/%s", storageID)
	resp, err := p.client.Get(ctx, path)
	if err != nil {
		if transportErr, ok := err.(*transport.Error); ok {
			if transportErr.Code == transport.ErrorCodeResourceNotFound {
				return &resource.StatusResult{
					ProgressResult: &resource.ProgressResult{
						Operation:       resource.OperationCheckStatus,
						OperationStatus: resource.OperationStatusFailure,
						ErrorCode:       resource.OperationErrorCodeNotFound,
						NativeID:        storageID,
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
				NativeID:        storageID,
			},
		}, nil
	}

	propsJSON, _ := json.Marshal(resp.Data)

	return &resource.StatusResult{
		ProgressResult: &resource.ProgressResult{
			Operation:          resource.OperationCheckStatus,
			OperationStatus:    resource.OperationStatusSuccess,
			NativeID:           storageID,
			ResourceProperties: propsJSON,
		},
	}, nil
}

// List returns all storage configurations.
// GET /storage
func (p *storageProvisioner) List(ctx context.Context, request *resource.ListRequest) (*resource.ListResult, error) {
	path := "/storage"
	resp, err := p.client.Get(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("failed to list storage: %w", err)
	}

	var nativeIDs []string
	for _, item := range resp.DataArray {
		storageData, ok := item.(map[string]interface{})
		if !ok {
			continue
		}

		storageID, _ := storageData["storage"].(string)
		if storageID == "" {
			continue
		}

		nativeIDs = append(nativeIDs, storageID)
	}

	return &resource.ListResult{
		NativeIDs: nativeIDs,
	}, nil
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
		StorageResourceType,
		[]resource.Operation{
			resource.OperationCreate,
			resource.OperationRead,
			resource.OperationUpdate,
			resource.OperationDelete,
			resource.OperationList,
			resource.OperationCheckStatus,
		},
		func(client *transport.Client, node string) prov.Provisioner {
			return &storageProvisioner{client: client, node: node}
		},
	)
}
