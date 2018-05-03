package miner

import (
	"math/big"

	cbor "gx/ipfs/QmRVSCwQtW1rjHCay9NqKXDwbtKTgDcN4iY7PrpSqfKM5D/go-ipld-cbor"

	"github.com/filecoin-project/go-filecoin/abi"
	"github.com/filecoin-project/go-filecoin/actor"
	"github.com/filecoin-project/go-filecoin/address"
	"github.com/filecoin-project/go-filecoin/exec"
	"github.com/filecoin-project/go-filecoin/types"
	"github.com/filecoin-project/go-filecoin/vm/errors"
)

func init() {
	cbor.RegisterCborType(Storage{})
}

// Actor is the miner actor
type Actor struct{}

// Sector is the on-chain representation of a sector
type Sector struct {
	CommR []byte
	Deals []uint64
}

// Storage is the miner actors storage
type Storage struct {
	Owner types.Address

	// Pledge is amount the space being offered up by this miner
	// TODO: maybe minimum granularity is more than 1 byte?
	PledgeBytes *types.BytesAmount

	// Collateral is the total amount of filecoin being held as collateral for
	// the miners pledge
	Collateral *types.TokenAmount

	Sectors []*Sector

	LockedStorage *types.BytesAmount // LockedStorage is the amount of the miner's storage that is used.
	Power         *types.BytesAmount
}

// NewStorage returns an empty MinerStorage struct
func (ma *Actor) NewStorage() interface{} {
	return &Storage{}
}

var _ exec.ExecutableActor = (*Actor)(nil)

// NewActor returns a new miner actor
func NewActor(owner types.Address, pledge *types.BytesAmount, coll *types.TokenAmount) (*types.Actor, error) {
	st := &Storage{
		Owner:         owner,
		PledgeBytes:   pledge,
		Collateral:    coll,
		LockedStorage: types.NewBytesAmount(0),
	}

	storageBytes, err := actor.MarshalStorage(st)
	if err != nil {
		return nil, err
	}

	return types.NewActorWithMemory(types.MinerActorCodeCid, nil, storageBytes), nil
}

var minerExports = exec.Exports{
	"addAsk": &exec.FunctionSignature{
		Params: []abi.Type{abi.TokenAmount, abi.BytesAmount},
		Return: []abi.Type{abi.Integer},
	},
	"getOwner": &exec.FunctionSignature{
		Params: nil,
		Return: []abi.Type{abi.Address},
	},
	"addDealsToSector": &exec.FunctionSignature{
		Params: []abi.Type{abi.Integer, abi.UintArray},
		Return: []abi.Type{abi.Integer},
	},
	"commitSector": &exec.FunctionSignature{
		Params: []abi.Type{abi.Integer, abi.Bytes, abi.UintArray},
		Return: []abi.Type{abi.Integer},
	},
}

// Exports returns the miner actors exported functions
func (ma *Actor) Exports() exec.Exports {
	return minerExports
}

// ErrCallerUnauthorized signals an unauthorized caller.
var ErrCallerUnauthorized = errors.NewRevertError("not authorized to call the method")

// ErrInsufficientPledge signals insufficient pledge for what you are trying to do.
var ErrInsufficientPledge = errors.NewRevertError("not enough pledged")

// AddAsk adds an ask via this miner to the storage markets orderbook
func (ma *Actor) AddAsk(ctx exec.VMContext, price *types.TokenAmount, size *types.BytesAmount) (*big.Int, uint8,
	error) {
	var mstore Storage
	out, err := actor.WithStorage(ctx, &mstore, func() (interface{}, error) {
		if ctx.Message().From != mstore.Owner {
			// TODO This should probably return a non-zero exit code instead of an error.
			return nil, ErrCallerUnauthorized
		}

		// compute locked storage + new ask
		total := mstore.LockedStorage.Add(size)

		if total.GreaterThan(mstore.PledgeBytes) {
			// TODO This should probably return a non-zero exit code instead of an error.88
			return nil, ErrInsufficientPledge
		}

		mstore.LockedStorage = total

		// TODO: kinda feels weird that I can't get a real type back here
		out, ret, err := ctx.Send(address.StorageMarketAddress, "addAsk", nil, []interface{}{price, size})
		if err != nil {
			return nil, err
		}

		askID, err := abi.Deserialize(out, abi.Integer)
		if err != nil {
			return nil, errors.FaultErrorWrap(err, "error deserializing")
		}

		if ret != 0 {
			// TODO: Log an error maybe? need good ways of signaling *why* failures happened.
			// I guess we do want to revert all state changes in this case.
			// Which is usually signalled through an error. Something smells.
			return nil, errors.NewRevertError("call to StorageMarket.addAsk failed")
		}

		return askID.Val, nil
	})
	if err != nil {
		return nil, 1, err
	}

	askID, ok := out.(*big.Int)
	if !ok {
		return nil, 1, errors.NewRevertErrorf("expected an Integer return value from call, but got %T instead", out)
	}

	return askID, 0, nil
}

