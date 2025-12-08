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

// Package nodeselection provides Proxmox node selection strategies
package nodeselection

import (
	"context"
	"fmt"
	"math/rand"
	"strings"

	"github.com/spf13/viper"
	"github.com/vitistack/proxmox-operator/internal/consts"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// Strategy represents a node selection strategy
type Strategy string

const (
	// StrategyFirst selects the first available node
	StrategyFirst Strategy = "first"
	// StrategyRandom selects a random node
	StrategyRandom Strategy = "random"
	// StrategyRoundRobin selects nodes in round-robin fashion
	StrategyRoundRobin Strategy = "round-robin"
)

// Selector handles node selection for VM placement
type Selector struct{}

// NewSelector creates a new node selector
func NewSelector() *Selector {
	return &Selector{}
}

// SelectNode selects a Proxmox node based on the configured strategy
func (s *Selector) SelectNode(ctx context.Context, nodeNames []string) (string, error) {
	logger := logf.FromContext(ctx)

	if len(nodeNames) == 0 {
		return "", fmt.Errorf("no Proxmox nodes available")
	}

	// Get allowed nodes filter
	candidateNodes := s.filterAllowedNodes(nodeNames)

	if len(candidateNodes) == 0 {
		allowedNodesStr := viper.GetString(consts.PROXMOX_ALLOWED_NODES)
		return "", fmt.Errorf("no allowed Proxmox nodes available from list: %v", allowedNodesStr)
	}

	// Get selection strategy
	strategy := Strategy(viper.GetString(consts.PROXMOX_NODE_SELECTION))
	if strategy == "" {
		strategy = StrategyFirst // Default
	}

	logger.Info("Selecting node", "strategy", strategy, "candidates", candidateNodes)

	return s.selectByStrategy(strategy, candidateNodes, logger), nil
}

// filterAllowedNodes filters nodes based on the allowed list
func (s *Selector) filterAllowedNodes(nodeNames []string) []string {
	allowedNodesStr := viper.GetString(consts.PROXMOX_ALLOWED_NODES)

	if allowedNodesStr == "" {
		return nodeNames // All nodes allowed
	}

	allowedNodes := strings.Split(allowedNodesStr, ",")
	for i, node := range allowedNodes {
		allowedNodes[i] = strings.TrimSpace(node)
	}

	var candidateNodes []string
	for _, node := range nodeNames {
		for _, allowed := range allowedNodes {
			if node == allowed {
				candidateNodes = append(candidateNodes, node)
				break
			}
		}
	}

	return candidateNodes
}

// selectByStrategy selects a node based on the given strategy
func (s *Selector) selectByStrategy(strategy Strategy, candidateNodes []string, logger interface{ Info(string, ...interface{}) }) string {
	switch strategy {
	case StrategyFirst:
		return candidateNodes[0]
	case StrategyRandom:
		return candidateNodes[rand.Intn(len(candidateNodes))] // #nosec G404 - Node selection doesn't require cryptographic randomness
	case StrategyRoundRobin:
		// TODO: Implement proper round-robin with stored state
		// For now, use random as round-robin would require state persistence
		return candidateNodes[rand.Intn(len(candidateNodes))] // #nosec G404 - Node selection doesn't require cryptographic randomness
	default:
		logger.Info("Unknown node selection strategy, using 'first'", "strategy", strategy)
		return candidateNodes[0]
	}
}
