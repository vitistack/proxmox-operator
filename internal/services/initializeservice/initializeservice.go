package initializeservice

import (
	"context"
	"fmt"

	"github.com/spf13/viper"
	"github.com/vitistack/common/pkg/loggers/vlog"
	"github.com/vitistack/common/pkg/operator/crdcheck"
	vitistackcrdsv1alpha1 "github.com/vitistack/common/pkg/v1alpha1"
	"github.com/vitistack/proxmox-operator/internal/consts"
	"github.com/vitistack/proxmox-operator/internal/services/proxmox"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func CheckPrerequisites() {
	vlog.Info("Running prerequisite checks...")

	crdcheck.MustEnsureInstalled(context.TODO(),
		// your CRD plural
		crdcheck.Ref{Group: "vitistack.io", Version: "v1alpha1", Resource: "machines"},
		crdcheck.Ref{Group: "vitistack.io", Version: "v1alpha1", Resource: "machineclasses"},
	)

	// Check Proxmox connection
	if err := CheckProxmoxConnection(); err != nil {
		vlog.Fatalf("❌ Proxmox connection check failed: %v", err)
	}

	vlog.Info("✅ Prerequisite checks passed")
}

func CheckProxmoxConnection() error {
	vlog.Info("Testing Proxmox connection...")

	endpoint := viper.GetString(consts.PROXMOX_ENDPOINT)
	username := viper.GetString(consts.PROXMOX_USERNAME)
	password := viper.GetString(consts.PROXMOX_PASSWORD)
	tokenID := viper.GetString(consts.PROXMOX_TOKEN_ID)
	tokenSecret := viper.GetString(consts.PROXMOX_TOKEN_SECRET)
	insecure := viper.GetBool(consts.PROXMOX_INSECURE_TLS)

	if endpoint == "" {
		return fmt.Errorf("proxmox configuration incomplete: endpoint is required")
	}

	// Check authentication - either username/password or token
	hasCredentials := username != "" && password != ""
	hasToken := tokenID != "" && tokenSecret != ""

	if !hasCredentials && !hasToken {
		return fmt.Errorf("proxmox configuration incomplete: either username/password or token authentication is required")
	}

	if hasCredentials && hasToken {
		vlog.Info("Both username/password and token provided, using token authentication")
	}

	var proxmoxclient proxmox.ProxmoxClient
	var err error

	if hasToken {
		proxmoxclient, err = proxmox.NewProxmoxClient(
			proxmox.WithEndpoint(endpoint),
			proxmox.WithToken(tokenID, tokenSecret),
			proxmox.WithInsecureTLS(insecure),
		)
	} else {
		proxmoxclient, err = proxmox.NewProxmoxClientFromConfig(endpoint, username, password, "", "", insecure)
	}

	if err != nil {
		return fmt.Errorf("failed to create Proxmox client: %w", err)
	}

	if err := proxmoxclient.TestConnection(context.TODO()); err != nil {
		return fmt.Errorf("failed to connect to Proxmox: %w", err)
	}

	vlog.Info("✅ Proxmox connection successful")
	return nil
}

// EnsureMachineProvider checks if VitiStack CRD exists and creates a MachineProvider if needed
// This should be called after the controller-runtime manager is started to have access to the client
func EnsureMachineProvider(ctx context.Context, k8sClient client.Client) error {
	vlog.Info("Checking for VitiStack and MachineProvider...")

	machineProviderName := viper.GetString(consts.NAME_MACHINE_PROVIDER)
	if machineProviderName == "" {
		vlog.Info("NAME_MACHINE_PROVIDER not configured, skipping MachineProvider auto-creation")
		return nil
	}

	// List all VitiStack objects - if this fails, VitiStack CRD is not installed or not accessible
	var vitistackList vitistackcrdsv1alpha1.VitistackList
	if err := k8sClient.List(ctx, &vitistackList); err != nil {
		vlog.Infof("VitiStack not available (CRD may not be installed): %v", err)
		return nil // Non-fatal, just skip
	}

	if len(vitistackList.Items) == 0 {
		vlog.Info("No VitiStack objects found in cluster, skipping MachineProvider auto-creation")
		return nil
	}

	// Use the first VitiStack found
	vitistack := vitistackList.Items[0]
	vlog.Infof("Found VitiStack: %s (region: %s, zone: %s)", vitistack.Name, vitistack.Spec.Region, vitistack.Spec.Zone)

	// Check if MachineProvider already exists
	var existingProvider vitistackcrdsv1alpha1.MachineProvider
	err := k8sClient.Get(ctx, client.ObjectKey{Name: machineProviderName}, &existingProvider)
	if err == nil {
		vlog.Infof("MachineProvider '%s' already exists, skipping creation", machineProviderName)
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to check existing MachineProvider: %w", err)
	}

	// Create MachineProvider from VitiStack info
	machineProvider := buildMachineProviderFromVitiStack(machineProviderName, &vitistack)

	if err := k8sClient.Create(ctx, machineProvider); err != nil {
		return fmt.Errorf("failed to create MachineProvider: %w", err)
	}

	vlog.Infof("✅ Created MachineProvider '%s' from VitiStack '%s'", machineProviderName, vitistack.Name)
	return nil
}

// buildMachineProviderFromVitiStack creates a MachineProvider spec from VitiStack information
func buildMachineProviderFromVitiStack(name string, vitistack *vitistackcrdsv1alpha1.Vitistack) *vitistackcrdsv1alpha1.MachineProvider {
	// Build display name
	displayName := fmt.Sprintf("Proxmox Provider - %s", vitistack.Spec.DisplayName)
	if displayName == "Proxmox Provider - " {
		displayName = fmt.Sprintf("Proxmox Provider - %s", name)
	}

	// Build zones array from zone if provided
	var zones []string
	if vitistack.Spec.Zone != "" {
		zones = []string{vitistack.Spec.Zone}
	}

	// Get Proxmox endpoint configuration
	endpoint := viper.GetString(consts.PROXMOX_ENDPOINT)
	insecure := viper.GetBool(consts.PROXMOX_INSECURE_TLS)

	machineProvider := &vitistackcrdsv1alpha1.MachineProvider{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				"vitistack.io/vitistack":     vitistack.Name,
				"vitistack.io/provider-type": string(vitistackcrdsv1alpha1.MachineProviderTypeProxmox),
				"vitistack.io/operator":      "proxmox-operator",
			},
		},
		Spec: vitistackcrdsv1alpha1.MachineProviderSpec{
			ProviderType: string(vitistackcrdsv1alpha1.MachineProviderTypeProxmox),
			DisplayName:  displayName,
			Region:       vitistack.Spec.Region,
			Zones:        zones,
			Endpoint: vitistackcrdsv1alpha1.ProviderEndpoint{
				URL:                endpoint,
				InsecureSkipVerify: insecure,
			},
			Authentication: vitistackcrdsv1alpha1.ProviderAuthentication{
				Type: "token", // Default to token auth as it's preferred
			},
			DefaultTags: map[string]string{
				"vitistack":      vitistack.Name,
				"region":         vitistack.Spec.Region,
				"infrastructure": vitistack.Spec.Infrastructure,
			},
		},
	}

	// Add location info if available
	if vitistack.Spec.Location.Country != "" {
		if machineProvider.Spec.DefaultTags == nil {
			machineProvider.Spec.DefaultTags = make(map[string]string)
		}
		machineProvider.Spec.DefaultTags["country"] = vitistack.Spec.Location.Country
	}

	return machineProvider
}