// GetOwner returns the miners owner
func (ma *Actor) GetOwner(ctx exec.VMContext) (types.Address, uint8, error) {
	var mstore Storage
	out, err := actor.WithStorage(ctx, &mstore, func() (interface{}, error) {
		return mstore.Owner, nil
	})
	if err != nil {
		return types.Address{}, 1, err
	}

	a, ok := out.(types.Address)
	if !ok {
		return types.Address{}, 1, errors.NewFaultErrorf("expected an Address return value from call, but got %T instead", out)
	}

	return a, 0, nil
}

// AddDealsToSector adds deals to a sector. If the sectorID given is -1, a new
// sector ID is allocated. The sector ID that deals are added to is returned
func (ma *Actor) AddDealsToSector(ctx exec.VMContext, sectorID int64, deals []uint64) (*big.Int, uint8,
	error) {
	var mstore Storage
	out, err := actor.WithStorage(ctx, &mstore, func() (interface{}, error) {
		return mstore.upsertDealsToSector(sectorID, deals)
	})
	if err != nil {
		return nil, 1, err
	}

	secIDout, ok := out.(int64)
	if !ok {
		return nil, 1, errors.NewRevertError("expected an int64")
	}

	return big.NewInt(secIDout), 0, nil
}

func (mstore *Storage) upsertDealsToSector(sectorID int64, deals []uint64) (int64, error) {
	if sectorID == -1 {
		sectorID = int64(len(mstore.Sectors))
		mstore.Sectors = append(mstore.Sectors, new(Sector))
	}
	if sectorID >= int64(len(mstore.Sectors)) {
		return 0, errors.NewRevertError("sectorID out of range")
	}
	sector := mstore.Sectors[sectorID]
	if sector.CommR != nil {
		return 0, errors.NewRevertError("can't add deals to committed sector")
	}

	sector.Deals = append(sector.Deals, deals...)
	return sectorID, nil
}

// CommitSector adds a commitment to the specified sector
// if sectorID is -1, a new sector will be allocated.
// if passing an existing sector ID, any deals given here will be added to the
// deals already added to that sector
func (ma *Actor) CommitSector(ctx exec.VMContext, sectorID int64, commR []byte, deals []uint64) (*big.Int, uint8, error) {
	var mstore Storage
	out, err := actor.WithStorage(ctx, &mstore, func() (interface{}, error) {
		if len(deals) != 0 {
			sid, err := mstore.upsertDealsToSector(sectorID, deals)
			if err != nil {
				return nil, err
			}
			sectorID = sid
		}

		sector := mstore.Sectors[sectorID]
		if sector.CommR != nil {
			return nil, errors.NewRevertError("sector already committed")
		}

		resp, ret, err := ctx.Send(address.StorageMarketAddress, "commitDeals", nil, []interface{}{sector.Deals})
		if err != nil {
			return nil, err
		}
		if ret != 0 {
			return nil, errors.NewRevertError("failed to call commitDeals")
		}

		sector.CommR = commR
		power := types.NewBytesAmountFromBytes(resp)
		mstore.Power = mstore.Power.Add(power)

		return nil, nil
	})
	if err != nil {
		return nil, 1, err
	}

	secIDout, ok := out.(int64)
	if !ok {
		return nil, 1, errors.NewRevertError("expected an int64")
	}

	return big.NewInt(secIDout), 0, nil
}