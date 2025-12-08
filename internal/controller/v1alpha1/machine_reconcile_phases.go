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

package v1alpha1

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/viper"
	vitistackcrdsv1alpha1 "github.com/vitistack/common/pkg/v1alpha1"
	"github.com/vitistack/proxmox-operator/internal/consts"
	"github.com/vitistack/proxmox-operator/internal/helpers/network"
	"github.com/vitistack/proxmox-operator/internal/helpers/providerid"
	"github.com/vitistack/proxmox-operator/internal/services/networkconfiguration"
	"github.com/vitistack/proxmox-operator/internal/services/vmbuilder"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// reconcileCreate handles the creation of a new VM
func (r *MachineReconciler) reconcileCreate(ctx context.Context, machine *vitistackcrdsv1alpha1.Machine, machineClass *vitistackcrdsv1alpha1.MachineClass) (ctrl.Result, error) {
	logger := logf.FromContext(ctx)

	// Check if VM already exists (from previous reconciliation attempt)
	if err := r.checkExistingVM(ctx, machine); err == nil {
		return ctrl.Result{}, nil // VM exists, no need to create
	}

	// Update status to creating
	machine.Status.Phase = vitistackcrdsv1alpha1.MachinePhaseCreating
	r.setCondition(ctx, machine, "False", "Creating", "VM creation in progress")
	if err := r.updateStatusWithRetry(ctx, machine); err != nil {
		return ctrl.Result{}, err
	}

	// Select Proxmox node
	node, err := r.selectNode(ctx)
	if err != nil {
		logger.Error(err, "Failed to select Proxmox node")
		machine.Status.Phase = vitistackcrdsv1alpha1.MachinePhaseFailed
		r.setCondition(ctx, machine, "False", "NodeSelectionFailed", fmt.Sprintf("Failed to select Proxmox node: %v", err))
		_ = r.updateStatusWithRetry(ctx, machine)
		return ctrl.Result{}, err
	}

	// Generate VM ID and find an available one
	vmID := r.findAvailableVMID(ctx, machine, node)
	if vmID == 0 {
		logger.Error(fmt.Errorf("no available VMID found"), "Failed to find available VMID after max attempts")
		machine.Status.Phase = vitistackcrdsv1alpha1.MachinePhaseFailed
		machine.Status.Message = "No available VMID found - all IDs in range are in use"
		r.setCondition(ctx, machine, "False", "NoAvailableVMID", "No available VMID found after checking range")
		_ = r.updateStatusWithRetry(ctx, machine)
		return ctrl.Result{}, fmt.Errorf("no available VMID found")
	}

	// Generate MAC address
	macAddress, err := r.generateMACAddress(ctx)
	if err != nil {
		logger.Error(err, "Failed to generate MAC address")
		machine.Status.Phase = vitistackcrdsv1alpha1.MachinePhaseFailed
		machine.Status.Message = fmt.Sprintf("Failed to generate MAC address: %v", err)
		r.setCondition(ctx, machine, "False", "MACAddressGenerationFailed", fmt.Sprintf("Failed to generate MAC address: %v", err))
		_ = r.updateStatusWithRetry(ctx, machine)
		return ctrl.Result{}, err
	}

	// Determine VLAN from NetworkNamespace or default
	vlanTag := r.getVLANTag(ctx, machine)

	// Determine MTU from Machine spec or default
	mtu := r.getMTU(machine)

	// Build network config for VM builder
	netConfig := vmbuilder.NetworkConfig{
		MACAddress: macAddress,
		VLAN:       vlanTag,
		MTU:        mtu,
	}

	// Build VM options
	options := r.vmBuilder.BuildOptions(machine, machineClass, netConfig)

	// Validate ISO
	if machine.Spec.OS.ImageID != "" {
		if err := r.validateISO(ctx, node, machine.Spec.OS.ImageID); err != nil {
			logger.Error(err, "ISO validation failed")
			machine.Status.Phase = vitistackcrdsv1alpha1.MachinePhaseFailed
			machine.Status.Message = fmt.Sprintf("ISO validation failed: %v", err)
			r.setCondition(ctx, machine, "False", "ISOValidationFailed", fmt.Sprintf("ISO validation failed: %v", err))
			_ = r.updateStatusWithRetry(ctx, machine)
			return ctrl.Result{}, err
		}
	}

	// Create VM
	task, err := r.ProxmoxClient.CreateVM(ctx, node, vmID, options)
	if err != nil {
		logger.Error(err, "Failed to create VM")
		machine.Status.Phase = vitistackcrdsv1alpha1.MachinePhaseFailed
		machine.Status.Message = err.Error()
		r.setCondition(ctx, machine, "False", "VMCreationFailed", fmt.Sprintf("Failed to create VM: %v", err))
		_ = r.updateStatusWithRetry(ctx, machine)
		return ctrl.Result{}, err
	}

	// Wait for task completion
	if err := task.Wait(ctx, 5*time.Second, 5*time.Minute); err != nil {
		logger.Error(err, "VM creation task failed")
		machine.Status.Phase = vitistackcrdsv1alpha1.MachinePhaseFailed
		machine.Status.Message = err.Error()
		r.setCondition(ctx, machine, "False", "VMProvisioningFailed", fmt.Sprintf("VM creation task failed: %v", err))
		_ = r.updateStatusWithRetry(ctx, machine)
		return ctrl.Result{}, err
	}

	// Log task completion details for debugging
	logger.Info("VM creation task completed",
		"vmID", vmID,
		"node", node,
		"taskID", task.ID,
		"taskType", task.Type,
		"taskStatus", task.Status,
		"taskExitStatus", task.ExitStatus,
	)

	// Verify task exit status
	if task.ExitStatus != "OK" && task.ExitStatus != "" {
		errMsg := fmt.Sprintf("VM creation task exited with status: %s", task.ExitStatus)
		logger.Error(fmt.Errorf("%s", errMsg), "Task did not complete successfully")
		machine.Status.Phase = vitistackcrdsv1alpha1.MachinePhaseFailed
		machine.Status.Message = errMsg
		r.setCondition(ctx, machine, "False", "VMProvisioningFailed", errMsg)
		_ = r.updateStatusWithRetry(ctx, machine)
		return ctrl.Result{}, fmt.Errorf("%s", errMsg)
	}

	// Fetch VM details - this is critical to verify the VM was actually created
	vm, err := r.ProxmoxClient.GetVM(ctx, node, vmID)
	if err != nil {
		logger.Error(err, "Failed to get VM details after creation - VM may not exist", "vmID", vmID)
		// Mark machine as failed since VM creation didn't actually succeed
		machine.Status.Phase = vitistackcrdsv1alpha1.MachinePhaseFailed
		r.setCondition(ctx, machine, "False", "VMCreationFailed", fmt.Sprintf("VM creation task completed but VM does not exist: %v", err))
		if updateErr := r.updateStatusWithRetry(ctx, machine); updateErr != nil {
			return ctrl.Result{}, updateErr
		}
		return ctrl.Result{}, fmt.Errorf("VM creation task completed but VM does not exist: %w", err)
	}

	// Create NetworkConfiguration if needed
	ipSource := viper.GetString(consts.IP_SOURCE)
	if ipSource == "networkconfiguration" && macAddress != "" {
		netConfConfig := networkconfiguration.NetworkConfig{
			MACAddress: macAddress,
			VLAN:       vlanTag,
		}
		if err := r.netConfManager.Create(ctx, machine, netConfConfig); err != nil {
			logger.Error(err, "Failed to create NetworkConfiguration")
		}
	}

	// Update status
	machine.Status.Phase = vitistackcrdsv1alpha1.MachinePhaseRunning
	machine.Status.ProviderID = providerid.Build(node, vmID)
	machine.Status.MachineID = fmt.Sprintf("%d", vmID)
	machine.Status.Provider = "proxmox"
	machine.Status.Region = node
	machine.Status.Zone = node
	machine.Status.CreationTime = &metav1.Time{Time: time.Now()}
	r.updateVMStatus(ctx, machine, vm, node)
	r.setCondition(ctx, machine, "True", "VMRunning", "VM successfully created and running")
	if err := r.updateStatusWithRetry(ctx, machine); err != nil {
		return ctrl.Result{}, err
	}

	logger.Info("VM created successfully", "vmID", vmID, "node", node)
	return ctrl.Result{}, nil
}

