// SPDX-License-Identifier: Apache-2.0

package registry

import (
	"sync"

	"github.com/platform-engineering-labs/formae-plugin-proxmox/pkg/resources/prov"
	"github.com/platform-engineering-labs/formae-plugin-proxmox/pkg/transport"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// ProvisionerFactory creates a provisioner using the Proxmox transport
type ProvisionerFactory func(client *transport.Client, node string) prov.Provisioner

type registration struct {
	operations []resource.Operation
	factory    ProvisionerFactory
}

var (
	mu            sync.RWMutex
	registrations = make(map[string]*registration)
)

// Register registers a resource type with a provisioner factory
func Register(resourceType string, operations []resource.Operation, factory ProvisionerFactory) {
	mu.Lock()
	defer mu.Unlock()
	registrations[resourceType] = &registration{
		operations: operations,
		factory:    factory,
	}
}

// GetFactory returns the provisioner factory for a resource type
func GetFactory(resourceType string) (ProvisionerFactory, bool) {
	mu.RLock()
	defer mu.RUnlock()
	reg, ok := registrations[resourceType]
	if !ok {
		return nil, false
	}
	return reg.factory, true
}

// GetOperations returns supported operations for a resource type
func GetOperations(resourceType string) []resource.Operation {
	mu.RLock()
	defer mu.RUnlock()
	reg, ok := registrations[resourceType]
	if !ok {
		return nil
	}
	return reg.operations
}

// HasProvisioner checks if a resource type is registered
func HasProvisioner(resourceType string) bool {
	mu.RLock()
	defer mu.RUnlock()
	_, ok := registrations[resourceType]
	return ok
}

// ResourceTypes returns all registered resource types
func ResourceTypes() []string {
	mu.RLock()
	defer mu.RUnlock()
	types := make([]string, 0, len(registrations))
	for t := range registrations {
		types = append(types, t)
	}
	return types
}
