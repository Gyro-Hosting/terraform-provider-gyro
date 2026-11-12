package client

import (
	"context"
	"fmt"
	"net/http"
)

// Project is a customer project (API 2.18.0). Id 0 is the default project ("Genel"/"General").
type Project struct {
	ID           int64   `json:"id"`
	Name         string  `json:"name"`
	Description  *string `json:"description"`
	Color        string  `json:"color"`
	Icon         string  `json:"icon"`
	IsDefault    bool    `json:"is_default"`
	ServiceCount int64   `json:"service_count"`
	ServiceTotal int64   `json:"service_total"`
	ServiceIDs   []int64 `json:"service_ids"`
}

func (c *Client) Projects(ctx context.Context) ([]Project, error) {
	var out []Project
	_, err := c.Do(ctx, http.MethodGet, "cloud/projects", nil, nil, "", &out)
	return out, err
}

func (c *Client) Project(ctx context.Context, id int64) (*Project, error) {
	var p Project
	_, err := c.Do(ctx, http.MethodGet, fmt.Sprintf("cloud/projects/%d", id), nil, nil, "", &p)
	return &p, err
}

func (c *Client) CreateProject(ctx context.Context, body map[string]any) (*Project, error) {
	var p Project
	_, err := c.Do(ctx, http.MethodPost, "cloud/projects", nil, body, "", &p)
	return &p, err
}

func (c *Client) UpdateProject(ctx context.Context, id int64, body map[string]any) (*Project, error) {
	var p Project
	_, err := c.Do(ctx, http.MethodPatch, fmt.Sprintf("cloud/projects/%d", id), nil, body, "", &p)
	return &p, err
}

func (c *Client) DeleteProject(ctx context.Context, id int64) error {
	_, err := c.Do(ctx, http.MethodDelete, fmt.Sprintf("cloud/projects/%d", id), nil, nil, "", nil)
	return err
}

// ServiceProjectID returns the project id of a service (0 = default project); nil when the API has no projects.
func (c *Client) ServiceProjectID(ctx context.Context, serviceID int64) (*int64, error) {
	var s struct {
		Project *struct {
			ID int64 `json:"id"`
		} `json:"project"`
	}
	if _, err := c.Do(ctx, http.MethodGet, fmt.Sprintf("cloud/services/%d", serviceID), nil, nil, "", &s); err != nil {
		return nil, err
	}
	if s.Project == nil {
		return nil, nil
	}
	id := s.Project.ID
	return &id, nil
}

// SetServiceProject moves one service into a project (0 = default project).
func (c *Client) SetServiceProject(ctx context.Context, serviceID, projectID int64) error {
	_, err := c.Do(ctx, http.MethodPut, fmt.Sprintf("cloud/services/%d/project", serviceID), nil, map[string]any{"project_id": projectID}, "", nil)
	return err
}
