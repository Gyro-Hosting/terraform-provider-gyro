package client

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

// Template is a custom template captured from one of the caller's tr-ist-1 servers (Gyro Public API 2.15.0).
// It installs new servers (image "custom:<id>") or reinstalls existing ones; no image upload exists.
type Template struct {
	ID             int64    `json:"id"`
	Image          string   `json:"image"`
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	Status         string   `json:"status"` // creating | ready | error | deleting | suspended
	SizeGB         float64  `json:"size_gb"`
	SizeBytes      int64    `json:"size_bytes"`
	MinDiskGB      int64    `json:"min_disk_gb"`
	BaseImage      string   `json:"base_image"`
	BaseImageName  string   `json:"base_image_name"`
	SourceServerID *int64   `json:"source_server_id"`
	Region         string   `json:"region"`
	Mode           string   `json:"mode"`
	Warnings       []string `json:"warnings"`
	CreatedAt      *string  `json:"created_at"`
	ReadyAt        *string  `json:"ready_at"`
}

// TemplateImageID returns the template id of an image slug "custom:<id>" (0 when the image is a catalogue image).
func TemplateImageID(image string) int64 {
	if !strings.HasPrefix(image, "custom:") {
		return 0
	}
	var id int64
	if _, err := fmt.Sscan(strings.TrimPrefix(image, "custom:"), &id); err != nil || id < 1 {
		return 0
	}
	return id
}

func (c *Client) Templates(ctx context.Context) ([]Template, error) {
	return listAll[Template](ctx, c, "cloud/templates", nil)
}

func (c *Client) Template(ctx context.Context, id int64) (*Template, error) {
	var t Template
	_, err := c.Do(ctx, http.MethodGet, fmt.Sprintf("cloud/templates/%d", id), nil, nil, "", &t)
	return &t, err
}

// CreateTemplate captures server serverID; mode is "snapshot" (server keeps running) or "shutdown".
func (c *Client) CreateTemplate(ctx context.Context, serverID int64, name, description, mode string) (*Template, error) {
	var r struct {
		Template *Template `json:"template"`
	}
	body := map[string]any{"name": name, "description": description}
	if mode != "" {
		body["mode"] = mode
	}
	if _, err := c.Do(ctx, http.MethodPost, fmt.Sprintf("cloud/servers/%d/templates", serverID), nil, body, "", &r); err != nil {
		return nil, err
	}
	if r.Template == nil || r.Template.ID == 0 {
		return nil, fmt.Errorf("the API did not return a template id")
	}
	return r.Template, nil
}

func (c *Client) UpdateTemplate(ctx context.Context, id int64, name, description string) (*Template, error) {
	var t Template
	_, err := c.Do(ctx, http.MethodPatch, fmt.Sprintf("cloud/templates/%d", id), nil, map[string]any{"name": name, "description": description}, "", &t)
	return &t, err
}

func (c *Client) DeleteTemplate(ctx context.Context, id int64) error {
	_, err := c.Do(ctx, http.MethodDelete, fmt.Sprintf("cloud/templates/%d", id), nil, nil, "", nil)
	return err
}

// CreateServerFromTemplate orders a tr-ist-1 server installed from template tplID (same money rules as CreateServer).
func (c *Client) CreateServerFromTemplate(ctx context.Context, tplID int64, req CreateServerRequest, idemKey string) (*CreateServerResult, error) {
	var r CreateServerResult
	body := map[string]any{"confirm": req.Confirm}
	if req.ProductID > 0 {
		body["product_id"] = req.ProductID
	}
	if req.Plan != "" {
		body["plan"] = req.Plan
	}
	if req.Hostname != "" {
		body["hostname"] = req.Hostname
	}
	if len(req.SSHKeys) > 0 {
		body["ssh_keys"] = req.SSHKeys
	}
	if req.UserData != "" {
		body["user_data"] = req.UserData
	}
	if req.DryRun {
		body["dry_run"] = true
		idemKey = ""
	}
	_, err := c.Do(ctx, http.MethodPost, fmt.Sprintf("cloud/templates/%d/servers", tplID), nil, body, idemKey, &r)
	return &r, err
}
