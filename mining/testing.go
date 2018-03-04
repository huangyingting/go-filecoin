package mining

import (
	"context"
	"sync"

	"github.com/filecoin-project/go-filecoin/types"
	"github.com/stretchr/testify/mock"
)

// MockBlockGenerator is a testify mock for BlockGenerator.
type MockBlockGenerator struct {
	mock.Mock
}

var _ BlockGenerator = &MockBlockGenerator{}

// Generate is a testify mock implementation.
func (bg *MockBlockGenerator) Generate(ctx context.Context, h *types.Block) (b *types.Block, err error) {
	args := bg.Called(ctx, h)
	if args.Get(0) != nil {
		b = args.Get(0).(*types.Block)
	}
	err = args.Error(1)
	return
}

// MockWorker is a mock Worker.
type MockWorker struct {
	mock.Mock
}

// Start is the MockWorker's Start function.
func (w *MockWorker) Start(ctx context.Context) (chan<- *types.Block, <-chan Result, *sync.WaitGroup) {
	args := w.Called(ctx)
	return args.Get(0).(chan<- *types.Block), args.Get(1).(<-chan Result), args.Get(2).(*sync.WaitGroup)
}

const (
	// ChannelClosed is returned by the Receive*Ch helper functions to indicate
	// the cahnnel is closed.
	ChannelClosed = iota
	// ChannelEmpty indicates an empty channel.
	ChannelEmpty
	// ChannelReceivedValue indicates the channel held a value, which has been
	// received.
	ChannelReceivedValue
)

// ReceiveInCh returns the channel status.
func ReceiveInCh(ch <-chan *types.Block) int {
	select {
	case _, ok := <-ch:
		if ok {
			return ChannelReceivedValue
		}
		return ChannelClosed
	default:
		return ChannelEmpty
	}
}

// ReceiveOutCh returns the channel status.
func ReceiveOutCh(ch <-chan Result) int {
	select {
	case _, ok := <-ch:
		if ok {
			return ChannelReceivedValue
		}
		return ChannelClosed
	default:
		return ChannelEmpty
	}
}