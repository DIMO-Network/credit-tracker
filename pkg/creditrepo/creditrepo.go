package creditrepo

import (
	"context"
	"sync"
	"sync/atomic"
)

const InsufficientCreditsErr = constError("insufficient credits: would result in negative balance")

type constError string

func (e constError) Error() string {
	return string(e)
}

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

// UpdateCredits updates the credits for a developer license and asset DID by adding the amount
// A positive amount adds credits, a negative amount deducts credits
func (r *Repository) UpdateCredits(_ context.Context, developerLicense, assetDid string, amount int64) (int64, error) {
	// Check if developer exists under read lock
	r.mu.RLock()
	devCredits, devExists := r.credits[developerLicense]
	if !devExists {
		r.mu.RUnlock()
		// Create developer map under write lock
		r.mu.Lock()
		// Double-check after acquiring write lock
		if _, exists := r.credits[developerLicense]; !exists {
			r.credits[developerLicense] = make(map[string]*atomic.Int64)
		}
		devCredits = r.credits[developerLicense]
		r.mu.Unlock()
		// Switch back to read lock for asset check
		r.mu.RLock()
	}

	// Check if asset exists under read lock
	atomicVal, exists := devCredits[assetDid]
	if !exists {
		r.mu.RUnlock()
		// Create atomic value under write lock
		r.mu.Lock()
		if _, exists := r.credits[developerLicense][assetDid]; !exists {
			devCredits[assetDid] = &atomic.Int64{}
		}
		atomicVal = devCredits[assetDid]
		r.mu.Unlock()
	} else {
		r.mu.RUnlock()
	}

	// Update value atomically without any locks
	newAmount := atomicVal.Add(amount)

	// Check if the result would be negative
	if newAmount < 0 {
		// Revert the change
		atomicVal.Add(-amount)
		return 0, InsufficientCreditsErr
	}

	return newAmount, nil
}

// GetCredits returns the credits for a developer license and asset DID
func (r *Repository) GetCredits(_ context.Context, developerLicense, assetDid string) (int64, error) {
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
