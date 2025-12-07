package settings

import (
	"github.com/spf13/viper"
	"github.com/vitistack/common/pkg/loggers/vlog"
	"github.com/vitistack/common/pkg/settings/dotenv"
	"github.com/vitistack/proxmox-operator/internal/consts"
)

var (
	Version = "0.0.0"
	Commit  = "localdev"
)

func Init() {
	// Initialize settings here

	viper.SetDefault(consts.DEVELOPMENT, false)
	viper.SetDefault(consts.LOG_JSON, true)
	viper.SetDefault(consts.LOG_LEVEL, "info")
	viper.SetDefault(consts.IP_SOURCE, "")
	viper.SetDefault(consts.NAME_MACHINE_PROVIDER, "proxmox-provider")

	// Proxmox settings (no defaults - must be configured)
	viper.SetDefault(consts.PROXMOX_INSECURE_TLS, false)
	viper.SetDefault(consts.PROXMOX_VM_ID_START, 2000)
	viper.SetDefault(consts.PROXMOX_ALLOWED_NODES, "")                     // empty means all nodes allowed
	viper.SetDefault(consts.PROXMOX_NODE_SELECTION, "first")               // first, random, round-robin
	viper.SetDefault(consts.PROXMOX_DEFAULT_STORAGE, "local-lvm")          // default storage pool
	viper.SetDefault(consts.PROXMOX_DEFAULT_NETWORK, "vmbr0")              // default network bridge
	viper.SetDefault(consts.PROXMOX_DEFAULT_CPU_TYPE, "x86-64-v2-AES")     // default CPU type
	viper.SetDefault(consts.PROXMOX_ENABLE_NUMA, true)                     // NUMA disabled by default
	viper.SetDefault(consts.PROXMOX_SCSI_CONTROLLER, "virtio-scsi-single") // SCSI controller type
	viper.SetDefault(consts.PROXMOX_ENABLE_QEMU_AGENT, true)               // QEMU Guest Agent enabled by default
	viper.SetDefault(consts.PROXMOX_START_ON_CREATE, true)                 // Start VM after creation

	dotenv.LoadDotEnv()

	viper.AutomaticEnv()

	printEnvironmentSettings()
}

func printEnvironmentSettings() {
	settings := []string{
		consts.LOG_JSON,
		consts.LOG_COLORIZE_LINE,
		consts.LOG_ADD_CALLER,
		consts.LOG_DISABLE_STACKTRACE,
		consts.LOG_UNESCAPED_MULTILINE,
		consts.LOG_LEVEL,
		consts.PROXMOX_ENDPOINT,
		consts.PROXMOX_USERNAME,
		consts.PROXMOX_TOKEN_ID,
		consts.PROXMOX_INSECURE_TLS,
		consts.PROXMOX_VM_ID_START,
		consts.PROXMOX_ALLOWED_NODES,
		consts.PROXMOX_NODE_SELECTION,
		consts.PROXMOX_DEFAULT_STORAGE,
		consts.PROXMOX_DEFAULT_NETWORK,
		consts.PROXMOX_DEFAULT_CPU_TYPE,
		consts.PROXMOX_ENABLE_NUMA,
		consts.PROXMOX_SCSI_CONTROLLER,
	}

	for _, s := range settings {
		val := viper.Get(s)
		if val != nil {
			value := viper.GetString(s)
			if value != "" {
				if s == consts.PROXMOX_PASSWORD || s == consts.PROXMOX_TOKEN_SECRET {
					value = "***masked***"
				}
				// #nosec G202
				vlog.Debug(s + "=" + value)
			}
		}
	}
}
