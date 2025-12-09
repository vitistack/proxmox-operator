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

// Package vmbuilder provides functions for building Proxmox VM configurations
package vmbuilder

import (
	"fmt"

	"github.com/luthermonson/go-proxmox"
	"github.com/spf13/viper"
	vitistackcrdsv1alpha1 "github.com/vitistack/common/pkg/v1alpha1"
	"github.com/vitistack/proxmox-operator/internal/consts"
)

// Builder builds Proxmox VM options from Machine and MachineClass specs
type Builder struct{}

// NetworkConfig holds network configuration for VM creation
type NetworkConfig struct {
	// MACAddress is the MAC address for the network interface
	MACAddress string
	// VLAN is the VLAN tag (0 means no VLAN)
	VLAN int
	// MTU is the Maximum Transmission Unit (0 means use Proxmox default of 1500)
	MTU int
}

// NewBuilder creates a new VM options builder
func NewBuilder() *Builder {
	return &Builder{}
}

// BuildOptions builds Proxmox VM options from Machine and MachineClass specs
func (b *Builder) BuildOptions(machine *vitistackcrdsv1alpha1.Machine, machineClass *vitistackcrdsv1alpha1.MachineClass, netConfig NetworkConfig) []proxmox.VirtualMachineOption {
	options := []proxmox.VirtualMachineOption{}

	options = append(options, b.buildCPUOptions(machine, machineClass)...)
	options = append(options, b.buildMemoryOptions(machine, machineClass)...)
	options = append(options, b.buildStorageOptions(machine)...)
	options = append(options, b.buildNetworkOptions(machine, netConfig)...)
	options = append(options, b.buildOSOptions(machine)...)
	options = append(options, b.buildMiscOptions(machine)...)

	return options
}

// buildCPUOptions builds CPU-related VM options
func (b *Builder) buildCPUOptions(machine *vitistackcrdsv1alpha1.Machine, machineClass *vitistackcrdsv1alpha1.MachineClass) []proxmox.VirtualMachineOption {
	options := []proxmox.VirtualMachineOption{}

	// CPU cores
	if machine.Spec.CPU.Cores > 0 {
		options = append(options, proxmox.VirtualMachineOption{Name: "cores", Value: machine.Spec.CPU.Cores})
	} else if machineClass.Spec.CPU.Cores > 0 {
		options = append(options, proxmox.VirtualMachineOption{Name: "cores", Value: machineClass.Spec.CPU.Cores})
	}

	// CPU type
	cpuType := viper.GetString(consts.PROXMOX_DEFAULT_CPU_TYPE)
	if cpuType != "" {
		options = append(options, proxmox.VirtualMachineOption{Name: "cpu", Value: cpuType})
	}

	// NUMA
	if viper.GetBool(consts.PROXMOX_ENABLE_NUMA) {
		options = append(options, proxmox.VirtualMachineOption{Name: "numa", Value: 1})
	}

	return options
}

// buildMemoryOptions builds memory-related VM options
func (b *Builder) buildMemoryOptions(machine *vitistackcrdsv1alpha1.Machine, machineClass *vitistackcrdsv1alpha1.MachineClass) []proxmox.VirtualMachineOption {
	options := []proxmox.VirtualMachineOption{}

	memoryMB := int64(0)
	if machine.Spec.Memory > 0 {
		memoryMB = machine.Spec.Memory / (1024 * 1024) // Convert bytes to MB
	} else if machineClass.Spec.Memory.Quantity.Value() > 0 {
		memoryMB = machineClass.Spec.Memory.Quantity.Value() / (1024 * 1024)
	}

	if memoryMB > 0 {
		options = append(options, proxmox.VirtualMachineOption{Name: "memory", Value: memoryMB})
	}

	return options
}

