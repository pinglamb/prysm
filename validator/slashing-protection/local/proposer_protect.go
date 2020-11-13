package local

import (
	"context"

	ethpb "github.com/prysmaticlabs/ethereumapis/eth/v1alpha1"
)

func (s *Service) IsSlashableBlock(ctx context.Context, header *ethpb.BeaconBlockHeader) error {
	fmtKey := fmt.Sprintf("%#x", pubKey[:])
	signingRoot, err := v.db.ProposalHistoryForSlot(ctx, pubKey[:], block.Slot)
	if err != nil {
		if v.emitAccountMetrics {
			ValidatorProposeFailVec.WithLabelValues(fmtKey).Inc()
		}
		return errors.Wrap(err, "failed to get proposal history")
	}
	// If the bit for the current slot is marked, do not propose.
	if !bytes.Equal(signingRoot, params.BeaconConfig().ZeroHash[:]) {
		if v.emitAccountMetrics {
			ValidatorProposeFailVec.WithLabelValues(fmtKey).Inc()
		}
		return errors.New(failedPreBlockSignLocalErr)
	}
}
