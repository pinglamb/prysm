package sync

import (
	"context"
	"encoding/hex"
	"errors"
	"sync"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
	gcache "github.com/patrickmn/go-cache"
	ethpb "github.com/prysmaticlabs/ethereumapis/eth/v1alpha1"
	"github.com/prysmaticlabs/prysm/beacon-chain/core/helpers"
	"github.com/prysmaticlabs/prysm/shared/bls"
	"github.com/prysmaticlabs/prysm/shared/bytesutil"
	"github.com/prysmaticlabs/prysm/shared/params"
	"github.com/prysmaticlabs/prysm/shared/rand"
	"github.com/prysmaticlabs/prysm/shared/runutil"
	"github.com/prysmaticlabs/prysm/shared/slotutil"
	"github.com/sirupsen/logrus"
	"go.opencensus.io/trace"
)

// This defines how often a node cleans up and processes pending attestations in the queue.
var processPendingAttsPeriod = slotutil.DivideSlotBy(2 /* twice per slot */)

// This processes pending attestation queues on every `processPendingAttsPeriod`.
func (s *Service) processPendingAttsQueue() {
	// Prevents multiple queue processing goroutines (invoked by RunEvery) from contending for data.
	mutex := new(sync.Mutex)
	runutil.RunEvery(s.ctx, processPendingAttsPeriod, func() {
		mutex.Lock()
		if err := s.processPendingAtts(s.ctx); err != nil {
			log.WithError(err).Debugf("Could not process pending attestation: %v", err)
		}
		mutex.Unlock()
	})
}

// This defines how pending attestations are processed. It contains features:
// 1. Clean up invalid pending attestations from the queue.
// 2. Check if pending attestations can be processed when the block has arrived.
// 3. Request block from a random peer if unable to proceed step 2.
func (s *Service) processPendingAtts(ctx context.Context) error {
	ctx, span := trace.StartSpan(ctx, "processPendingAtts")
	defer span.End()

	// Before a node processes pending attestations queue, it verifies
	// the attestations in the queue are still valid. Attestations will
	// be deleted from the queue if invalid (ie. getting staled from falling too many slots behind).
	s.validatePendingAtts(ctx, s.chain.CurrentSlot())

	roots := make([][32]byte, 0, s.blkRootToPendingAtts.ItemCount())
	for r := range s.blkRootToPendingAtts.Items() {
		r32 := bytesutil.ToBytes32([]byte(r))
		roots = append(roots, r32)
	}

	var pendingRoots [][32]byte
	randGen := rand.NewGenerator()
	for _, bRoot := range roots {
		value, ok := s.blkRootToPendingAtts.Get(string(bRoot[:]))
		if !ok {
			// This shouldn't happen but if block root doesn't exist in block root to pending attestations queue, continue.
			continue
		}
		attestations, ok := value.([]*ethpb.SignedAggregateAttestationAndProof)
		if !ok {
			// This shouldn't happen but if we can't covert cached item to SignedAggregateAttestationAndProof, continue.
			continue
		}

		// Has the pending attestation's missing block arrived and the node processed block yet?
		hasStateSummary := s.db.HasStateSummary(ctx, bRoot) || s.stateSummaryCache.Has(bRoot)
		if s.db.HasBlock(ctx, bRoot) && (s.db.HasState(ctx, bRoot) || hasStateSummary) {
			numberOfBlocksRecoveredFromAtt.Inc()
			for _, signedAtt := range attestations {
				att := signedAtt.Message
				// The pending attestations can arrive in both aggregated and unaggregated forms,
				// each from has distinct validation steps.
				if helpers.IsAggregated(att.Aggregate) {
					// Save the pending aggregated attestation to the pool if it passes the aggregated
					// validation steps.
					aggValid := s.validateAggregatedAtt(ctx, signedAtt) == pubsub.ValidationAccept
					if s.validateBlockInAttestation(ctx, signedAtt) && aggValid {
						if err := s.attPool.SaveAggregatedAttestation(att.Aggregate); err != nil {
							return err
						}
						numberOfAttsRecovered.Inc()

						// Broadcasting the signed attestation again once a node is able to process it.
						if err := s.p2p.Broadcast(ctx, signedAtt); err != nil {
							log.WithError(err).Debug("Failed to broadcast")
						}
					}
				} else {
					// Save the pending unaggregated attestation to the pool if the BLS signature is
					// valid.
					if _, err := bls.SignatureFromBytes(att.Aggregate.Signature); err != nil {
						continue
					}
					if err := s.attPool.SaveUnaggregatedAttestation(att.Aggregate); err != nil {
						return err
					}
					numberOfAttsRecovered.Inc()

					// Verify signed aggregate has a valid signature.
					if _, err := bls.SignatureFromBytes(signedAtt.Signature); err != nil {
						continue
					}

					// Broadcasting the signed attestation again once a node is able to process it.
					if err := s.p2p.Broadcast(ctx, signedAtt); err != nil {
						log.WithError(err).Debug("Failed to broadcast")
					}
				}
			}
			log.WithFields(logrus.Fields{
				"blockRoot":        hex.EncodeToString(bytesutil.Trunc(bRoot[:])),
				"pendingAttsCount": len(attestations),
			}).Info("Verified and saved pending attestations to pool")

			// Delete the missing block root key from pending attestation queue so a node will not request for the block again.
			s.blkRootToPendingAtts.Delete(string(bRoot[:]))
		} else {
			// Pending attestation's missing block has not arrived yet.
			log.WithFields(logrus.Fields{
				"currentSlot": s.chain.CurrentSlot(),
				"attSlot":     attestations[0].Message.Aggregate.Data.Slot,
				"attCount":    len(attestations),
				"blockRoot":   hex.EncodeToString(bytesutil.Trunc(bRoot[:])),
			}).Debug("Requesting block for pending attestation")
			pendingRoots = append(pendingRoots, bRoot)
		}
	}
	return s.sendBatchRootRequest(ctx, pendingRoots, randGen)
}

