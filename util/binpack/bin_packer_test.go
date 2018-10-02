package binpack

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNaivePacker(t *testing.T) {
	assert := assert.New(t)

	binner := &testBinner{binSize: 20}
	packer, bin, _ := NewNaivePacker(binner)

	newItem := func(size Space) testItem {
		return testItem{size: size}
	}

	_, err := packer.PackItemIntoBin(context.Background(), newItem(10), bin)
	assert.NoError(err)
	assert.Equal(Space(10), binner.currentBinUsed)
	assert.Equal(0, binner.closeCount)

	_, err = packer.PackItemIntoBin(context.Background(), newItem(8), bin)
	assert.NoError(err)
	assert.Equal(Space(18), binner.currentBinUsed)
	assert.Equal(0, binner.closeCount)

	_, err = packer.PackItemIntoBin(context.Background(), newItem(2), bin)
	assert.NoError(err)
	assert.Equal(Space(0), binner.currentBinUsed)
	assert.Equal(1, binner.closeCount)

	_, err = packer.PackItemIntoBin(context.Background(), newItem(5), bin)
	assert.NoError(err)
	assert.Equal(Space(5), binner.currentBinUsed)
	assert.Equal(1, binner.closeCount)

	_, err = packer.PackItemIntoBin(context.Background(), newItem(25), bin)
	assert.EqualError(err, "item too large for bin")
	assert.Equal(Space(5), binner.currentBinUsed)
	assert.Equal(1, binner.closeCount)
}

// Binner implementation for tests.

type testItem struct {
	size Space
}

type testBinner struct {
	binSize        Space
	currentBinUsed Space
	closeCount     int
}

var _ Binner = &testBinner{}

func (tb *testBinner) GetCurrentBin() Bin {
	return tb.currentBinUsed
}

func (tb *testBinner) AddItem(ctx context.Context, item Item, bin Bin) error {
	tb.currentBinUsed += item.(testItem).size
	return nil
}

func (tb *testBinner) BinSize() Space {
	return tb.binSize
}

func (tb *testBinner) CloseBin(Bin) {
	tb.currentBinUsed = 0
	tb.closeCount++
}

func (tb *testBinner) ItemSize(item Item) Space {
	return item.(testItem).size
}

func (tb *testBinner) NewBin() (Bin, error) {
	return Space(tb.binSize), nil
}

func (tb *testBinner) SpaceAvailable(bin Bin) Space {
	return tb.binSize - tb.currentBinUsed
}
