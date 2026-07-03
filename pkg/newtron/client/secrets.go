package client

import "net/url"

// ListSecrets returns the key names in the network's secret store — never the
// values (the read is keys-only by design). See GET /networks/{netID}/secrets.
func (c *Client) ListSecrets() ([]string, error) {
	var resp struct {
		Keys []string `json:"keys"`
	}
	if err := c.doGet(c.networkPath()+"/secrets", &resp); err != nil {
		return nil, err
	}
	return resp.Keys, nil
}

// SetSecret writes key → value into the network's secret store — the backing
// value a spec field references via ${secret:KEY}. Write-only: there is no
// read-back through the API. See POST /networks/{netID}/secrets.
func (c *Client) SetSecret(key, value string) error {
	body := map[string]string{"key": key, "value": value}
	return c.doPost(c.networkPath()+"/secrets", body, nil)
}

// DeleteSecret removes key from the network's secret store.
// See DELETE /networks/{netID}/secrets/{key}.
func (c *Client) DeleteSecret(key string) error {
	_, err := c.RawRequest("DELETE", c.networkPath()+"/secrets/"+url.PathEscape(key), nil)
	return err
}