// This defines how pending attestations is saved in the map. The key is the
// root of the missing block. The value is the list of pending attestations
// that voted for that block root.
func (s *Service) savePendingAtt(att *ethpb.SignedAggregateAttestationAndProof) error {
	root := bytesutil.ToBytes32(att.Message.Aggregate.Data.BeaconBlockRoot)
	k := string(root[:])

	value, ok := s.blkRootToPendingAtts.Get(k)
	if !ok {
		return s.blkRootToPendingAtts.Add(k, []*ethpb.SignedAggregateAttestationAndProof{att}, gcache.DefaultExpiration)
	}
	atts, ok := value.([]*ethpb.SignedAggregateAttestationAndProof)
	if !ok {
		return errors.New("could not convert item to SignedAggregateAttestationAndProof")
	}

	// Skip if the attestation from the same aggregator already exists in the pending queue.
	for _, a := range atts {
		if a.Message.AggregatorIndex == att.Message.AggregatorIndex {
			return nil
		}
	}

	atts = append(atts, att)
	return s.blkRootToPendingAtts.Add(k, atts, gcache.DefaultExpiration)
}

// This validates the pending attestations in the queue are still valid.
// If not valid, a node will remove it in the queue in place. The validity
// check specifies the pending attestation could not fall one epoch behind
// of the current slot.
func (s *Service) validatePendingAtts(ctx context.Context, slot uint64) error {
	ctx, span := trace.StartSpan(ctx, "validatePendingAtts")
	defer span.End()

	items := s.blkRootToPendingAtts.Items()
	for k, v := range items {
		atts, ok := v.Object.([]*ethpb.SignedAggregateAttestationAndProof)
		if !ok {
			return errors.New("could not convert item to SignedAggregateAttestationAndProof")
		}

		for i := len(atts) - 1; i >= 0; i-- {
			if slot >= atts[i].Message.Aggregate.Data.Slot+params.BeaconConfig().SlotsPerEpoch {
				// Remove the pending attestation from the list in place.
				atts = append(atts[:i], atts[i+1:]...)
				numberOfAttsNotRecovered.Inc()
			}
		}
		if err := s.blkRootToPendingAtts.Add(k, atts, gcache.DefaultExpiration); err != nil {
			return err
		}

		// If the pending attestations list of a given block root is empty,
		// a node will remove the key from the map to avoid dangling keys.
		if len(s.blkRootToPendingAtts[bRoot]) == 0 {
			delete(s.blkRootToPendingAtts, bRoot)
			numberOfBlocksNotRecoveredFromAtt.Inc()
		}
	}
	return nil
}
