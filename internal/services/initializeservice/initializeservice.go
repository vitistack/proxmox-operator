package initializeservice

import (
	"context"
	"fmt"

	"github.com/spf13/viper"
	"github.com/vitistack/common/pkg/loggers/vlog"
	"github.com/vitistack/common/pkg/operator/crdcheck"
	"github.com/vitistack/proxmox-operator/internal/consts"
	"github.com/vitistack/proxmox-operator/internal/services/proxmox"
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

	var client proxmox.ProxmoxClient
	var err error

	if hasToken {
		client, err = proxmox.NewProxmoxClient(
			proxmox.WithEndpoint(endpoint),
			proxmox.WithToken(tokenID, tokenSecret),
			proxmox.WithInsecureTLS(insecure),
		)
	} else {
		client, err = proxmox.NewProxmoxClientFromConfig(endpoint, username, password, insecure)
	}

	if err != nil {
		return fmt.Errorf("failed to create Proxmox client: %w", err)
	}

	if err := client.TestConnection(context.TODO()); err != nil {
		return fmt.Errorf("failed to connect to Proxmox: %w", err)
	}

	vlog.Info("✅ Proxmox connection successful")
	return nil
}
