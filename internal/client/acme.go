package client

import (
	"context"
	"fmt"
	"net/http"
)

// AcmeKey is a Gyro DNS ACME key (scope dns_acme: only _acme-challenge TXT records).
type AcmeKey struct {
	ID          int64    `json:"id"`
	Name        string   `json:"name"`
	Token       string   `json:"token,omitempty"`
	TokenPrefix string   `json:"token_prefix"`
	ServiceID   int64    `json:"service_id"`
	Zones       []string `json:"zones"`
	IPs         []string `json:"ips"`
	Status      string   `json:"status"`
	CreatedAt   string   `json:"created_at"`
	LastUsedAt  *string  `json:"last_used_at"`
}

// AcmeKeys lists the ACME keys of a Gyro DNS service.
func (c *Client) AcmeKeys(ctx context.Context, serviceID int64) ([]AcmeKey, error) {
	var out []AcmeKey
	_, err := c.Do(ctx, http.MethodGet, fmt.Sprintf("cloud/dns/%d/acme-keys", serviceID), nil, nil, "", &out)
	return out, err
}

// CreateAcmeKey creates an ACME key; the token is only in this answer.
func (c *Client) CreateAcmeKey(ctx context.Context, serviceID int64, name string, zones, ips []string) (*AcmeKey, error) {
	body := map[string]any{"name": name}
	if len(zones) > 0 {
		body["zones"] = zones
	}
	if len(ips) > 0 {
		body["ips"] = ips
	}
	var k AcmeKey
	_, err := c.Do(ctx, http.MethodPost, fmt.Sprintf("cloud/dns/%d/acme-keys", serviceID), nil, body, "", &k)
	return &k, err
}

// DeleteAcmeKey revokes an ACME key.
func (c *Client) DeleteAcmeKey(ctx context.Context, serviceID, keyID int64) error {
	_, err := c.Do(ctx, http.MethodDelete, fmt.Sprintf("cloud/dns/%d/acme-keys/%d", serviceID, keyID), nil, nil, "", nil)
	return err
}
