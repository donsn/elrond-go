package mock

// BlacklistHandlerStub -
type BlacklistHandlerStub struct {
	HasCalled func(pid string) bool
}

// Has -
func (bhs *BlacklistHandlerStub) Has(pid string) bool {
	return bhs.HasCalled(pid)
}

// IsInterfaceNil -
func (bhs *BlacklistHandlerStub) IsInterfaceNil() bool {
	return bhs == nil
}
