package derived

import (
	"context"
	"encoding/hex"
	"fmt"
	"testing"

	"github.com/prysmaticlabs/prysm/shared/bls"
	"github.com/prysmaticlabs/prysm/shared/testutil/assert"
	"github.com/prysmaticlabs/prysm/shared/testutil/require"
	mock "github.com/prysmaticlabs/prysm/validator/accounts/testing"
)

func TestDerivationFromMnemonic(t *testing.T) {
	secretKeysCache = make(map[[48]byte]bls.SecretKey)
	mnemonic := "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"
	passphrase := "TREZOR"
	seed := "c55257c360c07c72029aebc1b53c05ed0362ada38ead3e3e9efa3708e53495531f09a6987599d18264c1e1c92f2cf141630c7a3c4ab7c81b2f001698e7463b04"
	masterSK := "6083874454709270928345386274498605044986640685124978867557563392430687146096"
	childIndex := 0
	childSK := "20397789859736650942317412262472558107875392172444076792671091975210932703118"
	ctx := context.Background()
	wallet := &mock.Wallet{
		Files:            make(map[string]map[string][]byte),
		AccountPasswords: make(map[string]string),
		WalletPassword:   "secretPassw0rd$1999",
	}
	km, err := KeymanagerForPhrase(ctx, &SetupConfig{
		Opts:             DefaultKeymanagerOpts(),
		Wallet:           wallet,
		Mnemonic:         mnemonic,
		Mnemonic25thWord: passphrase,
	})
	require.NoError(t, err)
	seedBytes, err := hex.DecodeString(seed)
	require.NoError(t, err)
	assert.DeepEqual(t, seedBytes, km.seed)

	// We derive keys, then check the master SK and the child SK.
	withdrawalKey, err := km.deriveKey("m")
	require.NoError(t, err)
	validatingKey, err := km.deriveKey(fmt.Sprintf("m/%d", childIndex))
	require.NoError(t, err)

	expectedMasterSK, err := bls.SecretKeyFromBigNum(masterSK)
	require.NoError(t, err)
	expectedChildSK, err := bls.SecretKeyFromBigNum(childSK)
	require.NoError(t, err)
	assert.DeepEqual(t, expectedMasterSK.Marshal(), withdrawalKey.Marshal())
	assert.DeepEqual(t, expectedChildSK.Marshal(), validatingKey.Marshal())
}

func TestDerivationFromSeed(t *testing.T) {
	type fields struct {
		seed       string
		childIndex int
	}
	type want struct {
		masterSK string
		childSK  string
	}
	tests := []struct {
		name   string
		fields fields
		want   want
	}{
		{
			name: "Case 1",
			fields: fields{
				seed:       "3141592653589793238462643383279502884197169399375105820974944592",
				childIndex: 3141592653,
			},
			want: want{
				masterSK: "29757020647961307431480504535336562678282505419141012933316116377660817309383",
				childSK:  "25457201688850691947727629385191704516744796114925897962676248250929345014287",
			},
		},
		{
			name: "Case 2",
			fields: fields{
				seed:       "0099FF991111002299DD7744EE3355BBDD8844115566CC55663355668888CC00",
				childIndex: 4294967295,
			},
			want: want{
				masterSK: "27580842291869792442942448775674722299803720648445448686099262467207037398656",
				childSK:  "29358610794459428860402234341874281240803786294062035874021252734817515685787",
			},
		},
		{
			name: "Case 3",
			fields: fields{
				seed:       "d4e56740f876aef8c010b86a40d5f56745a118d0906a34e69aec8c0db1cb8fa3",
				childIndex: 42,
			},
			want: want{
				masterSK: "19022158461524446591288038168518313374041767046816487870552872741050760015818",
				childSK:  "31372231650479070279774297061823572166496564838472787488249775572789064611981",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.name == "Case 1" || tt.name == "Case 2" {
				t.Skip("Skipping due to https://github.com/wealdtech/go-eth2-util/issues/2")
			}
			seedBytes, err := hex.DecodeString(tt.fields.seed)
			require.NoError(t, err)
			km := &Keymanager{
				seed: seedBytes,
				opts: DefaultKeymanagerOpts(),
			}
			// We derive keys, then check the master SK and the child SK.
			masterSK, err := km.deriveKey("m")
			require.NoError(t, err)
			childSK, err := km.deriveKey(fmt.Sprintf("m/%d", tt.fields.childIndex))
			require.NoError(t, err)

			expectedMasterSK, err := bls.SecretKeyFromBigNum(tt.want.masterSK)
			require.NoError(t, err)
			expectedChildSK, err := bls.SecretKeyFromBigNum(tt.want.childSK)
			require.NoError(t, err)
			assert.DeepEqual(t, expectedMasterSK.Marshal(), masterSK.Marshal())
			assert.DeepEqual(t, expectedChildSK.Marshal(), childSK.Marshal())
		})
	}
}