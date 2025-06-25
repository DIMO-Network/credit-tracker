package events

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"math/rand"
	"time"

	"github.com/DIMO-Network/cloudevent"
	"github.com/DIMO-Network/credit-tracker/models"
	"github.com/IBM/sarama"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/rs/zerolog"
)

type GrantRepository interface {
	CreateGrant(ctx context.Context, licenseID string, assetDID string, amount uint32, txHash string, mintTime time.Time) (*models.CreditOperation, error)
	ConfirmGrant(ctx context.Context, licenseID string, assetDID string, txHash string, logIndex int, amount uint32, mintTime time.Time) (*models.CreditOperation, error)
}

type ContractProcessor struct {
	grantRepo        GrantRepository
	dcxBurnedEventID string
}

func NewContractProcessor(grantRepo GrantRepository) *ContractProcessor {
	return &ContractProcessor{grantRepo: grantRepo}
}

func (c *ContractProcessor) CreateGrant(ctx context.Context, licenseID string, assetDID string, amount uint32) (*types.Transaction, error) {
	tx := tmpFakeTx()
	_, err := c.grantRepo.CreateGrant(ctx, licenseID, assetDID, amount, tx.Hash().String(), time.Now())
	if err != nil {
		return nil, fmt.Errorf("failed to create grant: %w", err)
	}

	// TODO: remove this once we have a real event handler is implemented
	err = c.handleDCXBurned(ctx, contractEventData{
		TxHash:    tx.Hash().String(),
		LogIndex:  1,
		Arguments: json.RawMessage(fmt.Sprintf(`{"licenseId": "%s", "assetDid": "%s", "amount": %d}`, licenseID, assetDID, amount)),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to handle dcx burned: %w", err)
	}
	return tx, nil
}

const (
	contractEventType = "zone.dimo.contract.event"
)

type contractEventData struct {
	EventSignature string          `json:"eventSignature"`
	Arguments      json.RawMessage `json:"arguments"`
	TxHash         string          `json:"txHash"`
	LogIndex       int             `json:"logIndex"`
}

func (p ContractProcessor) Setup(_ sarama.ConsumerGroupSession) error   { return nil }
func (p ContractProcessor) Cleanup(_ sarama.ConsumerGroupSession) error { return nil }
func (p ContractProcessor) ConsumeClaim(session sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	for {
		select {
		case <-session.Context().Done():
			return nil
		case msg, ok := <-claim.Messages():
			if !ok {
				zerolog.Ctx(session.Context()).Info().Msg("message channel closed")
				return nil
			}

			var event cloudevent.CloudEvent[contractEventData]
			if err := json.Unmarshal(msg.Value, &event); err != nil {
				zerolog.Ctx(session.Context()).Err(err).Msg("failed to parse contract event")
				continue
			}

			if event.Type != contractEventType {
				session.MarkMessage(msg, "")
				continue
			}

			switch event.Data.EventSignature {
			case p.dcxBurnedEventID:
				if err := p.handleDCXBurned(session.Context(), event.Data); err != nil {
					zerolog.Ctx(session.Context()).Err(err).Msg("failed to process dcx burned")
					continue
				}
			default:
				// do nothing
			}

			session.MarkMessage(msg, "")

		}
	}
}

type DCXBurnedData struct {
	LicenseID string `json:"licenseId"`
	AssetDid  string `json:"assetDid"`
	Amount    uint32 `json:"amount"`
}

func (p ContractProcessor) handleDCXBurned(ctx context.Context, data contractEventData) error {
	var burn DCXBurnedData
	if err := json.Unmarshal(data.Arguments, &burn); err != nil {
		return fmt.Errorf("failed to parse dcx burned event: %w", err)
	}

	_, err := p.grantRepo.ConfirmGrant(ctx, burn.LicenseID, burn.AssetDid, data.TxHash, data.LogIndex, burn.Amount, time.Now())
	if err != nil {
		return fmt.Errorf("failed to create grant: %w", err)
	}

	return nil
}

func tmpFakeTx() *types.Transaction {
	randNonce := rand.Uint64()
	return types.NewTx(&types.LegacyTx{
		Nonce:    randNonce,
		To:       &common.Address{},
		Value:    big.NewInt(0),
		Gas:      0,
		GasPrice: big.NewInt(0),
		Data:     []byte{},
	})
}
