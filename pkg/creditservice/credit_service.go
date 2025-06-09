package creditservice

import (
	"context"
	"errors"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/DIMO-Network/cloudevent"
	"github.com/DIMO-Network/credit-tracker/pkg/creditrepo"
	"github.com/DIMO-Network/credit-tracker/pkg/grpc"
	"github.com/rs/zerolog"
)

const creditsFromBurn = 50_000

type Repository interface {
	UpdateCredits(ctx context.Context, developerLicense, assetDid string, amount int64) (int64, error)
	GetCredits(ctx context.Context, developerLicense, assetDid string) (int64, error)
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

	credits, err := s.repository.GetCredits(ctx, req.DeveloperLicense, req.AssetDid)
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

	// First attempt to deduct credits
	newCredits, err := s.repository.UpdateCredits(ctx, req.DeveloperLicense, req.AssetDid, -req.Amount)
	for errors.Is(err, creditrepo.InsufficientCreditsErr) {
		newCredits, err = s.addBurnCredits(ctx, req.DeveloperLicense, req.AssetDid)
		if err != nil {
			return nil, status.Error(codes.Internal, "Failed to add credits after burn")
		}
		newCredits, err = s.repository.UpdateCredits(ctx, req.DeveloperLicense, req.AssetDid, -req.Amount)
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "Failed to update credits")
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

	newCredits, err := s.repository.UpdateCredits(ctx, req.DeveloperLicense, req.AssetDid, req.Amount)
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

// addBurnCredits adds burn credits to the user's balance then tries to deduct the amount requested.
func (s *CreditTrackerService) addBurnCredits(ctx context.Context, developerLicense, assetDid string) (int64, error) {
	// Add burn credits
	burnCredits, err := s.repository.UpdateCredits(ctx, developerLicense, assetDid, creditsFromBurn)
	if err != nil {
		return 0, err
	}

	// Record burn credit metric
	CreditOperations.WithLabelValues("burn", developerLicense, assetDid, getAmountBucket(creditsFromBurn)).Inc()
	CreditBalance.WithLabelValues(developerLicense, assetDid).Set(float64(burnCredits))
	zerolog.Ctx(ctx).Info().Str("developerLicense", developerLicense).Str("assetDid", assetDid).Int64("newBalance", burnCredits).Msg("Added burn credits")

	return burnCredits, nil
}
