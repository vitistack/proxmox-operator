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

	vitistackcrdsv1alpha1 "github.com/vitistack/crds/pkg/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// MachineReconciler reconciles a Machine object
type MachineReconciler struct {
	client.Client
	Scheme *runtime.Scheme
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

	// Only reconcile Machines for provider == "proxmox"
	provider := machine.Spec.ProviderConfig.Name
	if provider != "proxmox" {
		logger.V(1).Info("Skipping Machine: provider is not proxmox", "provider", provider, "name", req.NamespacedName)
		return ctrl.Result{}, nil
	}

	// TODO: Add proxmox-specific reconciliation logic here

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *MachineReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&vitistackcrdsv1alpha1.Machine{}).
		Named("machine").
		Complete(r)
}
