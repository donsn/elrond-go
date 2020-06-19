package mock

import (
	"github.com/ElrondNetwork/elrond-go/process"
)

// TxValidatorStub -
type TxValidatorStub struct {
	CheckTxValidityCalled func(txValidatorHandler process.TxValidatorHandler) process.ValidityCheckResult
	RejectedTxsCalled     func() uint64
}

// CheckTxValidity -
func (t *TxValidatorStub) CheckTxValidity(txValidatorHandler process.TxValidatorHandler) process.ValidityCheckResult {
	return t.CheckTxValidityCalled(txValidatorHandler)
}

// IsInterfaceNil returns true if there is no value under the interface
func (t *TxValidatorStub) IsInterfaceNil() bool {
	return t == nil
}
