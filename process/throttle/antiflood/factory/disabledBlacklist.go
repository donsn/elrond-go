package factory

// disabledBlacklistHandler is a mock implementation of BlacklistHandler that does not manage black listed keys
// (all keys [peers] are whitelisted)
type disabledBlacklistHandler struct {
}

// Add returns nil
func (nbh *disabledBlacklistHandler) Add(_ string) error {
	panic("implement me")
}

// Sweep does nothing
func (nbh *disabledBlacklistHandler) Sweep() {
	panic("implement me")
}

// Has outputs false (all peers are white listed)
func (nbh *disabledBlacklistHandler) Has(_ string) bool {
	return false
}

// IsInterfaceNil returns true if there is no value under the interface
func (nbh *disabledBlacklistHandler) IsInterfaceNil() bool {
	return nbh == nil
}
