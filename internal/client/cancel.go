package client

// Cancel starts shutdown immediately without waiting for the serialized
// connect/disconnect operation. Disconnect still owns and joins cleanup.
func (c *Controller) Cancel() {
	c.mu.Lock()
	cancel := c.cancel
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}
