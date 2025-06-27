package creditservice

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// CreditOperations tracks credit operations with labels for operation type, developer license, asset DID, and amount bucket
	CreditOperations = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "credit_tracker_operations_total",
			Help: "Total number of credit operations performed by the credit tracker service",
		},
		[]string{"operation", "developer_license", "amount_bucket"},
	)

	// CreditBalance tracks the current credit balance
	CreditBalance = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "credit_tracker_balance",
			Help: "Current credit balance tracked by the credit tracker service",
		},
		[]string{"developer_license"},
	)
)

// getAmountBucket returns a string label for the amount bucket
func getAmountBucket(amount int64) string {
	switch {
	case amount == 1:
		return "1"
	case amount == 2:
		return "2"
	case amount >= 3 && amount <= 5:
		return "3-5"
	case amount > 5 && amount <= 10:
		return "6-10"
	case amount > 10 && amount <= 50:
		return "11-50"
	case amount > 50 && amount <= 100:
		return "51-100"
	case amount > 100 && amount <= 500:
		return "101-500"
	case amount > 500 && amount <= 1000:
		return "501-1000"
	default:
		return "1000+"
	}
}
