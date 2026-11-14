package copilot

// EndpointsForTest exposes the wired endpoint URLs (tests only).
func (c *Client) EndpointsForTest() (tokenURL, exURL string) {
	return c.tokenURL, c.exURL
}
