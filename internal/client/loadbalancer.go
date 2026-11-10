package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// LBHealthCheck mirrors the API's health_check object of a load balancer service.
type LBHealthCheck struct {
	Protocol string `json:"protocol"`
	Port     int64  `json:"port"`
	Interval int64  `json:"interval"`
	Timeout  int64  `json:"timeout"`
	Retries  int64  `json:"retries"`
	HTTP     *struct {
		Domain      string   `json:"domain"`
		Path        string   `json:"path"`
		Response    string   `json:"response"`
		StatusCodes []string `json:"status_codes"`
		TLS         bool     `json:"tls"`
	} `json:"http"`
}

type LBService struct {
	Protocol        string        `json:"protocol"`
	ListenPort      int64         `json:"listen_port"`
	DestinationPort int64         `json:"destination_port"`
	ProxyProtocol   bool          `json:"proxyprotocol"`
	HealthCheck     LBHealthCheck `json:"health_check"`
	Sticky          bool          `json:"sticky"`
	CookieName      string        `json:"cookie_name"`
	CookieLifetime  int64         `json:"cookie_lifetime"`
	IdleTimeout     int64         `json:"idle_timeout"`
	Certificates    []int64       `json:"certificates"`
	RedirectHTTP    bool          `json:"redirect_http"`
}

type LBTarget struct {
	Type         string `json:"type"` // server | all | ip
	ServiceID    int64  `json:"service_id"`
	IP           string `json:"ip"`
	UsePrivateIP bool   `json:"use_private_ip"`
}

type LBLive struct {
	Name      string      `json:"name"`
	Status    string      `json:"status"`
	Size      *string     `json:"size"`
	Region    string      `json:"region"`
	Location  string      `json:"location"`
	IPv4      *string     `json:"ipv4"`
	IPv6      *string     `json:"ipv6"`
	Algorithm string      `json:"algorithm"`
	Services  []LBService `json:"services"`
	Targets   []LBTarget  `json:"targets"`
}

type LoadBalancer struct {
	ID           int64   `json:"id"`
	Name         string  `json:"name"`
	Status       string  `json:"status"`
	ProductID    int64   `json:"product_id"`
	Cycle        string  `json:"cycle"`
	Price        Money   `json:"price"`
	DueDate      *string `json:"due_date"`
	Cancellation *string `json:"cancellation"`
	Live         *LBLive `json:"load_balancer"`
}

type CreateLoadBalancerRequest struct {
	Size      string `json:"size,omitempty"`
	ProductID int64  `json:"product_id,omitempty"`
	Region    string `json:"region,omitempty"`
	Cycle     string `json:"cycle,omitempty"`
	Confirm   bool   `json:"confirm"`
}

type CreateLoadBalancerResult struct {
	LoadBalancer *LoadBalancer `json:"load_balancer"`
	Order        any           `json:"order"`
	Payment      *struct {
		Status string `json:"status"`
		Error  string `json:"error"`
	} `json:"payment"`
}

func (c *Client) CreateLoadBalancer(ctx context.Context, req CreateLoadBalancerRequest, idemKey string) (*CreateLoadBalancerResult, error) {
	var out CreateLoadBalancerResult
	_, err := c.Do(ctx, http.MethodPost, "cloud/load-balancers", nil, req, idemKey, &out)
	return &out, err
}

func (c *Client) LoadBalancer(ctx context.Context, id int64) (*LoadBalancer, error) {
	var out LoadBalancer
	_, err := c.Do(ctx, http.MethodGet, fmt.Sprintf("cloud/load-balancers/%d", id), nil, nil, "", &out)
	return &out, err
}

func (c *Client) DeleteLoadBalancer(ctx context.Context, id int64, immediate bool) error {
	q := url.Values{"confirm": {"true"}}
	if immediate {
		q.Set("immediate", "true")
	}
	_, err := c.Do(ctx, http.MethodDelete, fmt.Sprintf("cloud/load-balancers/%d", id), q, map[string]any{"confirm": true, "immediate": immediate}, "", nil)
	return err
}

// LBWrite calls one of the load balancer sub-resources (services, targets, algorithm, ...).
func (c *Client) LBWrite(ctx context.Context, method string, id int64, sub string, body map[string]any) error {
	_, err := c.Do(ctx, method, fmt.Sprintf("cloud/load-balancers/%d/%s", id, sub), nil, body, "", nil)
	return err
}
