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
	"math"
	"math/rand"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/viper"
	vitistackcrdsv1alpha1 "github.com/vitistack/common/pkg/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/luthermonson/go-proxmox"
	"github.com/vitistack/proxmox-operator/internal/consts"
	proxmoxsvc "github.com/vitistack/proxmox-operator/internal/services/proxmox"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	// machineFinalizer is the finalizer added to Machine resources
	machineFinalizer = "vitistack.io/proxmox-machine"

	updateInterval = 10 * time.Second
)

// MachineReconciler reconciles a Machine object
type MachineReconciler struct {
	client.Client
	Scheme        *runtime.Scheme
	ProxmoxClient proxmoxsvc.ProxmoxClient
}

// +kubebuilder:rbac:groups=vitistack.io,resources=machines,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=vitistack.io,resources=machines/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=vitistack.io,resources=machines/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Machine object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.21.0/pkg/reconcile
func (r *MachineReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := logf.FromContext(ctx)

	// Fetch the Machine instance
	var machine vitistackcrdsv1alpha1.Machine
	if err := r.Get(ctx, req.NamespacedName, &machine); err != nil {
		// Ignore not-found errors (object deleted) and requeue nothing
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Only reconcile Machines for provider == proxmox
	if machine.Spec.Provider != vitistackcrdsv1alpha1.MachineProviderTypeProxmox {
		logger.V(1).Info("Skipping Machine: provider is not proxmox", "provider", machine.Spec.Provider, "name", req.NamespacedName)
		return ctrl.Result{}, nil
	}

	// Handle deletion - check if object is being deleted
	if !machine.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&machine, machineFinalizer) {
			// Update phase to terminating
			if machine.Status.Phase != vitistackcrdsv1alpha1.MachinePhaseTerminating && machine.Status.Phase != vitistackcrdsv1alpha1.MachinePhaseTerminated {
				machine.Status.Phase = vitistackcrdsv1alpha1.MachinePhaseTerminating
				if err := r.Status().Update(ctx, &machine); err != nil {
					return ctrl.Result{}, err
				}
			}

			// Run termination logic to delete the VM from Proxmox
			result, err := r.reconcileTerminating(ctx, &machine)
			if err != nil {
				return result, err
			}

			// Remove finalizer after successful deletion
			logger.Info("Removing finalizer from Machine", "finalizer", machineFinalizer)
			controllerutil.RemoveFinalizer(&machine, machineFinalizer)
			if err := r.Update(ctx, &machine); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	// Add finalizer if not present
	if !controllerutil.ContainsFinalizer(&machine, machineFinalizer) {
		logger.Info("Adding finalizer to Machine", "finalizer", machineFinalizer)
		controllerutil.AddFinalizer(&machine, machineFinalizer)
		if err := r.Update(ctx, &machine); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Fetch MachineClass
	var machineClass vitistackcrdsv1alpha1.MachineClass
	machineClassKey := client.ObjectKey{Name: machine.Spec.MachineClass}
	if err := r.Get(ctx, machineClassKey, &machineClass); err != nil {
		logger.Error(err, "Failed to get MachineClass", "machineClass", machine.Spec.MachineClass)
		return ctrl.Result{}, err
	}

	// Check if MachineClass is enabled
	if !machineClass.Spec.Enabled {
		logger.Info("MachineClass is not enabled", "machineClass", machine.Spec.MachineClass)
		return ctrl.Result{}, nil
	}

	// Check if MachineClass supports Proxmox
	// supportsProxmox := false
	// for _, provider := range machineClass.Spec.MachineProviders {
	// 	if provider == vitistackcrdsv1alpha1.MachineProviderTypeProxmox {
	// 		supportsProxmox = true
	// 		break
	// 	}
	// }
	// if !supportsProxmox {
	// 	logger.Info("MachineClass does not support Proxmox", "machineClass", machine.Spec.MachineClass)
	// 	return ctrl.Result{}, nil
	// }

	// Reconcile based on current phase
	switch machine.Status.Phase {
	case "":
		// Initial creation
		return r.reconcileCreate(ctx, &machine, &machineClass)
	case vitistackcrdsv1alpha1.MachinePhasePending:
		return r.reconcileCreate(ctx, &machine, &machineClass)
	case vitistackcrdsv1alpha1.MachinePhaseCreating:
		return r.reconcileCreating(ctx, &machine)
	case vitistackcrdsv1alpha1.MachinePhaseRunning:
		return r.reconcileRunning(ctx, &machine)
	case vitistackcrdsv1alpha1.MachinePhaseTerminating:
		return r.reconcileTerminating(ctx, &machine)
	case vitistackcrdsv1alpha1.MachinePhaseFailed:
		// Don't retry failed machines automatically - user must delete and recreate
		logger.Info("Machine in Failed state, not retrying", "phase", machine.Status.Phase)
		return ctrl.Result{}, nil
	default:
		logger.Info("Unknown phase", "phase", machine.Status.Phase)
		return ctrl.Result{}, nil
	}
}

// reconcileCreate handles the creation of a new VM
func (r *MachineReconciler) reconcileCreate(ctx context.Context, machine *vitistackcrdsv1alpha1.Machine, machineClass *vitistackcrdsv1alpha1.MachineClass) (ctrl.Result, error) {
	logger := logf.FromContext(ctx)

	// Check if VM already exists (from previous reconciliation attempt)
	if machine.Status.MachineID != "" {
		vmID, err := strconv.Atoi(machine.Status.MachineID)
		if err == nil {
			// Extract node from ProviderID if available
			node, _, parseErr := parseProviderID(machine.Status.ProviderID)
			if parseErr == nil && node != "" {
				// Check if VM exists
				vm, err := r.ProxmoxClient.GetVM(ctx, node, vmID)
				if err == nil && vm != nil {
					logger.Info("VM already exists, updating status to Running", "vmID", vmID, "node", node)
					machine.Status.Phase = vitistackcrdsv1alpha1.MachinePhaseRunning
					r.updateVMStatus(ctx, machine, vm, node)
					r.setCondition(ctx, machine, "True", "VMRunning", "VM already exists and is running")
					if err := r.updateStatusWithRetry(ctx, machine); err != nil {
						return ctrl.Result{}, err
					}
					return ctrl.Result{}, nil
				}
			}
		}
	}

	// Update status to creating
	machine.Status.Phase = vitistackcrdsv1alpha1.MachinePhaseCreating
	r.setCondition(ctx, machine, "False", "Creating", "VM creation in progress")
	if err := r.updateStatusWithRetry(ctx, machine); err != nil {
		return ctrl.Result{}, err
	}

	// Get Proxmox node names and select one based on strategy
	nodeNames, err := r.ProxmoxClient.GetNodeNames(ctx)
	if err != nil {
		logger.Error(err, "Failed to get Proxmox nodes")
		machine.Status.Phase = vitistackcrdsv1alpha1.MachinePhaseFailed
		r.setCondition(ctx, machine, "False", "NodeSelectionFailed", fmt.Sprintf("Failed to get Proxmox nodes: %v", err))
		_ = r.updateStatusWithRetry(ctx, machine)
		return ctrl.Result{}, err
	}
	node, err := r.selectNode(ctx, nodeNames)
	if err != nil {
		logger.Error(err, "Failed to select Proxmox node")
		machine.Status.Phase = vitistackcrdsv1alpha1.MachinePhaseFailed
		r.setCondition(ctx, machine, "False", "NodeSelectionFailed", fmt.Sprintf("Failed to select Proxmox node: %v", err))
		_ = r.updateStatusWithRetry(ctx, machine)
		return ctrl.Result{}, err
	}

	// Generate VM ID starting from configured base
	vmIDStart := viper.GetInt(consts.PROXMOX_VM_ID_START)
	uidStr := string(machine.UID)
	uidInt := 0
	for _, r := range uidStr {
		uidInt += int(r)
	}
	vmID := vmIDStart + uidInt%1000 // Use configured start + hash-like offset

	// Build VM options from MachineClass
	options := r.buildVMOptions(machine, machineClass)

	// Validate ISO exists (if specified)
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

	// Wait for task completion (poll every 5 seconds, timeout after 5 minutes)
	if err := task.Wait(ctx, 5*time.Second, 5*time.Minute); err != nil {
		logger.Error(err, "VM creation task failed")
		machine.Status.Phase = vitistackcrdsv1alpha1.MachinePhaseFailed
		machine.Status.Message = err.Error()
		r.setCondition(ctx, machine, "False", "VMProvisioningFailed", fmt.Sprintf("VM creation task failed: %v", err))
		_ = r.updateStatusWithRetry(ctx, machine)
		return ctrl.Result{}, err
	}

	// Fetch VM details to populate status
	vm, err := r.ProxmoxClient.GetVM(ctx, node, vmID)
	if err != nil {
		logger.Error(err, "Failed to get VM details after creation", "vmID", vmID)
		// Continue anyway - VM was created successfully
	}

	// Update status
	machine.Status.Phase = vitistackcrdsv1alpha1.MachinePhaseRunning
	machine.Status.ProviderID = fmt.Sprintf("proxmox://%s/%d", node, vmID)
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
	// For now, assume creation is synchronous
	return ctrl.Result{}, nil
}

// reconcileRunning ensures the VM is running and updates status
func (r *MachineReconciler) reconcileRunning(ctx context.Context, machine *vitistackcrdsv1alpha1.Machine) (ctrl.Result, error) {
	logger := logf.FromContext(ctx)

	// Parse provider ID to get node and VM ID
	node, vmID, err := parseProviderID(machine.Status.ProviderID)
	if err != nil {
		logger.Error(err, "Failed to parse ProviderID")
		return ctrl.Result{}, nil // Don't requeue on parse error
	}

	// Get current VM state from Proxmox
	vm, err := r.ProxmoxClient.GetVM(ctx, node, vmID)
	if err != nil {
		if strings.Contains(err.Error(), "does not exist") || strings.Contains(err.Error(), "not found") {
			// VM was deleted externally
			logger.Info("VM no longer exists in Proxmox", "vmID", vmID, "node", node)
			machine.Status.Phase = vitistackcrdsv1alpha1.MachinePhaseFailed
			machine.Status.State = "missing"
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

	// Update VM status from Proxmox data
	r.updateVMStatus(ctx, machine, vm, node)

	if err := r.updateStatusWithRetry(ctx, machine); err != nil {
		return ctrl.Result{}, err
	}

	// Requeue to periodically sync status
	return ctrl.Result{RequeueAfter: updateInterval}, nil
}

// reconcileTerminating handles VM deletion
func (r *MachineReconciler) reconcileTerminating(ctx context.Context, machine *vitistackcrdsv1alpha1.Machine) (ctrl.Result, error) {
	logger := logf.FromContext(ctx)

	// Set terminating condition
	r.setCondition(ctx, machine, "False", "Terminating", "VM deletion in progress")

	// If no ProviderID, the VM was never created - just remove finalizer
	if machine.Status.ProviderID == "" {
		logger.Info("No ProviderID set, VM was never created - removing finalizer")
		return ctrl.Result{}, nil
	}

	// Parse VM ID from ProviderID
	node, vmID, err := parseProviderID(machine.Status.ProviderID)
	if err != nil {
		logger.Error(err, "Failed to parse ProviderID")
		r.setCondition(ctx, machine, "False", "DeletionFailed", err.Error())
		return ctrl.Result{}, err
	}

	// Check if VM exists and stop it if running
	vm, err := r.ProxmoxClient.GetVM(ctx, node, vmID)
	if err != nil {
		// VM doesn't exist, nothing to delete
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

	// Stop VM if it's running
	if vm.Status == "running" {
		logger.Info("Stopping VM before deletion", "vmID", vmID, "node", node)
		r.setCondition(ctx, machine, "False", "Stopping", "Stopping VM before deletion")
		_ = r.updateStatusWithRetry(ctx, machine)

		stopTask, err := r.ProxmoxClient.StopVM(ctx, node, vmID)
		if err != nil {
			logger.Error(err, "Failed to stop VM")
			r.setCondition(ctx, machine, "False", "DeletionFailed", fmt.Sprintf("Failed to stop VM: %v", err))
			return ctrl.Result{}, err
		}

		// Wait for stop task completion
		if err := stopTask.Wait(ctx, 5*time.Second, 2*time.Minute); err != nil {
			logger.Error(err, "VM stop task failed")
			r.setCondition(ctx, machine, "False", "DeletionFailed", fmt.Sprintf("VM stop task failed: %v", err))
			return ctrl.Result{}, err
		}
		logger.Info("VM stopped successfully", "vmID", vmID, "node", node)
	}

	// Delete VM
	logger.Info("Deleting VM", "vmID", vmID, "node", node)
	task, err := r.ProxmoxClient.DeleteVM(ctx, node, vmID)
	if err != nil {
		logger.Error(err, "Failed to delete VM")
		r.setCondition(ctx, machine, "False", "DeletionFailed", fmt.Sprintf("Failed to delete VM: %v", err))
		return ctrl.Result{}, err
	}

	// Wait for task completion (poll every 5 seconds, timeout after 5 minutes)
	if err := task.Wait(ctx, 5*time.Second, 5*time.Minute); err != nil {
		logger.Error(err, "VM deletion task failed")
		r.setCondition(ctx, machine, "False", "DeletionFailed", fmt.Sprintf("VM deletion task failed: %v", err))
		return ctrl.Result{}, err
	}

	// Update status
	machine.Status.Phase = vitistackcrdsv1alpha1.MachinePhaseTerminated
	r.setCondition(ctx, machine, "False", "Terminated", "VM successfully deleted")
	if err := r.updateStatusWithRetry(ctx, machine); err != nil {
		return ctrl.Result{}, err
	}

	logger.Info("VM deleted successfully", "vmID", vmID, "node", node)
	return ctrl.Result{}, nil
}

// parseProviderID parses a ProviderID in format "proxmox://node/vmID" and returns node and vmID
func parseProviderID(providerID string) (node string, vmID int, err error) {
	if providerID == "" {
		return "", 0, fmt.Errorf("empty ProviderID")
	}
	// Format: proxmox://node/vmID -> splits to ["proxmox:", "", "node", "vmID"]
	parts := strings.Split(providerID, "/")
	if len(parts) != 4 || parts[0] != "proxmox:" {
		return "", 0, fmt.Errorf("invalid ProviderID format: %s (expected proxmox://node/vmID)", providerID)
	}
	node = parts[2]
	vmID, err = strconv.Atoi(parts[3])
	if err != nil {
		return "", 0, fmt.Errorf("invalid VM ID in ProviderID: %s", parts[3])
	}
	return node, vmID, nil
}

// buildVMOptions builds Proxmox VM options from Machine and MachineClass specs
func (r *MachineReconciler) buildVMOptions(machine *vitistackcrdsv1alpha1.Machine, machineClass *vitistackcrdsv1alpha1.MachineClass) []proxmox.VirtualMachineOption {
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

	// Memory
	memoryMB := int64(0)
	if machine.Spec.Memory > 0 {
		memoryMB = machine.Spec.Memory / (1024 * 1024) // Convert bytes to MB
	} else if machineClass.Spec.Memory.Quantity.Value() > 0 {
		memoryMB = machineClass.Spec.Memory.Quantity.Value() / (1024 * 1024)
	}
	if memoryMB > 0 {
		options = append(options, proxmox.VirtualMachineOption{Name: "memory", Value: memoryMB})
	}

	// SCSI Controller
	scsiController := viper.GetString(consts.PROXMOX_SCSI_CONTROLLER)
	if scsiController != "" {
		options = append(options, proxmox.VirtualMachineOption{Name: "scsihw", Value: scsiController})
	}

	// Disks
	for _, disk := range machine.Spec.Disks {
		if disk.Name == "root" {
			sizeGB := disk.SizeGB
			if sizeGB == 0 {
				sizeGB = 50 // Default
			}
			storagePool := viper.GetString(consts.PROXMOX_DEFAULT_STORAGE)
			options = append(options, proxmox.VirtualMachineOption{Name: "scsi0", Value: fmt.Sprintf("%s:%d", storagePool, sizeGB)})
		}
	}

	// Network
	if len(machine.Spec.Network.Interfaces) > 0 {
		networkBridge := viper.GetString(consts.PROXMOX_DEFAULT_NETWORK)
		options = append(options, proxmox.VirtualMachineOption{Name: "net0", Value: fmt.Sprintf("virtio,bridge=%s", networkBridge)})
	}

	// OS
	if machine.Spec.OS.ImageID != "" {
		options = append(options, proxmox.VirtualMachineOption{Name: "cdrom", Value: machine.Spec.OS.ImageID})
	}

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

// validateISO checks if the specified ISO exists in Proxmox storage
func (r *MachineReconciler) validateISO(ctx context.Context, node string, isoPath string) error {
	logger := logf.FromContext(ctx)

	// If it's not a local storage path, skip validation (could be HTTP URL)
	if !strings.HasPrefix(isoPath, "local:") {
		logger.Info("Skipping ISO validation for non-local path", "isoPath", isoPath)
		return nil
	}

	// Parse the storage and volume from the path (e.g., "local:iso/debian.iso")
	parts := strings.SplitN(isoPath, ":", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid ISO path format: %s", isoPath)
	}

	storageName := parts[0]

	// Get the storage content to check if the ISO exists
	nodeClient, err := r.ProxmoxClient.Client().Node(ctx, node)
	if err != nil {
		return fmt.Errorf("failed to get node client: %w", err)
	}

	storage, err := nodeClient.Storage(ctx, storageName)
	if err != nil {
		return fmt.Errorf("failed to get storage %s: %w", storageName, err)
	}

	content, err := storage.GetContent(ctx)
	if err != nil {
		return fmt.Errorf("failed to get storage content for %s: %w", storageName, err)
	}

	// Check if the ISO exists
	for _, item := range content {
		// Volid from API includes storage prefix, e.g., "local:iso/debian.iso"
		if item.Volid == isoPath {
			logger.Info("ISO validated successfully", "isoPath", isoPath, "node", node)
			return nil
		}
	}

	// Log available ISOs for debugging
	availableISOs := make([]string, 0, len(content))
	for _, item := range content {
		availableISOs = append(availableISOs, item.Volid)
	}
	logger.Info("Available ISOs in storage", "storage", storageName, "isos", availableISOs)

	return fmt.Errorf("ISO not found in storage %s: %s (available: %v)", storageName, isoPath, availableISOs)
}

// setCondition sets a Ready condition on the Machine status
func (r *MachineReconciler) setCondition(ctx context.Context, machine *vitistackcrdsv1alpha1.Machine, status string, reason, message string) {
	logger := logf.FromContext(ctx)

	now := metav1.Now()

	// Find existing condition or create new one
	var existingCondition *vitistackcrdsv1alpha1.MachineCondition
	for i := range machine.Status.Conditions {
		if machine.Status.Conditions[i].Type == "Ready" {
			existingCondition = &machine.Status.Conditions[i]
			break
		}
	}

	if existingCondition != nil {
		// Update existing condition
		existingCondition.Status = status
		existingCondition.LastTransitionTime = now
		existingCondition.Reason = reason
		existingCondition.Message = message
	} else {
		// Add new condition
		condition := vitistackcrdsv1alpha1.MachineCondition{
			Type:               "Ready",
			Status:             status,
			LastTransitionTime: now,
			Reason:             reason,
			Message:            message,
		}
		machine.Status.Conditions = append(machine.Status.Conditions, condition)
	}

	logger.Info("Set machine condition", "type", "Ready", "status", status, "reason", reason)
}

// updateStatusWithRetry updates the machine status with automatic retry on conflict
func (r *MachineReconciler) updateStatusWithRetry(ctx context.Context, machine *vitistackcrdsv1alpha1.Machine) error {
	logger := logf.FromContext(ctx)

	err := r.Status().Update(ctx, machine)
	if err != nil {
		if apierrors.IsConflict(err) {
			logger.V(1).Info("Conflict updating status, will be retried on next reconcile")
			// Return nil - the controller will requeue anyway and get fresh data
			return nil
		}
		return err
	}
	return nil
}

// updateVMStatus updates machine status fields from Proxmox VM data
func (r *MachineReconciler) updateVMStatus(ctx context.Context, machine *vitistackcrdsv1alpha1.Machine, vm *proxmox.VirtualMachine, node string) {
	logger := logf.FromContext(ctx)

	if vm == nil {
		return
	}

	// Update last updated timestamp
	machine.Status.LastUpdated = metav1.Now()

	// Update state from VM status
	machine.Status.State = vm.Status

	// Update hostname from VM name
	if vm.Name != "" {
		machine.Status.Hostname = vm.Name
	}

	// Update CPU count
	if vm.CPUs > 0 {
		machine.Status.CPUs = vm.CPUs
	}

	// Update memory (Proxmox returns memory in bytes)
	// Safe conversion: cap at MaxInt64 to prevent overflow (practically unreachable for real memory)
	if vm.MaxMem > 0 {
		if vm.MaxMem <= math.MaxInt64 {
			machine.Status.Memory = int64(vm.MaxMem)
		} else {
			machine.Status.Memory = math.MaxInt64
		}
	}

	// Set provider info
	machine.Status.Provider = vitistackcrdsv1alpha1.MachineProviderTypeProxmox
	if machine.Status.Region == "" {
		machine.Status.Region = node
	}
	if machine.Status.Zone == "" {
		machine.Status.Zone = node
	}

	// Extract network information from QEMU guest agent if available
	if vm.Agent && vm.Status == "running" {
		r.updateNetworkStatus(ctx, machine, vm)
	}

	logger.V(1).Info("Updated VM status", "state", vm.Status, "cpus", vm.CPUs, "memory", vm.MaxMem, "hostname", vm.Name)
}

// updateNetworkStatus fetches network interface information from QEMU guest agent and updates machine status
func (r *MachineReconciler) updateNetworkStatus(ctx context.Context, machine *vitistackcrdsv1alpha1.Machine, vm *proxmox.VirtualMachine) {
	logger := logf.FromContext(ctx)

	// Get network interfaces from QEMU guest agent
	ifaces, err := vm.AgentGetNetworkIFaces(ctx)
	if err != nil {
		logger.V(1).Info("Failed to get network interfaces from QEMU agent (agent may not be running)", "error", err.Error())
		return
	}

	// Clear existing network status
	machine.Status.NetworkInterfaces = nil
	machine.Status.IPAddresses = nil
	machine.Status.IPv6Addresses = nil
	machine.Status.PrivateIPAddresses = nil
	machine.Status.PublicIPAddresses = nil

	var allIPv4 []string
	var allIPv6 []string

	for _, iface := range ifaces {
		// Skip loopback interface
		if iface.Name == "lo" {
			continue
		}

		netIfaceStatus := vitistackcrdsv1alpha1.NetworkInterfaceStatus{
			Name:       iface.Name,
			MACAddress: iface.HardwareAddress,
			Type:       "ethernet",
		}

		var ipv4Addrs []string
		var ipv6Addrs []string

		for _, ipAddr := range iface.IPAddresses {
			switch ipAddr.IPAddressType {
			case "ipv4":
				ipv4Addrs = append(ipv4Addrs, ipAddr.IPAddress)
				allIPv4 = append(allIPv4, ipAddr.IPAddress)
			case "ipv6":
				// Skip link-local IPv6 addresses
				if !strings.HasPrefix(ipAddr.IPAddress, "fe80:") {
					ipv6Addrs = append(ipv6Addrs, ipAddr.IPAddress)
					allIPv6 = append(allIPv6, ipAddr.IPAddress)
				}
			}
		}

		netIfaceStatus.IPAddresses = ipv4Addrs
		netIfaceStatus.IPv6Addresses = ipv6Addrs

		// Determine state based on having IP addresses
		if len(ipv4Addrs) > 0 || len(ipv6Addrs) > 0 {
			netIfaceStatus.State = "up"
		}

		machine.Status.NetworkInterfaces = append(machine.Status.NetworkInterfaces, netIfaceStatus)
	}

	// Set aggregated IP addresses
	machine.Status.IPAddresses = allIPv4
	machine.Status.IPv6Addresses = allIPv6

	// Categorize IPs as public or private
	for _, ip := range allIPv4 {
		if isPrivateIP(ip) {
			machine.Status.PrivateIPAddresses = append(machine.Status.PrivateIPAddresses, ip)
		} else {
			machine.Status.PublicIPAddresses = append(machine.Status.PublicIPAddresses, ip)
		}
	}

	logger.V(1).Info("Updated network status", "interfaces", len(machine.Status.NetworkInterfaces), "ipv4", allIPv4, "ipv6", allIPv6)
}

// isPrivateIP checks if an IP address is in a private range (RFC 1918)
func isPrivateIP(ip string) bool {
	// Check common private IP ranges
	return strings.HasPrefix(ip, "10.") ||
		strings.HasPrefix(ip, "172.16.") ||
		strings.HasPrefix(ip, "172.17.") ||
		strings.HasPrefix(ip, "172.18.") ||
		strings.HasPrefix(ip, "172.19.") ||
		strings.HasPrefix(ip, "172.20.") ||
		strings.HasPrefix(ip, "172.21.") ||
		strings.HasPrefix(ip, "172.22.") ||
		strings.HasPrefix(ip, "172.23.") ||
		strings.HasPrefix(ip, "172.24.") ||
		strings.HasPrefix(ip, "172.25.") ||
		strings.HasPrefix(ip, "172.26.") ||
		strings.HasPrefix(ip, "172.27.") ||
		strings.HasPrefix(ip, "172.28.") ||
		strings.HasPrefix(ip, "172.29.") ||
		strings.HasPrefix(ip, "172.30.") ||
		strings.HasPrefix(ip, "172.31.") ||
		strings.HasPrefix(ip, "192.168.") ||
		strings.HasPrefix(ip, "127.")
}

// selectNode selects a Proxmox node based on the configured strategy
func (r *MachineReconciler) selectNode(ctx context.Context, nodeNames []string) (string, error) {
	logger := logf.FromContext(ctx)

	if len(nodeNames) == 0 {
		return "", fmt.Errorf("no Proxmox nodes available")
	}

	// Get allowed nodes filter
	allowedNodesStr := viper.GetString(consts.PROXMOX_ALLOWED_NODES)
	var allowedNodes []string
	if allowedNodesStr != "" {
		allowedNodes = strings.Split(allowedNodesStr, ",")
		for i, node := range allowedNodes {
			allowedNodes[i] = strings.TrimSpace(node)
		}
	}

	// Filter nodes based on allowed list
	var candidateNodes []string
	if len(allowedNodes) > 0 {
		for _, node := range nodeNames {
			for _, allowed := range allowedNodes {
				if node == allowed {
					candidateNodes = append(candidateNodes, node)
					break
				}
			}
		}
		if len(candidateNodes) == 0 {
			return "", fmt.Errorf("no allowed Proxmox nodes available from list: %v", allowedNodes)
		}
	} else {
		candidateNodes = nodeNames
	}

	// Get selection strategy
	strategy := viper.GetString(consts.PROXMOX_NODE_SELECTION)
	if strategy == "" {
		strategy = "first" // Default
	}

	logger.Info("Selecting node", "strategy", strategy, "candidates", candidateNodes)

	switch strategy {
	case "first":
		return candidateNodes[0], nil
	case "random":
		return candidateNodes[rand.Intn(len(candidateNodes))], nil // #nosec G404 - Node selection doesn't require cryptographic randomness
	case "round-robin":
		// For now, use random as round-robin would require state persistence
		// TODO: Implement proper round-robin with stored state
		return candidateNodes[rand.Intn(len(candidateNodes))], nil // #nosec G404 - Node selection doesn't require cryptographic randomness
	default:
		logger.Info("Unknown node selection strategy, using 'first'", "strategy", strategy)
		return candidateNodes[0], nil
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *MachineReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&vitistackcrdsv1alpha1.Machine{}).
		Watches(&vitistackcrdsv1alpha1.MachineClass{}, &handler.EnqueueRequestForObject{}).
		Named("machine").
		Complete(r)
}
