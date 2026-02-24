/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package networkconfiguration provides NetworkConfiguration CRD management for machines
package networkconfiguration

import (
	"context"
	"fmt"
	"strconv"

	vitistackcrdsv1alpha1 "github.com/vitistack/common/pkg/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// Manager handles NetworkConfiguration CRD operations for machines
type Manager struct {
	client client.Client
}

// NewManager creates a new NetworkConfiguration manager
func NewManager(c client.Client) *Manager {
	return &Manager{
		client: c,
	}
}

// NetworkConfig holds network configuration for creating NetworkConfiguration CRDs
type NetworkConfig struct {
	// MACAddress is the MAC address for the network interface
	MACAddress string
	// VLAN is the VLAN tag (0 means no VLAN)
	VLAN int
}

// Create creates a NetworkConfiguration CRD for the machine with the given network config
// This is used when IP_SOURCE is set to "networkconfiguration" to get IP from Kea DHCP reservations
func (m *Manager) Create(ctx context.Context, machine *vitistackcrdsv1alpha1.Machine, netConfig NetworkConfig) error {
	logger := logf.FromContext(ctx)

	// Check if NetworkConfiguration already exists
	existingNetConf := &vitistackcrdsv1alpha1.NetworkConfiguration{}
	err := m.client.Get(ctx, client.ObjectKey{Name: machine.Name, Namespace: machine.Namespace}, existingNetConf)

	if err == nil {
		// NetworkConfiguration already exists
		logger.Info("NetworkConfiguration already exists", "name", machine.Name)
		return nil
	}

	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to check existing NetworkConfiguration: %w", err)
	}

	// Build network interface with MAC and optional VLAN
	netInterface := vitistackcrdsv1alpha1.NetworkConfigurationInterface{
		Name:       "net0",
		MacAddress: netConfig.MACAddress,
	}
	if netConfig.VLAN > 0 {
		netInterface.Vlan = strconv.Itoa(netConfig.VLAN)
	}

	// Create NetworkConfiguration
	netConf := &vitistackcrdsv1alpha1.NetworkConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name:      machine.Name,
			Namespace: machine.Namespace,
			Labels: map[string]string{
				"vitistack.io/machine":  machine.Name,
				"vitistack.io/operator": "proxmox-operator",
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion:         machine.APIVersion,
					Kind:               machine.Kind,
					Name:               machine.Name,
					UID:                machine.UID,
					Controller:         new(true),
					BlockOwnerDeletion: new(true),
				},
			},
		},
		Spec: vitistackcrdsv1alpha1.NetworkConfigurationSpec{
			Name:              machine.Name,
			Provider:          "proxmox",
			NetworkInterfaces: []vitistackcrdsv1alpha1.NetworkConfigurationInterface{netInterface},
		},
	}

	if err := m.client.Create(ctx, netConf); err != nil {
		return fmt.Errorf("failed to create NetworkConfiguration: %w", err)
	}

	logger.Info("Created NetworkConfiguration", "name", machine.Name, "macAddress", netConfig.MACAddress, "vlan", netConfig.VLAN)
	return nil
}

// GetIP retrieves the IPv4 address from a NetworkConfiguration's status
// Returns empty string if no IP is available yet
func (m *Manager) GetIP(ctx context.Context, machine *vitistackcrdsv1alpha1.Machine) (string, error) {
	logger := logf.FromContext(ctx)

	netConf := &vitistackcrdsv1alpha1.NetworkConfiguration{}
	err := m.client.Get(ctx, client.ObjectKey{Name: machine.Name, Namespace: machine.Namespace}, netConf)

	if err != nil {
		if apierrors.IsNotFound(err) {
			return "", nil // NetworkConfiguration doesn't exist yet
		}
		return "", fmt.Errorf("failed to get NetworkConfiguration: %w", err)
	}

	// Check status for network interfaces with IP addresses
	if len(netConf.Status.NetworkInterfaces) > 0 {
		for _, iface := range netConf.Status.NetworkInterfaces {
			if len(iface.IPv4Addresses) > 0 {
				ip := iface.IPv4Addresses[0]
				logger.Info("Got IP from NetworkConfiguration", "name", machine.Name, "ip", ip)
				return ip, nil
			}
		}
	}

	return "", nil // No IP available yet
}

// Delete deletes the NetworkConfiguration for a machine
func (m *Manager) Delete(ctx context.Context, machine *vitistackcrdsv1alpha1.Machine) error {
	logger := logf.FromContext(ctx)

	netConf := &vitistackcrdsv1alpha1.NetworkConfiguration{}
	err := m.client.Get(ctx, client.ObjectKey{Name: machine.Name, Namespace: machine.Namespace}, netConf)

	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil // Already deleted
		}
		return fmt.Errorf("failed to get NetworkConfiguration: %w", err)
	}

	if err := m.client.Delete(ctx, netConf); err != nil {
		if apierrors.IsNotFound(err) {
			return nil // Already deleted
		}
		return fmt.Errorf("failed to delete NetworkConfiguration: %w", err)
	}

	logger.Info("Deleted NetworkConfiguration", "name", machine.Name)
	return nil
}

// boolPtr returns a pointer to a bool value
//
//go:fix inline
func boolPtr(b bool) *bool {
	return new(b)
}
