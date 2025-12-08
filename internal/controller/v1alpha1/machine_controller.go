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
	"strings"
	"time"

	"github.com/luthermonson/go-proxmox"
	"github.com/spf13/viper"
	vitistackcrdsv1alpha1 "github.com/vitistack/common/pkg/v1alpha1"
	"github.com/vitistack/proxmox-operator/internal/consts"
	"github.com/vitistack/proxmox-operator/internal/helpers/network"
	"github.com/vitistack/proxmox-operator/internal/services/networkconfiguration"
	"github.com/vitistack/proxmox-operator/internal/services/nodeselection"
	proxmoxsvc "github.com/vitistack/proxmox-operator/internal/services/proxmox"
	"github.com/vitistack/proxmox-operator/internal/services/vmbuilder"
	"github.com/vitistack/proxmox-operator/pkg/macaddress"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

const (
	// machineFinalizer is the finalizer added to Machine resources
	machineFinalizer = "vitistack.io/proxmox-machine"

	// updateInterval is the interval for periodic status updates
	updateInterval = 10 * time.Second

	// Proxmox VM status constants (lowercase as returned by Proxmox API)
	proxmoxStatusRunning   = "running"
	proxmoxStatusStopped   = "stopped"
	proxmoxStatusPaused    = "paused"
	proxmoxStatusSuspended = "suspended"
	proxmoxStatusPrelaunch = "prelaunch"
)

// MachineReconciler reconciles a Machine object
type MachineReconciler struct {
	client.Client
	Scheme              *runtime.Scheme
	ProxmoxClient       proxmoxsvc.ProxmoxClient
	MacAddressGenerator macaddress.MacAddressGenerator

	// Internal services (initialized lazily)
	vmBuilder      *vmbuilder.Builder
	nodeSelector   *nodeselection.Selector
	netConfManager *networkconfiguration.Manager
}

// +kubebuilder:rbac:groups=vitistack.io,resources=machines,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=vitistack.io,resources=machines/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=vitistack.io,resources=machines/finalizers,verbs=update
// +kubebuilder:rbac:groups=vitistack.io,resources=networkconfigurations,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=vitistack.io,resources=networkconfigurations/status,verbs=get;watch
// +kubebuilder:rbac:groups=vitistack.io,resources=networknamespaces,verbs=get;list;watch
// +kubebuilder:rbac:groups=vitistack.io,resources=networknamespaces/status,verbs=get
// +kubebuilder:rbac:groups=vitistack.io,resources=vitistacks,verbs=get;list;watch
// +kubebuilder:rbac:groups=vitistack.io,resources=machineproviders,verbs=get;list;watch;create;update;patch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *MachineReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := logf.FromContext(ctx)

	// Initialize services if not already done
	r.initServices()

	// Fetch the Machine instance
	var machine vitistackcrdsv1alpha1.Machine
	if err := r.Get(ctx, req.NamespacedName, &machine); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Only reconcile Machines for provider == proxmox
	if machine.Spec.Provider != vitistackcrdsv1alpha1.MachineProviderTypeProxmox {
		logger.V(1).Info("Skipping Machine: provider is not proxmox", "provider", machine.Spec.Provider, "name", req.NamespacedName)
		return ctrl.Result{}, nil
	}

	// Handle deletion
	if !machine.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, &machine)
	}

	// Ensure finalizer is present
	if err := r.ensureFinalizer(ctx, &machine); err != nil {
		return ctrl.Result{}, err
	}

	// Fetch MachineClass
	machineClass, err := r.getMachineClass(ctx, machine.Spec.MachineClass)
	if err != nil {
		logger.Error(err, "Failed to get MachineClass", "machineClass", machine.Spec.MachineClass)
		return ctrl.Result{}, err
	}

	// Check if MachineClass is enabled
	if !machineClass.Spec.Enabled {
		logger.Info("MachineClass is not enabled", "machineClass", machine.Spec.MachineClass)
		return ctrl.Result{}, nil
	}

	// Reconcile based on current phase
	return r.reconcileByPhase(ctx, &machine, machineClass)
}

