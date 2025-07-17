package rpc

import (
	"context"
	"errors"
	"fmt"

	"github.com/DIMO-Network/cloudevent"
	"github.com/DIMO-Network/credit-tracker/internal/creditrepo"
	"github.com/DIMO-Network/credit-tracker/models"
	"github.com/DIMO-Network/credit-tracker/pkg/grpc"
	"github.com/ethereum/go-ethereum/core/types"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const creditsFromBurn = 50_000

type Repository interface {
	DeductCredits(ctx context.Context, licenseID string, assetDID string, amount uint64, appName string, referenceID string) (*models.CreditOperation, error)
	RefundCredits(ctx context.Context, appName string, referenceID string) (*models.CreditOperation, error)
	GetBalance(ctx context.Context, licenseID, assetDID string) (int64, error)
}

type ContractProcessor interface {
	CreateGrant(ctx context.Context, licenseID string, assetDID string, amount uint64) (*types.Transaction, error)
}

// CreditTrackerServer represents the gRPC server
type CreditTrackerServer struct {
	grpc.UnimplementedCreditTrackerServer
	repository        Repository
	contractProcessor ContractProcessor
}

// NewServer creates a new instance of the gRPC server
func NewServer(repo Repository, contractProcessor ContractProcessor) *CreditTrackerServer {
	server := &CreditTrackerServer{
		repository:        repo,
		contractProcessor: contractProcessor,
	}

	return server
}

// DeductCredits implements the gRPC service method
func (s *CreditTrackerServer) DeductCredits(ctx context.Context, req *grpc.CreditDeductRequest) (*grpc.CreditDeductResponse, error) {
	if _, err := decodeAssetDID(req.AssetDid); err != nil {
		return nil, err
	}

	// First attempt to deduct credits
	_, err := s.repository.DeductCredits(ctx, req.DeveloperLicense, req.AssetDid, req.Amount, req.AppName, req.ReferenceId)
	for errors.Is(err, creditrepo.InsufficientCreditsErr) {
		err = s.addBurnCredits(ctx, req.DeveloperLicense, req.AssetDid)
		if err != nil {
			return nil, status.Error(codes.Internal, fmt.Sprintf("Failed to add credits after burn: %v", err))
		}
		// Try again now that the developer should have credits
		_, err = s.repository.DeductCredits(ctx, req.DeveloperLicense, req.AssetDid, req.Amount, req.AppName, req.ReferenceId)
		if err != nil {
			return nil, status.Error(codes.Internal, fmt.Sprintf("Failed to deduct credits after burn: %v", err))
		}
	}
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("Failed to deduct credits: %v", err))
	}

	// Record metrics
	CreditOperations.WithLabelValues("deduct", req.DeveloperLicense, getAmountBucket(int64(req.Amount))).Inc()

	return &grpc.CreditDeductResponse{}, nil
}

// RefundCredits implements the gRPC service method
func (s *CreditTrackerServer) RefundCredits(ctx context.Context, req *grpc.RefundCreditsRequest) (*grpc.RefundCreditsResponse, error) {
	operation, err := s.repository.RefundCredits(ctx, req.AppName, req.ReferenceId)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("Failed to refund credits: %v", err))
	}

	// Record metrics
	CreditOperations.WithLabelValues("refund", operation.LicenseID, getAmountBucket(operation.TotalAmount)).Inc()
	CreditBalance.WithLabelValues(operation.LicenseID).Set(float64(operation.TotalAmount))

	return &grpc.RefundCreditsResponse{}, nil
}

func decodeAssetDID(assetDid string) (cloudevent.ERC721DID, error) {
	did, err := cloudevent.DecodeERC721DID(assetDid)
	if err == nil {
		return did, nil
	}
	grpcStatus := status.New(codes.InvalidArgument, "Invalid asset DID")
	errorInfo := &errdetails.ErrorInfo{
		Reason: grpc.ErrorReason_ERROR_REASON_INVALID_ASSET_DID.String(),
		Domain: grpc.ErrorDomain_ERROR_DOMAIN_CREDIT_TRACKER.String(),
		Metadata: map[string]string{
			grpc.MetadataKey_METADATA_KEY_ASSET_DID.String(): assetDid,
		},
	}
	grpcStatus, err = grpcStatus.WithDetails(errorInfo)
	if err != nil {
		return cloudevent.ERC721DID{}, status.Error(codes.Internal, "Failed to create error details")
	}
	return cloudevent.ERC721DID{}, grpcStatus.Err()
}

// HandleInsufficientCredits handles the case where the user has insufficient credits
func HandleInsufficientCredits(ctx context.Context, assetDid string, hasCredits bool, hasDCXAndIntiatedTransaction bool) error {
	if !hasCredits {
		if hasDCXAndIntiatedTransaction {
			txHash := "0x123"
			st := status.New(codes.FailedPrecondition, "No credits available, but transaction initiated")
			errorInfo := &errdetails.ErrorInfo{
				Reason: grpc.ErrorReason_ERROR_REASON_INSUFFICIENT_CREDITS.String(),
				Domain: grpc.ErrorDomain_ERROR_DOMAIN_CREDIT_TRACKER.String(),
				Metadata: map[string]string{
					grpc.MetadataKey_METADATA_KEY_ASSET_DID.String():        assetDid,
					grpc.MetadataKey_METADATA_KEY_TRANSACTION_HASH.String(): txHash,
				},
			}
			st, err := st.WithDetails(errorInfo)
			if err != nil {
				return status.Error(codes.Internal, "Failed to create error details")
			}
			return st.Err()
		}
		// No credits and no transaction - don't retry
		st := status.New(codes.FailedPrecondition, "No credits available")
		retryInfo := &errdetails.ErrorInfo{
			Reason: grpc.ErrorReason_ERROR_REASON_INSUFFICIENT_CREDITS.String(),
			Domain: grpc.ErrorDomain_ERROR_DOMAIN_CREDIT_TRACKER.String(),
			Metadata: map[string]string{
				grpc.MetadataKey_METADATA_KEY_ASSET_DID.String(): assetDid,
			},
		}
		st, err := st.WithDetails(retryInfo)
		if err != nil {
			return status.Error(codes.Internal, "Failed to create error details")
		}
		return st.Err()
	}
	return nil
}

// addBurnCredits adds burn credits to the user's balance then tries to deduct the amount requested.
func (s *CreditTrackerServer) addBurnCredits(ctx context.Context, developerLicense, assetDid string) error {
	// Add burn credits
	_, err := s.contractProcessor.CreateGrant(ctx, developerLicense, assetDid, creditsFromBurn)
	if err != nil {
		if errors.Is(err, creditrepo.GrantAlreadyExistsErr) {
			// Someone else has already created a grant for this license and asset, so we can just return
			return nil
		}
		return fmt.Errorf("failed to create grant transaction: %w", err)
	}
	// Record burn credit metric
	CreditOperations.WithLabelValues("burn", developerLicense, getAmountBucket(creditsFromBurn)).Inc()

	return nil
}
