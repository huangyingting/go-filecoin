package consensus_test

import (
	"context"
	"encoding/hex"
	"github.com/filecoin-project/go-filecoin/actor"
	"github.com/filecoin-project/go-filecoin/address"
	"github.com/filecoin-project/go-filecoin/consensus"
	"github.com/filecoin-project/go-filecoin/proofs"
	"github.com/filecoin-project/go-filecoin/state"
	"github.com/filecoin-project/go-filecoin/testhelpers"
	"github.com/filecoin-project/go-filecoin/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gx/ipfs/QmQZadYTDF4ud9DdK85PH2vReJRzUM9YfVW4ReB1q2m51p/go-hamt-ipld"
	"gx/ipfs/QmTfTKeBhTLjSjxXQsjkF2b1DfZmYEMnknGE2y2gX57C6v/go-blockservice"
	"gx/ipfs/QmZxjqR9Qgompju73kakSoUj3rbVndAzky3oCDiBNCxPs1/go-ipfs-exchange-offline"

	"gx/ipfs/QmS2aqUZLJp8kF1ihE5rvDGE5LvmKDPnx32w9Z1BW9xLV5/go-ipfs-blockstore"
	"gx/ipfs/QmVmDhyTTUcQXFD1rRQ64fGLMSAoaQvNH3hwuaCFAPq2hy/errors"
	"gx/ipfs/QmcTzQXRcU2vf8yX5EEboz1BSvWC7wWmeYAKVQmhp8WZYU/sha256-simd"
	"gx/ipfs/Qmf4xQhNomPNhrtZc67qSnfJSjxjXs9LWvknJtSXwimPrM/go-datastore"
	"testing"
)

func TestNewExpected(t *testing.T) {
	assert := assert.New(t)
	t.Run("a new Expected can be created", func(t *testing.T) {
		cst, bstore, prover := setupCborBlockstoreProofs()
		ptv := consensus.NewTestPowerTableView(1, 5)
		exp := consensus.NewExpected(cst, bstore, ptv, types.SomeCid(), prover)
		assert.NotNil(exp)
	})
}

// TestExpected_NewValidTipSet also tests validateBlockStructure.
func TestExpected_NewValidTipSet(t *testing.T) {
	assert := assert.New(t)

	cst, bstore, prover := setupCborBlockstoreProofs()
	ptv := consensus.NewTestPowerTableView(1, 5)

	t.Run("NewValidTipSet returns a tipset + nil when valid blocks", func(t *testing.T) {
		ctx, blocks := setUpContextAndBlocks()
		exp := consensus.NewExpected(cst, bstore, ptv, types.SomeCid(), prover)

		tipSet, err := exp.NewValidTipSet(ctx, blocks)
		assert.NoError(err)
		assert.NotNil(tipSet)
	})

	t.Run("NewValidTipSet returns nil + error when invalid blocks", func(t *testing.T) {
		parentBlock := types.NewBlockForTest(nil, 0)

		blocks := []*types.Block{
			types.NewBlockForTest(parentBlock, 1),
		}
		ki := types.MustGenerateKeyInfo(1, types.GenerateKeyInfoSeed())
		mockSigner := types.NewMockSigner(ki)
		blocks[0].Messages = types.NewSignedMsgs(1, mockSigner)
		retVal := []byte{1, 2, 3}

		receipt := &types.MessageReceipt{
			ExitCode: 123,
			Return:   []types.Bytes{retVal},
		}
		blocks[0].MessageReceipts = []*types.MessageReceipt{receipt}

		exp := consensus.NewExpected(cst, bstore, ptv, types.SomeCid(), prover)

		ctx := context.Background()
		tipSet, err := exp.NewValidTipSet(ctx, blocks)
		assert.Error(err, "Foo")
		assert.Nil(tipSet)
	})
}