// initServices initializes the internal services
func (r *MachineReconciler) initServices() {
	if r.vmBuilder == nil {
		r.vmBuilder = vmbuilder.NewBuilder()
	}
	if r.nodeSelector == nil {
		r.nodeSelector = nodeselection.NewSelector()
	}
	if r.netConfManager == nil {
		r.netConfManager = networkconfiguration.NewManager(r.Client)
	}
}

// handleDeletion handles the deletion of a machine
func (r *MachineReconciler) handleDeletion(ctx context.Context, machine *vitistackcrdsv1alpha1.Machine) (ctrl.Result, error) {
	logger := logf.FromContext(ctx)

	if !controllerutil.ContainsFinalizer(machine, machineFinalizer) {
		return ctrl.Result{}, nil
	}

	// Update phase to terminating
	if machine.Status.Phase != vitistackcrdsv1alpha1.MachinePhaseTerminating &&
		machine.Status.Phase != vitistackcrdsv1alpha1.MachinePhaseTerminated {
		machine.Status.Phase = vitistackcrdsv1alpha1.MachinePhaseTerminating
		if err := r.Status().Update(ctx, machine); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Run termination logic
	result, err := r.reconcileTerminating(ctx, machine)
	if err != nil {
		return result, err
	}

	// Remove finalizer
	logger.Info("Removing finalizer from Machine", "finalizer", machineFinalizer)
	controllerutil.RemoveFinalizer(machine, machineFinalizer)
	if err := r.Update(ctx, machine); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// ensureFinalizer ensures the finalizer is present on the machine
func (r *MachineReconciler) ensureFinalizer(ctx context.Context, machine *vitistackcrdsv1alpha1.Machine) error {
	logger := logf.FromContext(ctx)

	if controllerutil.ContainsFinalizer(machine, machineFinalizer) {
		return nil
	}

	logger.Info("Adding finalizer to Machine", "finalizer", machineFinalizer)
	controllerutil.AddFinalizer(machine, machineFinalizer)
	return r.Update(ctx, machine)
}

// getMachineClass fetches the MachineClass for the machine
func (r *MachineReconciler) getMachineClass(ctx context.Context, name string) (*vitistackcrdsv1alpha1.MachineClass, error) {
	var machineClass vitistackcrdsv1alpha1.MachineClass
	if err := r.Get(ctx, client.ObjectKey{Name: name}, &machineClass); err != nil {
		return nil, err
	}
	return &machineClass, nil
}

// reconcileByPhase dispatches reconciliation based on machine phase
func (r *MachineReconciler) reconcileByPhase(ctx context.Context, machine *vitistackcrdsv1alpha1.Machine, machineClass *vitistackcrdsv1alpha1.MachineClass) (ctrl.Result, error) {
	logger := logf.FromContext(ctx)

	switch machine.Status.Phase {
	case "", vitistackcrdsv1alpha1.MachinePhasePending:
		return r.reconcileCreate(ctx, machine, machineClass)
	case vitistackcrdsv1alpha1.MachinePhaseCreating:
		return r.reconcileCreating(ctx, machine)
	case vitistackcrdsv1alpha1.MachinePhaseRunning:
		return r.reconcileRunning(ctx, machine)
	case vitistackcrdsv1alpha1.MachinePhaseTerminating:
		return r.reconcileTerminating(ctx, machine)
	case vitistackcrdsv1alpha1.MachinePhaseFailed:
		logger.Info("Machine in Failed state, not retrying", "phase", machine.Status.Phase)
		return ctrl.Result{}, nil
	default:
		logger.Info("Unknown phase", "phase", machine.Status.Phase)
		return ctrl.Result{}, nil
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *MachineReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Create predicate to filter only Proxmox machines
	proxmoxMachinePredicate := predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			if machine, ok := e.Object.(*vitistackcrdsv1alpha1.Machine); ok {
				return machine.Spec.Provider == vitistackcrdsv1alpha1.MachineProviderTypeProxmox
			}
			return false
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			if machine, ok := e.ObjectNew.(*vitistackcrdsv1alpha1.Machine); ok {
				return machine.Spec.Provider == vitistackcrdsv1alpha1.MachineProviderTypeProxmox
			}
			return false
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			if machine, ok := e.Object.(*vitistackcrdsv1alpha1.Machine); ok {
				return machine.Spec.Provider == vitistackcrdsv1alpha1.MachineProviderTypeProxmox
			}
			return false
		},
		GenericFunc: func(e event.GenericEvent) bool {
			if machine, ok := e.Object.(*vitistackcrdsv1alpha1.Machine); ok {
				return machine.Spec.Provider == vitistackcrdsv1alpha1.MachineProviderTypeProxmox
			}
			return false
		},
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&vitistackcrdsv1alpha1.Machine{}).
		WithEventFilter(proxmoxMachinePredicate).
		Watches(&vitistackcrdsv1alpha1.MachineClass{}, &handler.EnqueueRequestForObject{}).
		Owns(&vitistackcrdsv1alpha1.NetworkConfiguration{}).
		Named("proxmox-machine").
		Complete(r)
}

// updateStatusWithRetry updates the machine status with automatic retry on conflict
func (r *MachineReconciler) updateStatusWithRetry(ctx context.Context, machine *vitistackcrdsv1alpha1.Machine) error {
	logger := logf.FromContext(ctx)

	err := r.Status().Update(ctx, machine)
	if err != nil {
		if apierrors.IsConflict(err) {
			logger.V(1).Info("Conflict updating status, will be retried on next reconcile")
			return nil
		}
		return err
	}
	return nil
}

// setCondition sets a Ready condition on the Machine status
func (r *MachineReconciler) setCondition(ctx context.Context, machine *vitistackcrdsv1alpha1.Machine, status string, reason, message string) {
	logger := logf.FromContext(ctx)

	now := metav1.Now()

	var existingCondition *vitistackcrdsv1alpha1.MachineCondition
	for i := range machine.Status.Conditions {
		if machine.Status.Conditions[i].Type == "Ready" {
			existingCondition = &machine.Status.Conditions[i]
			break
		}
	}

	if existingCondition != nil {
		existingCondition.Status = status
		existingCondition.LastTransitionTime = now
		existingCondition.Reason = reason
		existingCondition.Message = message
	} else {
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

// updateVMStatus updates machine status fields from Proxmox VM data
func (r *MachineReconciler) updateVMStatus(ctx context.Context, machine *vitistackcrdsv1alpha1.Machine, vm *proxmox.VirtualMachine, node string) {
	logger := logf.FromContext(ctx)

	if vm == nil {
		return
	}

	machine.Status.LastUpdated = metav1.Now()
	machine.Status.State = mapProxmoxStatusToState(vm.Status)

	if vm.Name != "" {
		machine.Status.Hostname = vm.Name
	}

	if vm.CPUs > 0 {
		machine.Status.CPUs = vm.CPUs
	}

	if vm.MaxMem > 0 {
		if vm.MaxMem <= math.MaxInt64 {
			machine.Status.Memory = int64(vm.MaxMem)
		} else {
			machine.Status.Memory = math.MaxInt64
		}
	}

	machine.Status.Provider = vitistackcrdsv1alpha1.MachineProviderTypeProxmox
	if machine.Status.Region == "" {
		machine.Status.Region = node
	}
	if machine.Status.Zone == "" {
		machine.Status.Zone = node
	}

	if vm.Agent && vm.Status == proxmoxStatusRunning {
		r.updateNetworkStatus(ctx, machine, vm)
	}

	logger.V(1).Info("Updated VM status", "state", vm.Status, "cpus", vm.CPUs, "memory", vm.MaxMem, "hostname", vm.Name)
}

// updateNetworkStatus fetches network interface information from QEMU guest agent
func (r *MachineReconciler) updateNetworkStatus(ctx context.Context, machine *vitistackcrdsv1alpha1.Machine, vm *proxmox.VirtualMachine) {
	logger := logf.FromContext(ctx)

	ifaces, err := vm.AgentGetNetworkIFaces(ctx)
	if err != nil {
		logger.V(1).Info("Failed to get network interfaces from QEMU agent", "error", err.Error())
		return
	}

	machine.Status.NetworkInterfaces = nil
	machine.Status.IPAddresses = nil
	machine.Status.IPv6Addresses = nil
	machine.Status.PrivateIPAddresses = nil
	machine.Status.PublicIPAddresses = nil

	var allIPv4, allIPv6 []string

	for _, iface := range ifaces {
		if iface.Name == "lo" {
			continue
		}

		netIfaceStatus := vitistackcrdsv1alpha1.NetworkInterfaceStatus{
			Name:       iface.Name,
			MACAddress: iface.HardwareAddress,
			Type:       "ethernet",
		}

		var ipv4Addrs, ipv6Addrs []string

		for _, ipAddr := range iface.IPAddresses {
			switch ipAddr.IPAddressType {
			case "ipv4":
				ipv4Addrs = append(ipv4Addrs, ipAddr.IPAddress)
				allIPv4 = append(allIPv4, ipAddr.IPAddress)
			case "ipv6":
				if !strings.HasPrefix(ipAddr.IPAddress, "fe80:") {
					ipv6Addrs = append(ipv6Addrs, ipAddr.IPAddress)
					allIPv6 = append(allIPv6, ipAddr.IPAddress)
				}
			}
		}

		netIfaceStatus.IPAddresses = ipv4Addrs
		netIfaceStatus.IPv6Addresses = ipv6Addrs

		if len(ipv4Addrs) > 0 || len(ipv6Addrs) > 0 {
			netIfaceStatus.State = "up"
		}

		machine.Status.NetworkInterfaces = append(machine.Status.NetworkInterfaces, netIfaceStatus)
	}

	machine.Status.IPAddresses = allIPv4
	machine.Status.IPv6Addresses = allIPv6

	for _, ip := range allIPv4 {
		if network.IsPrivateIP(ip) {
			machine.Status.PrivateIPAddresses = append(machine.Status.PrivateIPAddresses, ip)
		} else {
			machine.Status.PublicIPAddresses = append(machine.Status.PublicIPAddresses, ip)
		}
	}

	logger.V(1).Info("Updated network status", "interfaces", len(machine.Status.NetworkInterfaces), "ipv4", allIPv4, "ipv6", allIPv6)
}

// validateISO checks if the specified ISO exists in Proxmox storage
func (r *MachineReconciler) validateISO(ctx context.Context, node string, isoPath string) error {
	logger := logf.FromContext(ctx)

	if !strings.HasPrefix(isoPath, "local:") {
		logger.Info("Skipping ISO validation for non-local path", "isoPath", isoPath)
		return nil
	}

	parts := strings.SplitN(isoPath, ":", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid ISO path format: %s", isoPath)
	}

	storageName := parts[0]

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

	for _, item := range content {
		if item.Volid == isoPath {
			logger.Info("ISO validated successfully", "isoPath", isoPath, "node", node)
			return nil
		}
	}

	availableISOs := make([]string, 0, len(content))
	for _, item := range content {
		availableISOs = append(availableISOs, item.Volid)
	}
	logger.Info("Available ISOs in storage", "storage", storageName, "isos", availableISOs)

	return fmt.Errorf("ISO not found in storage %s: %s (available: %v)", storageName, isoPath, availableISOs)
}

// generateVMID generates a VM ID based on the machine UID
func (r *MachineReconciler) generateVMID(machine *vitistackcrdsv1alpha1.Machine) int {
	vmIDStart := viper.GetInt(consts.PROXMOX_VM_ID_START)
	uidStr := string(machine.UID)
	uidInt := 0
	for _, r := range uidStr {
		uidInt += int(r)
	}
	return vmIDStart + uidInt%1000
}

// getVLANTag determines the VLAN tag for a machine
// Priority: NetworkNamespace in machine's namespace -> Default VLAN env var -> 0 (no VLAN)
func (r *MachineReconciler) getVLANTag(ctx context.Context, machine *vitistackcrdsv1alpha1.Machine) int {
	logger := logf.FromContext(ctx)
	defaultVLAN := viper.GetInt(consts.PROXMOX_DEFAULT_VLAN)

	// List NetworkNamespaces in the machine's namespace
	var networkNamespaceList vitistackcrdsv1alpha1.NetworkNamespaceList
	if err := r.List(ctx, &networkNamespaceList, client.InNamespace(machine.Namespace)); err != nil {
		logger.Info("Failed to list NetworkNamespaces, using default VLAN",
			"namespace", machine.Namespace,
			"defaultVLAN", defaultVLAN,
			"error", err.Error())
		return defaultVLAN
	}

	// Check if any NetworkNamespace exists in the namespace
	if len(networkNamespaceList.Items) == 0 {
		logger.Info("No NetworkNamespace found in namespace, using default VLAN",
			"namespace", machine.Namespace,
			"defaultVLAN", defaultVLAN)
		return defaultVLAN
	}

	// Use the first NetworkNamespace (typically there's one per namespace)
	networkNamespace := networkNamespaceList.Items[0]

	// Check if VLAN ID is set in the NetworkNamespace status
	if networkNamespace.Status.VlanID == 0 {
		logger.Info("NetworkNamespace has no VLAN ID set, using default VLAN",
			"namespace", machine.Namespace,
			"networkNamespace", networkNamespace.Name,
			"defaultVLAN", defaultVLAN)
		return defaultVLAN
	}

	logger.V(1).Info("Using VLAN from NetworkNamespace",
		"namespace", machine.Namespace,
		"networkNamespace", networkNamespace.Name,
		"vlanID", networkNamespace.Status.VlanID)

	return networkNamespace.Status.VlanID
}

// getMTU determines the MTU for a machine's network interface
// Priority: Machine spec network interfaces -> Default MTU env var -> 0 (use Proxmox default of 1500)
func (r *MachineReconciler) getMTU(machine *vitistackcrdsv1alpha1.Machine) int {
	// TODO: Check if Machine spec has network interfaces with MTU configuration
	// The Machine CRD doesn't currently have MTU in interfaces, but this allows for future extension
	_ = machine // Reserved for future use when Machine spec supports MTU

	// Return default MTU from environment if set
	return viper.GetInt(consts.PROXMOX_DEFAULT_MTU)
}

// mapProxmoxStatusToState maps Proxmox VM status to vitistack MachinePhase values
// Proxmox statuses: running, stopped, paused, suspended, prelaunch
func mapProxmoxStatusToState(proxmoxStatus string) string {
	switch strings.ToLower(proxmoxStatus) {
	case proxmoxStatusRunning:
		return vitistackcrdsv1alpha1.MachinePhaseRunning
	case proxmoxStatusStopped:
		return vitistackcrdsv1alpha1.MachinePhaseStopped
	case proxmoxStatusPaused, proxmoxStatusSuspended:
		return vitistackcrdsv1alpha1.MachinePhaseStopping
	case proxmoxStatusPrelaunch:
		return vitistackcrdsv1alpha1.MachinePhaseCreating
	default:
		// For unknown statuses, capitalize the first letter
		if len(proxmoxStatus) > 0 {
			return strings.ToUpper(proxmoxStatus[:1]) + strings.ToLower(proxmoxStatus[1:])
		}
		return proxmoxStatus
	}
}