// buildStorageOptions builds storage-related VM options
func (b *Builder) buildStorageOptions(machine *vitistackcrdsv1alpha1.Machine) []proxmox.VirtualMachineOption {
	options := []proxmox.VirtualMachineOption{}

	// SCSI Controller - required for SCSI disks
	scsiController := viper.GetString(consts.PROXMOX_SCSI_CONTROLLER)
	if scsiController != "" {
		options = append(options, proxmox.VirtualMachineOption{Name: "scsihw", Value: scsiController})
	}

	storagePool := viper.GetString(consts.PROXMOX_DEFAULT_STORAGE)
	diskCreated := false

	// Process disks from machine spec
	for i, disk := range machine.Spec.Disks {
		sizeGB := disk.SizeGB
		if sizeGB == 0 {
			sizeGB = consts.DefaultDiskSizeGB
		}

		// Determine disk slot name based on disk name or index
		var diskSlot string
		if disk.Name == "root" || disk.Boot || i == 0 {
			diskSlot = consts.DiskSlotSCSI0
		} else {
			diskSlot = fmt.Sprintf("scsi%d", i)
		}

		// Skip if we already created a disk for this slot
		if diskSlot == consts.DiskSlotSCSI0 && diskCreated {
			continue
		}

		options = append(options, proxmox.VirtualMachineOption{
			Name:  diskSlot,
			Value: fmt.Sprintf("%s:%d", storagePool, sizeGB),
		})

		if diskSlot == consts.DiskSlotSCSI0 {
			diskCreated = true
		}
	}

	// If no disks were specified, create a default boot disk
	if !diskCreated && len(machine.Spec.Disks) == 0 {
		options = append(options, proxmox.VirtualMachineOption{
			Name:  consts.DiskSlotSCSI0,
			Value: fmt.Sprintf("%s:%d", storagePool, consts.DefaultDiskSizeGB),
		})
	}

	return options
}

// buildNetworkOptions builds network-related VM options
func (b *Builder) buildNetworkOptions(machine *vitistackcrdsv1alpha1.Machine, netConfig NetworkConfig) []proxmox.VirtualMachineOption {
	options := []proxmox.VirtualMachineOption{}

	if len(machine.Spec.Network.Interfaces) > 0 || netConfig.MACAddress != "" {
		networkBridge := viper.GetString(consts.PROXMOX_DEFAULT_NETWORK)
		networkModel := viper.GetString(consts.PROXMOX_NETWORK_MODEL)

		if networkModel == "" {
			networkModel = consts.DefaultNetworkModel
		}

		// Build network value: model,bridge=<bridge>[,macaddr=<mac>][,tag=<vlan>]
		netValue := fmt.Sprintf("%s,bridge=%s", networkModel, networkBridge)

		if netConfig.MACAddress != "" {
			netValue = fmt.Sprintf("%s,macaddr=%s", netValue, netConfig.MACAddress)
		}

		// Add VLAN tag if specified (from config or default)
		vlanTag := netConfig.VLAN
		if vlanTag == 0 {
			vlanTag = viper.GetInt(consts.PROXMOX_DEFAULT_VLAN)
		}
		if vlanTag > 0 {
			netValue = fmt.Sprintf("%s,tag=%d", netValue, vlanTag)
		}

		// Add MTU if specified (from config or default, 0 means use Proxmox default)
		mtu := netConfig.MTU
		if mtu == 0 {
			mtu = viper.GetInt(consts.PROXMOX_DEFAULT_MTU)
		}
		if mtu > 0 {
			netValue = fmt.Sprintf("%s,mtu=%d", netValue, mtu)
		}

		options = append(options, proxmox.VirtualMachineOption{Name: consts.NetSlot0, Value: netValue})
	}

	return options
}

// buildOSOptions builds OS-related VM options
func (b *Builder) buildOSOptions(machine *vitistackcrdsv1alpha1.Machine) []proxmox.VirtualMachineOption {
	options := []proxmox.VirtualMachineOption{}

	if machine.Spec.OS.ImageID != "" {
		options = append(options, proxmox.VirtualMachineOption{Name: "cdrom", Value: machine.Spec.OS.ImageID})
	}

	return options
}

// buildMiscOptions builds miscellaneous VM options
func (b *Builder) buildMiscOptions(machine *vitistackcrdsv1alpha1.Machine) []proxmox.VirtualMachineOption {
	options := []proxmox.VirtualMachineOption{}

	// Name
	if machine.Spec.Name != "" {
		options = append(options, proxmox.VirtualMachineOption{Name: "name", Value: machine.Spec.Name})
	}

	// QEMU Guest Agent
	if viper.GetBool(consts.PROXMOX_ENABLE_QEMU_AGENT) {
		options = append(options, proxmox.VirtualMachineOption{Name: "agent", Value: 1})
	}

	// Start VM after creation
	if viper.GetBool(consts.PROXMOX_START_ON_CREATE) {
		options = append(options, proxmox.VirtualMachineOption{Name: "start", Value: 1})
	}

	return options
}
