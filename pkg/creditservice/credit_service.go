package creditservice

import (
	"context"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/DIMO-Network/cloudevent"
	"github.com/DIMO-Network/credit-tracker/pkg/grpc"
)

const creditsFromBurn = 50_000

type Repository interface {
	UpdateCredits(developerLicense, assetDid string, amount int64) (int64, error)
	GetCredits(developerLicense, assetDid string) (int64, error)
}

// creditTrackerService implements the CreditTrackerService interface
type CreditTrackerService struct {
	repository Repository
}

// NewCreditTrackerService creates a new instance of the credit tracker service
func NewCreditTrackerService(repo Repository) *CreditTrackerService {
	return &CreditTrackerService{
		repository: repo,
	}
}

// CheckCredits implements the credit check operation
func (s *CreditTrackerService) CheckCredits(ctx context.Context, req *grpc.CreditCheckRequest) (*grpc.CreditCheckResponse, error) {
	if _, err := decodeAssetDID(req.AssetDid); err != nil {
		return nil, err
	}

	credits, err := s.repository.GetCredits(req.DeveloperLicense, req.AssetDid)
	if err != nil {
		return nil, status.Error(codes.Internal, "Failed to get credits")
	}

	// Record metrics
	CreditOperations.WithLabelValues("check", req.DeveloperLicense, req.AssetDid, getAmountBucket(credits)).Inc()
	CreditBalance.WithLabelValues(req.DeveloperLicense, req.AssetDid).Set(float64(credits))

	return &grpc.CreditCheckResponse{
		RemainingCredits: credits,
	}, nil
}

// DeductCredits implements the credit deduction operation
func (s *CreditTrackerService) DeductCredits(ctx context.Context, req *grpc.CreditDeductRequest) (*grpc.CreditDeductResponse, error) {
	if _, err := decodeAssetDID(req.AssetDid); err != nil {
		return nil, err
	}

	currentCredits, err := s.repository.GetCredits(req.DeveloperLicense, req.AssetDid)
	if err != nil {
		return nil, status.Error(codes.Internal, "Failed to get credits")
	}
	var newCredits int64
	if currentCredits < req.Amount {
		newCredits = currentCredits + creditsFromBurn - req.Amount
		newCredits, err = s.repository.UpdateCredits(req.DeveloperLicense, req.AssetDid, newCredits)
		if err != nil {
			return nil, status.Error(codes.Internal, "Failed to update credits")
		}
	} else {
		newCredits, err = s.repository.UpdateCredits(req.DeveloperLicense, req.AssetDid, currentCredits-req.Amount)
		if err != nil {
			return nil, status.Error(codes.Internal, "Failed to update credits")
		}
	}

	// Record metrics
	CreditOperations.WithLabelValues("deduct", req.DeveloperLicense, req.AssetDid, getAmountBucket(req.Amount)).Inc()
	CreditBalance.WithLabelValues(req.DeveloperLicense, req.AssetDid).Set(float64(newCredits))

	return &grpc.CreditDeductResponse{
		RemainingCredits: newCredits,
	}, nil
}

// RefundCredits implements the credit refund operation
func (s *CreditTrackerService) RefundCredits(ctx context.Context, req *grpc.RefundCreditsRequest) (*grpc.RefundCreditsResponse, error) {
	if _, err := decodeAssetDID(req.AssetDid); err != nil {
		return nil, err
	}

	currentCredits, err := s.repository.GetCredits(req.DeveloperLicense, req.AssetDid)
	if err != nil {
		return nil, status.Error(codes.Internal, "Failed to get credits")
	}

	newCredits, err := s.repository.UpdateCredits(req.DeveloperLicense, req.AssetDid, currentCredits+req.Amount)
	if err != nil {
		return nil, status.Error(codes.Internal, "Failed to update credits")
	}

	// Record metrics
	CreditOperations.WithLabelValues("refund", req.DeveloperLicense, req.AssetDid, getAmountBucket(req.Amount)).Inc()
	CreditBalance.WithLabelValues(req.DeveloperLicense, req.AssetDid).Set(float64(newCredits))

	return &grpc.RefundCreditsResponse{
		RemainingCredits: newCredits,
	}, nil
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
