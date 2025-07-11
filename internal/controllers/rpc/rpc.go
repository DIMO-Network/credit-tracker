package rpc

import (
	"context"

	"github.com/DIMO-Network/credit-tracker/internal/creditservice"
	"github.com/DIMO-Network/credit-tracker/pkg/grpc"
)

// Server represents the gRPC server
type Server struct {
	grpc.UnimplementedCreditTrackerServer
	service *creditservice.CreditTrackerService
}

// NewServer creates a new instance of the gRPC server
func NewServer(svc *creditservice.CreditTrackerService) *Server {
	server := &Server{
		service: svc,
	}

	return server
}

// CheckCredits implements the gRPC service method
func (s *Server) GetBalance(ctx context.Context, req *grpc.GetBalanceRequest) (*grpc.GetBalanceResponse, error) {
	return s.service.GetBalance(ctx, req)
}

// DeductCredits implements the gRPC service method
func (s *Server) DeductCredits(ctx context.Context, req *grpc.CreditDeductRequest) (*grpc.CreditDeductResponse, error) {
	return s.service.DeductCredits(ctx, req)
}

// RefundCredits implements the gRPC service method
func (s *Server) RefundCredits(ctx context.Context, req *grpc.RefundCreditsRequest) (*grpc.RefundCreditsResponse, error) {
	return s.service.RefundCredits(ctx, req)
}
