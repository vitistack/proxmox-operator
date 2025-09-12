package initializeservice

import (
	"context"

	"github.com/vitistack/common/pkg/loggers/vlog"
	"github.com/vitistack/common/pkg/operator/crdcheck"
)

func CheckPrerequisites() {
	vlog.Info("Running prerequisite checks...")

	crdcheck.MustEnsureInstalled(context.TODO(),
		// your CRD plural
		crdcheck.Ref{Group: "vitistack.io", Version: "v1alpha1", Resource: "machines"},
	)
	vlog.Info("✅ Prerequisite checks passed")
}