// reconcileCreating checks if VM creation is complete
func (r *MachineReconciler) reconcileCreating(ctx context.Context, machine *vitistackcrdsv1alpha1.Machine) (ctrl.Result, error) {
	// Creation is synchronous, so this phase is transitional
	return ctrl.Result{}, nil
}

// reconcileRunning ensures the VM is running and updates status
func (r *MachineReconciler) reconcileRunning(ctx context.Context, machine *vitistackcrdsv1alpha1.Machine) (ctrl.Result, error) {
	logger := logf.FromContext(ctx)

	node, vmID, err := providerid.Parse(machine.Status.ProviderID)
	if err != nil {
		logger.Error(err, "Failed to parse ProviderID")
		return ctrl.Result{}, nil
	}

	vm, err := r.ProxmoxClient.GetVM(ctx, node, vmID)
	if err != nil {
		if strings.Contains(err.Error(), "does not exist") || strings.Contains(err.Error(), "not found") {
			logger.Info("VM no longer exists in Proxmox", "vmID", vmID, "node", node)
			machine.Status.Phase = vitistackcrdsv1alpha1.MachinePhaseFailed
			machine.Status.State = "Missing"
			failureReason := "VMDeleted"
			failureMessage := "VM was deleted from Proxmox externally"
			machine.Status.FailureReason = &failureReason
			machine.Status.FailureMessage = &failureMessage
			r.setCondition(ctx, machine, "False", "VMDeleted", "VM was deleted from Proxmox externally")
			if updateErr := r.updateStatusWithRetry(ctx, machine); updateErr != nil {
				return ctrl.Result{}, updateErr
			}
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get VM status")
		return ctrl.Result{RequeueAfter: updateInterval}, nil
	}

	r.updateVMStatus(ctx, machine, vm, node)

	// Check for IP from NetworkConfiguration if configured
	ipSource := viper.GetString(consts.IP_SOURCE)
	if ipSource == "networkconfiguration" {
		ip, err := r.netConfManager.GetIP(ctx, machine)
		if err != nil {
			logger.Error(err, "Failed to get IP from NetworkConfiguration")
		} else if ip != "" {
			if len(machine.Status.IPAddresses) == 0 || machine.Status.IPAddresses[0] != ip {
				machine.Status.IPAddresses = []string{ip}
				if network.IsPrivateIP(ip) {
					machine.Status.PrivateIPAddresses = []string{ip}
				} else {
					machine.Status.PublicIPAddresses = []string{ip}
				}
				logger.Info("Updated IP from NetworkConfiguration", "ip", ip)
			}
		}
	}

	if err := r.updateStatusWithRetry(ctx, machine); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: updateInterval}, nil
}

