package client

import (
	"net/url"

	"github.com/aldrin-isaac/newtron/pkg/newtron"
)

// scopeQuery renders scope/scope_instance as a "?..." query string, or "" when
// both are empty (the server defaults an absent scope to network).
func scopeQuery(scope, instance string) string {
	if scope == "" && instance == "" {
		return ""
	}
	q := url.Values{}
	if scope != "" {
		q.Set("scope", scope)
	}
	if instance != "" {
		q.Set("scope_instance", instance)
	}
	return "?" + q.Encode()
}

// ShowSSHCredentials reads the device SSH login authored at one scope, with
// ssh_pass masked (a ${secret:} reference kept, plaintext → "***redacted***").
// See GET /networks/{netID}/ssh-credentials.
func (c *Client) ShowSSHCredentials(scope, scopeInstance string) (*newtron.SSHCredentialsView, error) {
	var v newtron.SSHCredentialsView
	if err := c.doGet(c.networkPath()+"/ssh-credentials"+scopeQuery(scope, scopeInstance), &v); err != nil {
		return nil, err
	}
	return &v, nil
}

// SetSSHCredentials sets the device SSH login at a scope. Either field may be
// empty (inherit from the next scope up); ssh_pass may be a ${secret:KEY}
// reference. See POST /networks/{netID}/set-ssh-credentials.
func (c *Client) SetSSHCredentials(scope, scopeInstance, sshUser, sshPass string) error {
	body := map[string]string{
		"scope":          scope,
		"scope_instance": scopeInstance,
		"ssh_user":       sshUser,
		"ssh_pass":       sshPass,
	}
	return c.doPost(c.networkPath()+"/set-ssh-credentials", body, nil)
}

// ClearSSHCredentials removes the device SSH login override at a scope.
// See POST /networks/{netID}/clear-ssh-credentials.
func (c *Client) ClearSSHCredentials(scope, scopeInstance string) error {
	body := map[string]string{"scope": scope, "scope_instance": scopeInstance}
	return c.doPost(c.networkPath()+"/clear-ssh-credentials", body, nil)
}
