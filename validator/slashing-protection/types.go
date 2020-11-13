package slashingprotection

import (
	"context"

	eth "github.com/prysmaticlabs/ethereumapis/eth/v1alpha1"
)

// Protector interface defines a struct which provides methods
// for validator slashing protection.
type Protector interface {
	IsSlashableAttestation(ctx context.Context, attestation *eth.IndexedAttestation) bool
	CommitAttestation(ctx context.Context, attestation *eth.IndexedAttestation) bool
	IsSlashableBlock(ctx context.Context, blockHeader *eth.BeaconBlockHeader) bool
	CommitBlock(ctx context.Context, blockHeader *eth.SignedBeaconBlockHeader) (bool, error)
}
