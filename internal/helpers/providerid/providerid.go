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

// Package providerid provides functions for parsing and building Proxmox provider IDs
package providerid

import (
	"fmt"
	"strconv"
	"strings"
)

const (
	// ProviderPrefix is the prefix for Proxmox provider IDs
	ProviderPrefix = "proxmox://"
)

// Parse parses a ProviderID in format "proxmox://node/vmID" and returns node and vmID
func Parse(providerID string) (node string, vmID int, err error) {
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

// Build creates a ProviderID from node name and VM ID
func Build(node string, vmID int) string {
	return fmt.Sprintf("%s%s/%d", ProviderPrefix, node, vmID)
}
