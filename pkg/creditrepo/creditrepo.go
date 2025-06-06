package creditrepo

import (
	"fmt"
	"sync"
	"sync/atomic"
)

// Repository implements the credit repository interface
type Repository struct {
	mu      sync.RWMutex
	credits map[string]map[string]*atomic.Int64 // map[developerLicense]map[assetDid]credits
}

// New creates a new instance of the credit repository
func New() *Repository {
	return &Repository{
		credits: make(map[string]map[string]*atomic.Int64),
	}
}

// UpdateCredits updates the credits for a developer license and asset DID
func (r *Repository) UpdateCredits(developerLicense, assetDid string, amount int64) (int64, error) {
	if amount < 0 {
		return 0, fmt.Errorf("credit amount cannot be negative")
	}

	r.mu.Lock()
	// Initialize the inner map if it doesn't exist
	if _, exists := r.credits[developerLicense]; !exists {
		r.credits[developerLicense] = make(map[string]*atomic.Int64)
	}

	// Initialize the atomic value if it doesn't exist
	if _, exists := r.credits[developerLicense][assetDid]; !exists {
		r.credits[developerLicense][assetDid] = &atomic.Int64{}
	}
	r.mu.Unlock()

	// Update the credits atomically
	r.credits[developerLicense][assetDid].Store(amount)
	return amount, nil
}

// GetCredits returns the credits for a developer license and asset DID
func (r *Repository) GetCredits(developerLicense, assetDid string) (int64, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Return 0 if the developer license or asset DID doesn't exist
	if credits, exists := r.credits[developerLicense]; exists {
		if amount, exists := credits[assetDid]; exists {
			return amount.Load(), nil
		}
	}
	return 0, nil
}
