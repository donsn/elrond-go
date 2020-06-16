package factory

import (
	"testing"

	"github.com/ElrondNetwork/elrond-go/core"
	"github.com/ElrondNetwork/elrond-go/core/check"
	"github.com/stretchr/testify/assert"
)

func TestDisabledAntiFlood_ShouldNotPanic(t *testing.T) {
	t.Parallel()

	defer func() {
		r := recover()
		assert.Nil(t, r, "this shouldn't panic")
	}()

	daf := &disabledAntiFlood{}
	assert.False(t, check.IfNil(daf))

	daf.SetMaxMessagesForTopic("test", 10)
	daf.ResetForTopic("test")
	_ = daf.CanProcessMessageOnTopic(core.PeerID(1), "test")
	_ = daf.CanProcessMessage(nil, core.PeerID(2))
}