// TestExpected_RunStateTransition_validateMining is concerned only with validateMining behavior.
// Fully unit-testing RunStateTransition is difficult due to this requiring that you
// completely set up a valid state tree with a valid matching TipSet.  RunStateTransition is tested
// with integration tests (see chain_daemon_test.go for example)
func TestExpected_RunStateTransition_validateMining(t *testing.T) {
	assert := assert.New(t)
	require := require.New(t)

	cistore, bstore, prover := setupCborBlockstoreProofs()
	ctx, blocks := setUpContextAndBlocks()

	vms := testhelpers.VMStorage()
	fakeActorCodeCid := types.AccountActorCodeCid
	newAddressFunc := address.NewForTestGetter()
	addr0, addr1, addr2 := newAddressFunc(), newAddressFunc(), newAddressFunc()
	act0 := testhelpers.RequireNewAccountActor(require, types.NewAttoFILFromFIL(101))
	act1 := testhelpers.RequireNewFakeActorWithTokens(require, vms, addr1, fakeActorCodeCid, types.NewAttoFILFromFIL(102))
	act2 := testhelpers.RequireNewFakeActorWithTokens(require, vms, addr2, fakeActorCodeCid, types.NewAttoFILFromFIL(0))

	_, stateTree := testhelpers.RequireMakeStateTree(require, cistore, map[address.Address]*actor.Actor{
		addr0: act0,
		addr1: act1,
		addr2: act2,
	})

	t.Run("passes the validateMining section when given valid mining blocks", func(t *testing.T) {
		ptv := consensus.NewTestPowerTableView(1, 5)
		exp := consensus.NewExpected(cistore, bstore, ptv, types.SomeCid(), prover)
		tipSet, err := exp.NewValidTipSet(ctx, blocks)
		require.NoError(err)

		_, err = exp.RunStateTransition(ctx, tipSet, stateTree)

		// therefore we know that it passed validateMining() and got to c.runMessages call.
		assert.EqualError(err, "blocks state root does not match computed result")
	})

	t.Run("returns nil + mining error when IsWinningTicket fails due to miner power error", func(t *testing.T) {
		ptv := NewFailingMinerTestPowerTableView(1, 5)
		exp := consensus.NewExpected(cistore, bstore, ptv, types.SomeCid(), prover)
		tipSet, err := exp.NewValidTipSet(ctx, blocks)
		require.NoError(err)

		_, err = exp.RunStateTransition(ctx, tipSet, stateTree)
		assert.EqualError(err, "couldn't compute ticket: Couldn't get minerPower: something went wrong with the miner power")
	})
}

func TestIsWinningTicket(t *testing.T) {
	assert := assert.New(t)

	t.Run("IsWinningTicket returns expected boolean + nil in non-error case", func(t *testing.T) {
		cases := []struct {
			ticket     byte
			myPower    uint64
			totalPower uint64
			wins       bool
		}{
			{0x00, 1, 5, true},
			{0x30, 1, 5, true},
			{0x40, 1, 5, false},
			{0xF0, 1, 5, false},
			{0x00, 5, 5, true},
			{0x33, 5, 5, true},
			{0x44, 5, 5, true},
			{0xFF, 5, 5, true},
			{0x00, 0, 5, false},
			{0x33, 0, 5, false},
			{0x44, 0, 5, false},
			{0xFF, 0, 5, false},
		}

		minerAddress := address.NewForTestGetter()()
		ctx := context.Background()
		d := datastore.NewMapDatastore()
		bs := blockstore.NewBlockstore(d)
		var st state.Tree

		for _, c := range cases {
			ptv := consensus.NewTestPowerTableView(c.myPower, c.totalPower)
			ticket := [sha256.Size]byte{}
			ticket[0] = c.ticket
			r, err := consensus.IsWinningTicket(ctx, bs, ptv, st, ticket[:], minerAddress)
			assert.NoError(err)
			assert.Equal(c.wins, r, "%+v", c)
		}
	})

	testCase := struct {
		ticket     byte
		myPower    int64
		totalPower int64
		wins       bool
	}{0x00, 1, 5, true}

	minerAddress := address.NewForTestGetter()()
	ctx := context.Background()
	d := datastore.NewMapDatastore()
	bs := blockstore.NewBlockstore(d)
	var st state.Tree

	t.Run("IsWinningTicket returns false + error when we fail to get total power", func(t *testing.T) {
		ptv1 := NewFailingTestPowerTableView(testCase.myPower, testCase.totalPower)
		ticket := [sha256.Size]byte{}
		ticket[0] = testCase.ticket
		r, err := consensus.IsWinningTicket(ctx, bs, ptv1, st, ticket[:], minerAddress)
		assert.False(r)
		assert.Equal(err.Error(), "Couldn't get totalPower: something went wrong with the total power")

	})

	t.Run("IsWinningTicket returns false + error when we fail to get miner power", func(t *testing.T) {
		ptv2 := NewFailingMinerTestPowerTableView(testCase.myPower, testCase.totalPower)
		ticket := [sha256.Size]byte{}
		ticket[0] = testCase.ticket
		r, err := consensus.IsWinningTicket(ctx, bs, ptv2, st, ticket[:], minerAddress)
		assert.False(r)
		assert.Equal(err.Error(), "Couldn't get minerPower: something went wrong with the miner power")

	})
}