// reconcileTerminating handles VM deletion
func (r *MachineReconciler) reconcileTerminating(ctx context.Context, machine *vitistackcrdsv1alpha1.Machine) (ctrl.Result, error) {
	logger := logf.FromContext(ctx)

	r.setCondition(ctx, machine, "False", "Terminating", "VM deletion in progress")

	if machine.Status.ProviderID == "" {
		logger.Info("No ProviderID set, VM was never created - removing finalizer")
		return ctrl.Result{}, nil
	}

	node, vmID, err := providerid.Parse(machine.Status.ProviderID)
	if err != nil {
		logger.Error(err, "Failed to parse ProviderID")
		r.setCondition(ctx, machine, "False", "DeletionFailed", err.Error())
		return ctrl.Result{}, err
	}

	vm, err := r.ProxmoxClient.GetVM(ctx, node, vmID)
	if err != nil {
		if strings.Contains(err.Error(), "does not exist") || strings.Contains(err.Error(), "not found") {
			logger.Info("VM does not exist, nothing to delete", "vmID", vmID, "node", node)
			machine.Status.Phase = vitistackcrdsv1alpha1.MachinePhaseTerminated
			r.setCondition(ctx, machine, "False", "Terminated", "VM already deleted or never existed")
			if updateErr := r.updateStatusWithRetry(ctx, machine); updateErr != nil {
				return ctrl.Result{}, updateErr
			}
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get VM status")
		r.setCondition(ctx, machine, "False", "DeletionFailed", fmt.Sprintf("Failed to get VM: %v", err))
		return ctrl.Result{}, err
	}

	// Stop VM if running
	if vm.Status == proxmoxStatusRunning {
		if err := r.stopVM(ctx, machine, node, vmID); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Delete VM
	logger.Info("Deleting VM", "vmID", vmID, "node", node)
	task, err := r.ProxmoxClient.DeleteVM(ctx, node, vmID)
	if err != nil {
		logger.Error(err, "Failed to delete VM")
		r.setCondition(ctx, machine, "False", "DeletionFailed", fmt.Sprintf("Failed to delete VM: %v", err))
		return ctrl.Result{}, err
	}

	if err := task.Wait(ctx, 5*time.Second, 5*time.Minute); err != nil {
		logger.Error(err, "VM deletion task failed")
		r.setCondition(ctx, machine, "False", "DeletionFailed", fmt.Sprintf("VM deletion task failed: %v", err))
		return ctrl.Result{}, err
	}

	// Delete NetworkConfiguration
	if err := r.netConfManager.Delete(ctx, machine); err != nil {
		logger.Error(err, "Failed to delete NetworkConfiguration (will be garbage collected)")
	}

	machine.Status.Phase = vitistackcrdsv1alpha1.MachinePhaseTerminated
	r.setCondition(ctx, machine, "False", "Terminated", "VM successfully deleted")
	if err := r.updateStatusWithRetry(ctx, machine); err != nil {
		return ctrl.Result{}, err
	}

	logger.Info("VM deleted successfully", "vmID", vmID, "node", node)
	return ctrl.Result{}, nil
}

// Helper functions

// checkExistingVM checks if VM already exists from previous reconciliation
func (r *MachineReconciler) checkExistingVM(ctx context.Context, machine *vitistackcrdsv1alpha1.Machine) error {
	logger := logf.FromContext(ctx)

	if machine.Status.MachineID == "" {
		return fmt.Errorf("no existing VM")
	}

	node, vmID, err := providerid.Parse(machine.Status.ProviderID)
	if err != nil {
		return err
	}

	vm, err := r.ProxmoxClient.GetVM(ctx, node, vmID)
	if err != nil || vm == nil {
		return fmt.Errorf("VM not found")
	}

	logger.Info("VM already exists, updating status to Running", "vmID", vmID, "node", node)
	machine.Status.Phase = vitistackcrdsv1alpha1.MachinePhaseRunning
	r.updateVMStatus(ctx, machine, vm, node)
	r.setCondition(ctx, machine, "True", "VMRunning", "VM already exists and is running")
	return r.updateStatusWithRetry(ctx, machine)
}

// findAvailableVMID finds an available VMID by checking if the generated ID exists and incrementing if needed
// Returns 0 if no available VMID is found after maxAttempts
func (r *MachineReconciler) findAvailableVMID(ctx context.Context, machine *vitistackcrdsv1alpha1.Machine, node string) int {
	logger := logf.FromContext(ctx)
	const maxAttempts = 100 // Maximum number of VMIDs to try before giving up

	baseVMID := r.generateVMID(machine)

	for attempt := range maxAttempts {
		vmID := baseVMID + attempt

		// Check if VMID exists on the node
		existingVM, err := r.ProxmoxClient.GetVM(ctx, node, vmID)
		if err != nil || existingVM == nil {
			// VMID is available (GetVM returned error means VM doesn't exist)
			if attempt > 0 {
				logger.Info("Found available VMID after collision",
					"originalVMID", baseVMID,
					"availableVMID", vmID,
					"attempts", attempt+1)
			}
			return vmID
		}

		logger.V(1).Info("VMID already in use, trying next",
			"vmID", vmID,
			"node", node,
			"attempt", attempt+1)
	}

	// No available VMID found after maxAttempts
	logger.Error(fmt.Errorf("exhausted VMID range"), "No available VMID found",
		"baseVMID", baseVMID,
		"maxAttempts", maxAttempts,
		"node", node)
	return 0
}

// selectNode selects a Proxmox node using the node selector
func (r *MachineReconciler) selectNode(ctx context.Context) (string, error) {
	nodeNames, err := r.ProxmoxClient.GetNodeNames(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get Proxmox nodes: %w", err)
	}
	return r.nodeSelector.SelectNode(ctx, nodeNames)
}

// generateMACAddress generates a MAC address using the generator
func (r *MachineReconciler) generateMACAddress(ctx context.Context) (string, error) {
	logger := logf.FromContext(ctx)

	if r.MacAddressGenerator == nil {
		return "", nil
	}

	macAddress, err := r.MacAddressGenerator.GetMACAddress()
	if err != nil {
		return "", err
	}

	logger.Info("Generated MAC address for VM", "macAddress", macAddress)
	return macAddress, nil
}

// stopVM stops a running VM
func (r *MachineReconciler) stopVM(ctx context.Context, machine *vitistackcrdsv1alpha1.Machine, node string, vmID int) error {
	logger := logf.FromContext(ctx)

	logger.Info("Stopping VM before deletion", "vmID", vmID, "node", node)
	r.setCondition(ctx, machine, "False", "Stopping", "Stopping VM before deletion")
	_ = r.updateStatusWithRetry(ctx, machine)

	stopTask, err := r.ProxmoxClient.StopVM(ctx, node, vmID)
	if err != nil {
		logger.Error(err, "Failed to stop VM")
		r.setCondition(ctx, machine, "False", "DeletionFailed", fmt.Sprintf("Failed to stop VM: %v", err))
		return err
	}

	if err := stopTask.Wait(ctx, 5*time.Second, 2*time.Minute); err != nil {
		logger.Error(err, "VM stop task failed")
		r.setCondition(ctx, machine, "False", "DeletionFailed", fmt.Sprintf("VM stop task failed: %v", err))
		return err
	}

	logger.Info("VM stopped successfully", "vmID", vmID, "node", node)
	return nil
}
