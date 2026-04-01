// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"

	"github.com/platform-engineering-labs/formae-plugin-proxmox/pkg/config"
	"github.com/platform-engineering-labs/formae-plugin-proxmox/pkg/resources/prov"
	"github.com/platform-engineering-labs/formae-plugin-proxmox/pkg/resources/registry"
	"github.com/platform-engineering-labs/formae-plugin-proxmox/pkg/transport"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"

	// Import resource packages to trigger init() registration
	_ "github.com/platform-engineering-labs/formae-plugin-proxmox/pkg/resources/compute"
	_ "github.com/platform-engineering-labs/formae-plugin-proxmox/pkg/resources/network"
	_ "github.com/platform-engineering-labs/formae-plugin-proxmox/pkg/resources/storage"
)

// Plugin implements the Formae ResourcePlugin interface.
// The SDK automatically provides identity methods (Name, Version, Namespace)
// and schema methods (SupportedResources, SchemaForResourceType) by reading
// formae-plugin.pkl and schema/pkl/ at startup.
type Plugin struct{}

// Compile-time check: Plugin must satisfy ResourcePlugin interface.
var _ plugin.ResourcePlugin = &Plugin{}

// RateLimit returns the rate limit configuration for this plugin
func (p *Plugin) RateLimit() plugin.RateLimitConfig {
	return plugin.RateLimitConfig{
		Scope:                            plugin.RateLimitScopeNamespace,
		MaxRequestsPerSecondForNamespace: 5,
	}
}

// DiscoveryFilters returns declarative filters for discovery.
func (p *Plugin) DiscoveryFilters() []plugin.MatchFilter {
	return nil
}

// LabelConfig returns the label extraction configuration for discovered Proxmox resources.
func (p *Plugin) LabelConfig() plugin.LabelConfig {
	return plugin.LabelConfig{
		DefaultQuery: "$.name",
	}
}

// getProvisionerForType returns a provisioner for a specific resource type
func (p *Plugin) getProvisionerForType(resourceType string, targetConfig []byte) (prov.Provisioner, error) {
	if !registry.HasProvisioner(resourceType) {
		return nil, fmt.Errorf("unsupported resource type: %s", resourceType)
	}

	cfg, err := config.FromTargetConfig(targetConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to extract config: %w", err)
	}

	client, err := transport.NewClient(&transport.ClientConfig{
		ApiUrl:  cfg.ApiUrl,
		TokenID: cfg.TokenID,
		Secret:  cfg.Secret,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create Proxmox API client: %w", err)
	}

	factory, ok := registry.GetFactory(resourceType)
	if !ok {
		return nil, fmt.Errorf("no factory registered for resource type: %s", resourceType)
	}

	return factory(client, cfg.Node), nil
}

func (p *Plugin) Create(ctx context.Context, request *resource.CreateRequest) (*resource.CreateResult, error) {
	provisioner, err := p.getProvisionerForType(request.ResourceType, request.TargetConfig)
	if err != nil {
		return nil, err
	}
	return provisioner.Create(ctx, request)
}

func (p *Plugin) Read(ctx context.Context, request *resource.ReadRequest) (*resource.ReadResult, error) {
	provisioner, err := p.getProvisionerForType(request.ResourceType, request.TargetConfig)
	if err != nil {
		return nil, err
	}
	return provisioner.Read(ctx, request)
}

func (p *Plugin) Update(ctx context.Context, request *resource.UpdateRequest) (*resource.UpdateResult, error) {
	provisioner, err := p.getProvisionerForType(request.ResourceType, request.TargetConfig)
	if err != nil {
		return nil, err
	}
	return provisioner.Update(ctx, request)
}

func (p *Plugin) Delete(ctx context.Context, request *resource.DeleteRequest) (*resource.DeleteResult, error) {
	provisioner, err := p.getProvisionerForType(request.ResourceType, request.TargetConfig)
	if err != nil {
		return nil, err
	}
	return provisioner.Delete(ctx, request)
}

func (p *Plugin) Status(ctx context.Context, request *resource.StatusRequest) (*resource.StatusResult, error) {
	provisioner, err := p.getProvisionerForType(request.ResourceType, request.TargetConfig)
	if err != nil {
		return nil, err
	}
	return provisioner.Status(ctx, request)
}

func (p *Plugin) List(ctx context.Context, request *resource.ListRequest) (*resource.ListResult, error) {
	provisioner, err := p.getProvisionerForType(request.ResourceType, request.TargetConfig)
	if err != nil {
		return nil, err
	}
	return provisioner.List(ctx, request)
}