func TestCreateChallenge(t *testing.T) {
	assert := assert.New(t)

	cases := []struct {
		parentTickets  [][]byte
		nullBlockCount uint64
		challenge      string
	}{
		// From https://www.di-mgt.com.au/sha_testvectors.html
		{[][]byte{[]byte("ac"), []byte("ab"), []byte("xx")},
			uint64('c'),
			"ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"},

		{[][]byte{[]byte("z"), []byte("x"), []byte("abcdbcdecdefdefgefghfghighijhijkijkljklmklmnlmnomnopnop")},
			uint64('q'),
			"248d6a61d20638b8e5c026930c3e6039a33ce45964ff2167f6ecedd419db06c1"},

		{[][]byte{[]byte("abcdefghbcdefghicdefghijdefghijkefghijklfghijklmghijklmnhijklmnoijklmnopjklmnopqklmnopqrlmnopqrsmnopqrstnopqrst"), []byte("z"), []byte("x")},
			uint64('u'),
			"cf5b16a778af8380036ce59e7b0492370b249b11e8f07a51afac45037afee9d1"},
	}

	for _, c := range cases {
		decoded, err := hex.DecodeString(c.challenge)
		assert.NoError(err)

		parents := consensus.TipSet{}
		for _, t := range c.parentTickets {
			b := types.Block{Ticket: t}
			parents.AddBlock(&b)
		}
		r, err := consensus.CreateChallenge(parents, c.nullBlockCount)
		assert.NoError(err)
		assert.Equal(decoded, r)
	}
}

func setupCborBlockstoreProofs() (*hamt.CborIpldStore, blockstore.Blockstore, proofs.Prover) {
	mds := datastore.NewMapDatastore()
	bs := blockstore.NewBlockstore(mds)
	offl := offline.Exchange(bs)
	blkserv := blockservice.New(bs, offl)
	cis := &hamt.CborIpldStore{Blocks: blkserv}
	pv := proofs.NewFakeProver(true, nil)
	return cis, bs, pv
}

func setUpContextAndBlocks() (context.Context, []*types.Block) {
	ctx := context.Background()
	parentBlock := types.NewBlockForTest(nil, 0)
	parentBlock.StateRoot = types.SomeCid()
	blocks := []*types.Block{
		types.NewBlockForTest(parentBlock, 0),
		types.NewBlockForTest(parentBlock, 0),
		types.NewBlockForTest(parentBlock, 0),
	}
	return ctx, blocks
}

type FailingTestPowerTableView struct{ minerPower, totalPower uint64 }

func NewFailingTestPowerTableView(minerPower int64, totalPower int64) *FailingTestPowerTableView {
	return &FailingTestPowerTableView{uint64(minerPower), uint64(totalPower)}
}

func (tv *FailingTestPowerTableView) Total(ctx context.Context, st state.Tree, bstore blockstore.Blockstore) (uint64, error) {
	return tv.totalPower, errors.New("something went wrong with the total power")
}

func (tv *FailingTestPowerTableView) Miner(ctx context.Context, st state.Tree, bstore blockstore.Blockstore, mAddr address.Address) (uint64, error) {
	return uint64(tv.minerPower), nil
}

func (tv *FailingTestPowerTableView) HasPower(ctx context.Context, st state.Tree, bstore blockstore.Blockstore, mAddr address.Address) bool {
	return true
}

type FailingMinerTestPowerTableView struct{ minerPower, totalPower uint64 }

func NewFailingMinerTestPowerTableView(minerPower int64, totalPower int64) *FailingMinerTestPowerTableView {
	return &FailingMinerTestPowerTableView{uint64(minerPower), uint64(totalPower)}
}

func (tv *FailingMinerTestPowerTableView) Total(ctx context.Context, st state.Tree, bstore blockstore.Blockstore) (uint64, error) {
	return tv.totalPower, nil
}

func (tv *FailingMinerTestPowerTableView) Miner(ctx context.Context, st state.Tree, bstore blockstore.Blockstore, mAddr address.Address) (uint64, error) {
	return tv.minerPower, errors.New("something went wrong with the miner power")
}

func (tv *FailingMinerTestPowerTableView) HasPower(ctx context.Context, st state.Tree, bstore blockstore.Blockstore, mAddr address.Address) bool {
	return true
}
