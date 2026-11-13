package client

import (
	"context"
	"fmt"
	"net/http"
)

// LabelTag is one colour tag of a service label (API 2.2.0+).
type LabelTag struct {
	Name  string `json:"name"`
	Color string `json:"color"`
}

// ServiceLabel is the customer-defined nickname + tags of a service (GET/PUT cloud/services/{id}/label).
type ServiceLabel struct {
	ServiceID int64      `json:"service_id"`
	Nickname  *string    `json:"nickname"`
	Tags      []LabelTag `json:"tags"`
	UpdatedAt *string    `json:"updated_at"`
}

func (c *Client) ServiceLabel(ctx context.Context, serviceID int64) (*ServiceLabel, error) {
	var l ServiceLabel
	_, err := c.Do(ctx, http.MethodGet, fmt.Sprintf("cloud/services/%d/label", serviceID), nil, nil, "", &l)
	return &l, err
}

// SetServiceLabel replaces nickname and tags (nil nickname / empty tags clear them).
func (c *Client) SetServiceLabel(ctx context.Context, serviceID int64, nickname *string, tags []LabelTag) (*ServiceLabel, error) {
	if tags == nil {
		tags = []LabelTag{}
	}
	var l ServiceLabel
	_, err := c.Do(ctx, http.MethodPut, fmt.Sprintf("cloud/services/%d/label", serviceID), nil, map[string]any{"nickname": nickname, "tags": tags}, "", &l)
	return &l, err
}
