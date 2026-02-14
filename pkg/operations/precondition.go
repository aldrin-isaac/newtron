// Package operations provides precondition checking utilities for device configuration.
//
// PreconditionChecker and DependencyChecker are defined in pkg/network and
// re-exported here as type aliases for backward compatibility.
package operations

import "github.com/newtron-network/newtron/pkg/network"

// PreconditionChecker is an alias for network.PreconditionChecker.
// The implementation lives in pkg/network/precondition.go.
type PreconditionChecker = network.PreconditionChecker

// NewPreconditionChecker creates a new precondition checker.
// Delegates to network.NewPreconditionChecker (single source of truth).
var NewPreconditionChecker = network.NewPreconditionChecker

// DependencyChecker is an alias for network.DependencyChecker.
// Use network.NewDependencyChecker to create instances.
type DependencyChecker = network.DependencyChecker

// NewDependencyChecker creates a new dependency checker.
// Delegates to network.NewDependencyChecker (single source of truth).
var NewDependencyChecker = network.NewDependencyChecker
