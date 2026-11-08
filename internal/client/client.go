// Package client is a small, dependency-free Go client for the Gyro Public API v1
// (https://www.gyrohosting.com/api/v1/client). It is shared by the Terraform provider and gyroctl.
package client

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const DefaultEndpoint = "https://www.gyrohosting.com/api/v1/client"

// Client talks to the API. Zero values are not usable; use New.
type Client struct {
	Endpoint   string
	Token      string
	UserAgent  string
	HTTP       *http.Client
	MaxRetries int
	// Sleep is replaceable in tests.
	Sleep func(time.Duration)
}

func New(endpoint, token, userAgent string) *Client {
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	return &Client{
		Endpoint:   strings.TrimRight(endpoint, "/"),
		Token:      token,
		UserAgent:  userAgent,
		HTTP:       &http.Client{Timeout: 90 * time.Second},
		MaxRetries: 4,
		Sleep:      time.Sleep,
	}
}

// APIError is the decoded {"error": {...}} body.
type APIError struct {
	Status  int            `json:"-"`
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

func (e *APIError) Error() string {
	return fmt.Sprintf("gyro api: %d %s: %s", e.Status, e.Code, e.Message)
}

// IsNotFound reports a 404 (also returned for objects owned by someone else).
func IsNotFound(err error) bool {
	var e *APIError
	return errors.As(err, &e) && e.Status == http.StatusNotFound
}

// HasCode reports an API error with the given code.
func HasCode(err error, code string) bool {
	var e *APIError
	return errors.As(err, &e) && e.Code == code
}

type envelope struct {
	Data  json.RawMessage `json:"data"`
	Meta  *PageMeta       `json:"meta,omitempty"`
	Error *APIError       `json:"error,omitempty"`
}

type PageMeta struct {
	Total    int `json:"total"`
	Page     int `json:"page"`
	Limit    int `json:"limit"`
	NextPage int `json:"next_page"`
}

// NewIdempotencyKey returns a random UUIDv4 string.
func NewIdempotencyKey() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// Do performs one API call. GET calls are retried on 429/5xx/network errors; POST/PATCH/PUT/DELETE
// are retried only when idemKey is set (the server then replays instead of repeating the work) or on 429.
func (c *Client) Do(ctx context.Context, method, path string, query url.Values, body any, idemKey string, out any) (*PageMeta, error) {
	var raw []byte
	if body != nil {
		var err error
		if raw, err = json.Marshal(body); err != nil {
			return nil, err
		}
	}
	u := c.Endpoint + "/" + strings.TrimLeft(path, "/")
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	safe := method == http.MethodGet || idemKey != ""
	var lastErr error
	for attempt := 0; attempt <= c.MaxRetries; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, u, bytes.NewReader(raw))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/json")
		if c.Token != "" {
			req.Header.Set("Authorization", "Bearer "+c.Token)
		}
		if c.UserAgent != "" {
			req.Header.Set("User-Agent", c.UserAgent)
		}
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if idemKey != "" {
			req.Header.Set("Idempotency-Key", idemKey)
		}
		resp, err := c.HTTP.Do(req)
		if err != nil {
			lastErr = err
			if !safe || ctx.Err() != nil {
				return nil, err
			}
			c.Sleep(backoff(attempt, 0))
			continue
		}
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		resp.Body.Close()
		var env envelope
		if len(data) > 0 {
			if jerr := json.Unmarshal(data, &env); jerr != nil {
				env.Error = &APIError{Code: "bad_response", Message: strings.TrimSpace(string(data[:min(len(data), 200)]))}
			}
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 && env.Error == nil {
			if out != nil && len(env.Data) > 0 && string(env.Data) != "null" {
				if err := json.Unmarshal(env.Data, out); err != nil {
					return nil, fmt.Errorf("gyro api: decoding %s %s: %w", method, path, err)
				}
			}
			return env.Meta, nil
		}
		apiErr := env.Error
		if apiErr == nil {
			apiErr = &APIError{Code: http.StatusText(resp.StatusCode), Message: "unexpected response"}
		}
		apiErr.Status = resp.StatusCode
		lastErr = apiErr
		retryAfter := 0
		if s := resp.Header.Get("Retry-After"); s != "" {
			retryAfter, _ = strconv.Atoi(s)
		}
		switch {
		case resp.StatusCode == http.StatusTooManyRequests:
		case resp.StatusCode == http.StatusConflict && apiErr.Code == "idempotency_in_flight":
		case resp.StatusCode >= 500 && safe:
		default:
			return nil, apiErr
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		c.Sleep(backoff(attempt, retryAfter))
	}
	return nil, lastErr
}

func backoff(attempt, retryAfter int) time.Duration {
	if retryAfter > 0 {
		if retryAfter > 60 {
			retryAfter = 60
		}
		return time.Duration(retryAfter) * time.Second
	}
	d := time.Duration(1<<attempt) * 500 * time.Millisecond
	if d > 10*time.Second {
		d = 10 * time.Second
	}
	return d
}

/* ------------------------------------------------------------------ models */

type Money struct {
	Amount   float64 `json:"amount"`
	Currency *string `json:"currency"`
}

type Price struct {
	Cycle    string  `json:"cycle"`
	Amount   float64 `json:"amount"`
	Setup    float64 `json:"setup"`
	Currency string  `json:"currency"`
}

type Product struct {
	ID           int64    `json:"id"`
	Name         string   `json:"name"`
	ServerType   string   `json:"server_type"`
	Location     string   `json:"location"`
	Cores        *int64   `json:"cores"`
	MemoryGB     *float64 `json:"memory_gb"`
	DiskGB       *int64   `json:"disk_gb"`
	CPUType      *string  `json:"cpu_type"`
	Architecture *string  `json:"architecture"`
	TrafficTB    *float64 `json:"traffic_tb"`
	IPv4         bool     `json:"ipv4"`
	IPv6         bool     `json:"ipv6"`
	Available    bool     `json:"available"`
	Prices       []Price  `json:"prices"`
}

type Location struct {
	Code     string `json:"code"`
	City     string `json:"city"`
	Country  string `json:"country"`
	Zone     string `json:"zone"`
	Products int64  `json:"products"`
}

type Image struct {
	Slug     string `json:"slug"`
	Name     string `json:"name"`
	OSFlavor string `json:"os_flavor"`
}

type SSHKey struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Fingerprint string `json:"fingerprint"`
	PublicKey   string `json:"public_key"`
	CreatedAt   string `json:"created_at"`
}

type Traffic struct {
	OutgoingBytes int64 `json:"outgoing_bytes"`
	IncludedBytes int64 `json:"included_bytes"`
}

type Live struct {
	PowerState     string  `json:"power_state"`
	Locked         bool    `json:"locked"`
	RescueEnabled  bool    `json:"rescue_enabled"`
	BackupsEnabled bool    `json:"backups_enabled"`
	IPv4           *string `json:"ipv4"`
	IPv6Network    *string `json:"ipv6_network"`
	Cores          int64   `json:"cores"`
	MemoryGB       float64 `json:"memory_gb"`
	DiskGB         int64   `json:"disk_gb"`
	Image          *string `json:"image"`
	ImageName      *string `json:"image_name"`
	Location       *string `json:"location"`
	Traffic        Traffic `json:"traffic"`
}

type Server struct {
	ID           int64   `json:"id"`
	Name         string  `json:"name"`
	Hostname     *string `json:"hostname"`
	Status       string  `json:"status"`
	ProductID    int64   `json:"product_id"`
	ServerType   *string `json:"server_type"`
	Location     *string `json:"location"`
	IPv4         *string `json:"ipv4"`
	IPv6         *string `json:"ipv6"`
	Provisioned  bool    `json:"provisioned"`
	Cycle        string  `json:"cycle"`
	Price        Money   `json:"price"`
	AutoRenew    bool    `json:"auto_renew"`
	CreatedAt    *string `json:"created_at"`
	DueDate      *string `json:"due_date"`
	Cancellation *string `json:"cancellation"`
	Live         *Live   `json:"live,omitempty"`
}

type CreateServerRequest struct {
	ProductID  int64   `json:"product_id,omitempty"`
	ServerType string  `json:"server_type,omitempty"`
	Location   string  `json:"location,omitempty"`
	Cycle      string  `json:"cycle,omitempty"`
	Image      string  `json:"image,omitempty"`
	SSHKeys    []int64 `json:"ssh_keys,omitempty"`
	Hostname   string  `json:"hostname,omitempty"`
	UserData   string  `json:"user_data,omitempty"`
	DryRun     bool    `json:"dry_run,omitempty"`
	Confirm    bool    `json:"confirm,omitempty"`
}

type Quote struct {
	ProductID int64  `json:"product_id"`
	Cycle     string `json:"cycle"`
	Total     *Money `json:"total"`
	Tax       *Money `json:"tax"`
	Balance   *Money `json:"balance"`
}

type CreateServerResult struct {
	DryRun bool    `json:"dry_run"`
	Quote  *Quote  `json:"quote"`
	Server *Server `json:"server"`
	Order  *struct {
		ID     int64  `json:"id"`
		Number string `json:"number"`
		Status string `json:"status"`
		Total  *Money `json:"total"`
	} `json:"order"`
	Payment *struct {
		Method string `json:"method"`
		Status string `json:"status"`
		Error  string `json:"error"`
	} `json:"payment"`
}

type ActionResult struct {
	Action               string `json:"action"`
	Status               string `json:"status"`
	ServerID             int64  `json:"server_id"`
	Message              string `json:"message"`
	RootPassword         string `json:"root_password,omitempty"`
	RootPasswordWithheld bool   `json:"root_password_withheld,omitempty"`
}

type Snapshot struct {
	ID          int64   `json:"id"`
	Description string  `json:"description"`
	Status      string  `json:"status"`
	SizeBytes   int64   `json:"size_bytes"`
	CreatedAt   *string `json:"created_at"`
}

type SnapshotList struct {
	Items   []Snapshot `json:"items"`
	QuotaGB int64      `json:"quota_gb"`
}

type Balance struct {
	Balance    Money `json:"balance"`
	LowBalance bool  `json:"low_balance"`
}

type Invoice struct {
	InvoiceID int64   `json:"invoice_id"`
	Number    string  `json:"number"`
	Status    string  `json:"status"`
	State     string  `json:"state"`
	Total     Money   `json:"total"`
	CreatedAt *string `json:"created_at"`
	DueDate   *string `json:"due_date"`
}

/* ------------------------------------------------------------------ calls */

func listAll[T any](ctx context.Context, c *Client, path string, q url.Values) ([]T, error) {
	if q == nil {
		q = url.Values{}
	}
	q.Set("limit", "100")
	var all []T
	for page := 1; page < 1000; {
		q.Set("page", strconv.Itoa(page))
		var batch []T
		meta, err := c.Do(ctx, http.MethodGet, path, q, nil, "", &batch)
		if err != nil {
			return nil, err
		}
		all = append(all, batch...)
		if meta == nil || meta.NextPage == 0 {
			break
		}
		page = meta.NextPage
	}
	return all, nil
}

func (c *Client) Products(ctx context.Context, location, serverType string) ([]Product, error) {
	q := url.Values{}
	if location != "" {
		q.Set("location", location)
	}
	if serverType != "" {
		q.Set("server_type", serverType)
	}
	return listAll[Product](ctx, c, "cloud/products", q)
}

func (c *Client) Locations(ctx context.Context) ([]Location, error) {
	var out []Location
	_, err := c.Do(ctx, http.MethodGet, "cloud/locations", nil, nil, "", &out)
	return out, err
}

func (c *Client) Images(ctx context.Context) ([]Image, error) {
	var out []Image
	_, err := c.Do(ctx, http.MethodGet, "cloud/images", nil, nil, "", &out)
	return out, err
}

func (c *Client) SSHKeys(ctx context.Context) ([]SSHKey, error) {
	return listAll[SSHKey](ctx, c, "cloud/ssh-keys", nil)
}

func (c *Client) SSHKey(ctx context.Context, id int64) (*SSHKey, error) {
	var k SSHKey
	_, err := c.Do(ctx, http.MethodGet, fmt.Sprintf("cloud/ssh-keys/%d", id), nil, nil, "", &k)
	return &k, err
}

func (c *Client) CreateSSHKey(ctx context.Context, name, publicKey string) (*SSHKey, error) {
	var k SSHKey
	_, err := c.Do(ctx, http.MethodPost, "cloud/ssh-keys", nil, map[string]string{"name": name, "public_key": publicKey}, "", &k)
	return &k, err
}

func (c *Client) DeleteSSHKey(ctx context.Context, id int64) error {
	_, err := c.Do(ctx, http.MethodDelete, fmt.Sprintf("cloud/ssh-keys/%d", id), nil, nil, "", nil)
	return err
}

func (c *Client) Servers(ctx context.Context, status string) ([]Server, error) {
	q := url.Values{}
	if status != "" {
		q.Set("status", status)
	}
	return listAll[Server](ctx, c, "cloud/servers", q)
}

func (c *Client) Server(ctx context.Context, id int64) (*Server, error) {
	var s Server
	_, err := c.Do(ctx, http.MethodGet, fmt.Sprintf("cloud/servers/%d", id), nil, nil, "", &s)
	return &s, err
}

// CreateServer places a paid order (or a dry-run quote). idemKey must be stable across retries of one logical order.
func (c *Client) CreateServer(ctx context.Context, req CreateServerRequest, idemKey string) (*CreateServerResult, error) {
	var r CreateServerResult
	if req.DryRun {
		idemKey = ""
	}
	_, err := c.Do(ctx, http.MethodPost, "cloud/servers", nil, req, idemKey, &r)
	return &r, err
}

func (c *Client) UpdateServer(ctx context.Context, id int64, fields map[string]any) (*Server, error) {
	var s Server
	_, err := c.Do(ctx, http.MethodPatch, fmt.Sprintf("cloud/servers/%d", id), nil, fields, "", &s)
	return &s, err
}

// DeleteServer cancels a server. immediate=true cancels now (billing stops, powered off, destroyed after the
// grace period); false files a cancellation request processed by the panel automation.
func (c *Client) DeleteServer(ctx context.Context, id int64, immediate bool) error {
	q := url.Values{"confirm": {"true"}}
	if immediate {
		q.Set("immediate", "true")
	}
	_, err := c.Do(ctx, http.MethodDelete, fmt.Sprintf("cloud/servers/%d", id), q, map[string]any{"confirm": true, "immediate": immediate}, "", nil)
	return err
}

func (c *Client) Action(ctx context.Context, id int64, action string, body map[string]any) (*ActionResult, error) {
	var r ActionResult
	_, err := c.Do(ctx, http.MethodPost, fmt.Sprintf("cloud/servers/%d/actions/%s", id, url.PathEscape(action)), nil, body, "", &r)
	return &r, err
}

func (c *Client) Snapshots(ctx context.Context, id int64) (*SnapshotList, error) {
	var r SnapshotList
	_, err := c.Do(ctx, http.MethodGet, fmt.Sprintf("cloud/servers/%d/snapshots", id), nil, nil, "", &r)
	return &r, err
}

func (c *Client) CreateSnapshot(ctx context.Context, id int64) error {
	_, err := c.Do(ctx, http.MethodPost, fmt.Sprintf("cloud/servers/%d/snapshots", id), nil, nil, "", nil)
	return err
}

func (c *Client) Balance(ctx context.Context) (*Balance, error) {
	var b Balance
	_, err := c.Do(ctx, http.MethodGet, "balance", nil, nil, "", &b)
	return &b, err
}

func (c *Client) Invoices(ctx context.Context) ([]Invoice, error) {
	return listAll[Invoice](ctx, c, "invoices", nil)
}

// WaitReady polls until the server is active, provisioned, unlocked and (if want != "") in the wanted power state.
func (c *Client) WaitReady(ctx context.Context, id int64, want string, interval time.Duration) (*Server, error) {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	for {
		s, err := c.Server(ctx, id)
		if err != nil && !IsNotFound(err) {
			var e *APIError
			if !errors.As(err, &e) || e.Status < 500 {
				return nil, err
			}
		}
		if err == nil {
			if s.Status == "cancelled" {
				return s, fmt.Errorf("server %d was cancelled while waiting", id)
			}
			if s.Status == "active" && s.Provisioned && s.Live != nil && !s.Live.Locked &&
				(want == "" || s.Live.PowerState == want) && s.Live.PowerState != "starting" && s.Live.PowerState != "stopping" {
				return s, nil
			}
		}
		select {
		case <-ctx.Done():
			return s, fmt.Errorf("timed out waiting for server %d: %w", id, ctx.Err())
		case <-time.After(interval):
		}
	}
}

/* ------------------------------------------------------------------ v2 */

type Service struct {
	ID          int64   `json:"id"`
	Type        string  `json:"type"`
	ProductLine string  `json:"product_line"`
	Module      *string `json:"module"`
	Name        string  `json:"name"`
	Status      string  `json:"status"`
	ProductID   int64   `json:"product_id"`
	Host        *string `json:"host"`
	IP          *string `json:"ip"`
	Cycle       string  `json:"cycle"`
	Price       Money   `json:"price"`
	AutoRenew   bool    `json:"auto_renew"`
	DueDate     *string `json:"due_date"`
	CustomerID  *int64  `json:"customer_id"`
}

type Webhook struct {
	ID          int64    `json:"id"`
	URL         string   `json:"url"`
	Events      []string `json:"events"`
	Description *string  `json:"description"`
	CustomerID  *int64   `json:"customer_id"`
	Status      string   `json:"status"`
	Secret      string   `json:"secret,omitempty"`
}

type EndCustomer struct {
	ID          int64   `json:"id"`
	Name        string  `json:"name"`
	Email       *string `json:"email"`
	ExternalRef *string `json:"external_ref"`
	Note        *string `json:"note"`
	Status      string  `json:"status"`
	AllowOrder  bool    `json:"allow_order"`
	Services    int64   `json:"services"`
}

type APIKey struct {
	ID          int64    `json:"id"`
	Name        string   `json:"name"`
	TokenPrefix string   `json:"token_prefix"`
	CustomerID  *int64   `json:"customer_id"`
	Scopes      []string `json:"scopes"`
	Services    []int64  `json:"services"`
	Actions     []string `json:"actions"`
	IPs         []string `json:"ips"`
	RateLimit   *int64   `json:"rate_limit"`
	Status      string   `json:"status"`
	Token       string   `json:"token,omitempty"`
}

func (c *Client) Services(ctx context.Context, productLine string) ([]Service, error) {
	q := url.Values{}
	if productLine != "" {
		q.Set("product_line", productLine)
	}
	return listAll[Service](ctx, c, "cloud/services", q)
}

func (c *Client) Webhook(ctx context.Context, id int64) (*Webhook, error) {
	var w Webhook
	_, err := c.Do(ctx, http.MethodGet, fmt.Sprintf("cloud/webhooks/%d", id), nil, nil, "", &w)
	return &w, err
}

func (c *Client) CreateWebhook(ctx context.Context, body map[string]any) (*Webhook, error) {
	var w Webhook
	_, err := c.Do(ctx, http.MethodPost, "cloud/webhooks", nil, body, "", &w)
	return &w, err
}

func (c *Client) UpdateWebhook(ctx context.Context, id int64, body map[string]any) (*Webhook, error) {
	var w Webhook
	_, err := c.Do(ctx, http.MethodPatch, fmt.Sprintf("cloud/webhooks/%d", id), nil, body, "", &w)
	return &w, err
}

func (c *Client) DeleteWebhook(ctx context.Context, id int64) error {
	_, err := c.Do(ctx, http.MethodDelete, fmt.Sprintf("cloud/webhooks/%d", id), nil, nil, "", nil)
	return err
}

func (c *Client) Customer(ctx context.Context, id int64) (*EndCustomer, error) {
	var e EndCustomer
	_, err := c.Do(ctx, http.MethodGet, fmt.Sprintf("cloud/reseller/customers/%d", id), nil, nil, "", &e)
	return &e, err
}

func (c *Client) CreateCustomer(ctx context.Context, body map[string]any) (*EndCustomer, error) {
	var e EndCustomer
	_, err := c.Do(ctx, http.MethodPost, "cloud/reseller/customers", nil, body, "", &e)
	return &e, err
}

func (c *Client) UpdateCustomer(ctx context.Context, id int64, body map[string]any) (*EndCustomer, error) {
	var e EndCustomer
	_, err := c.Do(ctx, http.MethodPatch, fmt.Sprintf("cloud/reseller/customers/%d", id), nil, body, "", &e)
	return &e, err
}

func (c *Client) DeleteCustomer(ctx context.Context, id int64) error {
	_, err := c.Do(ctx, http.MethodDelete, fmt.Sprintf("cloud/reseller/customers/%d", id), url.Values{"confirm": {"true"}}, nil, "", nil)
	return err
}

// APIKeys lists sub-account keys of the account (customerID 0) or of an end-customer.
func (c *Client) APIKeys(ctx context.Context, customerID int64) ([]APIKey, error) {
	var out []APIKey
	p := "cloud/keys"
	if customerID > 0 {
		p = fmt.Sprintf("cloud/reseller/customers/%d/keys", customerID)
	}
	_, err := c.Do(ctx, http.MethodGet, p, nil, nil, "", &out)
	return out, err
}

func (c *Client) CreateAPIKey(ctx context.Context, customerID int64, body map[string]any) (*APIKey, error) {
	var k APIKey
	p := "cloud/keys"
	if customerID > 0 {
		p = fmt.Sprintf("cloud/reseller/customers/%d/keys", customerID)
	}
	_, err := c.Do(ctx, http.MethodPost, p, nil, body, "", &k)
	return &k, err
}

func (c *Client) DeleteAPIKey(ctx context.Context, customerID, id int64) error {
	p := fmt.Sprintf("cloud/keys/%d", id)
	if customerID > 0 {
		p = fmt.Sprintf("cloud/reseller/customers/%d/keys/%d", customerID, id)
	}
	_, err := c.Do(ctx, http.MethodDelete, p, nil, nil, "", nil)
	return err
}

/* ------------------------------------------------------------------ v2 typed product lines */

type Mailbox struct {
	Email   string  `json:"email"`
	Name    string  `json:"name"`
	QuotaMB int64   `json:"quota_mb"`
	UsedMB  float64 `json:"used_mb"`
	Active  int64   `json:"active"`
}

type MailAlias struct {
	ID       int64    `json:"id"`
	Address  string   `json:"address"`
	Goto     []string `json:"goto"`
	Active   int64    `json:"active"`
	Kind     string   `json:"kind"`
	KeepCopy bool     `json:"keep_copy"`
}

type Bucket struct {
	Name     string `json:"name"`
	Bytes    int64  `json:"bytes"`
	Objects  int64  `json:"objects"`
	State    string `json:"state"`
	PathURL  string `json:"path_url"`
	VhostURL string `json:"vhost_url"`
}

type StorageKey struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Secret string `json:"secret,omitempty"`
}

type DNSRecord struct {
	Identity any    `json:"identity,omitempty"`
	Type     string `json:"type"`
	Name     string `json:"name"`
	Value    string `json:"value"`
	TTL      any    `json:"ttl,omitempty"`
	Priority any    `json:"priority,omitempty"`
}

type AppInstall struct {
	ID          int64             `json:"id"`
	App         string            `json:"app"`
	Status      string            `json:"status"`
	Progress    int64             `json:"progress"`
	URL         *string           `json:"url"`
	Host        *string           `json:"host"`
	Error       *string           `json:"error"`
	Credentials map[string]string `json:"credentials"`
}

func (c *Client) Mailboxes(ctx context.Context, sid int64) ([]Mailbox, error) {
	var r struct {
		Items []Mailbox `json:"items"`
	}
	_, err := c.Do(ctx, http.MethodGet, fmt.Sprintf("cloud/mail/%d/mailboxes", sid), nil, nil, "", &r)
	return r.Items, err
}

// MailWrite calls a typed mail/storage/monitor write path (POST/PATCH/PUT/DELETE) and returns the raw data object.
func (c *Client) TypedWrite(ctx context.Context, method, path string, body map[string]any) (map[string]any, error) {
	out := map[string]any{}
	_, err := c.Do(ctx, method, path, nil, body, "", &out)
	return out, err
}

func (c *Client) MailAliases(ctx context.Context, sid int64) ([]MailAlias, error) {
	var r struct {
		Items []MailAlias `json:"items"`
	}
	_, err := c.Do(ctx, http.MethodGet, fmt.Sprintf("cloud/mail/%d/aliases", sid), nil, nil, "", &r)
	return r.Items, err
}

func (c *Client) Buckets(ctx context.Context, sid int64) ([]Bucket, error) {
	var r struct {
		Items []Bucket `json:"items"`
	}
	_, err := c.Do(ctx, http.MethodGet, fmt.Sprintf("cloud/storage/%d/buckets", sid), nil, nil, "", &r)
	return r.Items, err
}

func (c *Client) StorageKeys(ctx context.Context, sid int64) ([]StorageKey, error) {
	var r struct {
		Items []StorageKey `json:"items"`
	}
	_, err := c.Do(ctx, http.MethodGet, fmt.Sprintf("cloud/storage/%d/keys", sid), nil, nil, "", &r)
	return r.Items, err
}

func (c *Client) CreateStorageKey(ctx context.Context, sid int64, label string) (*StorageKey, error) {
	var r struct {
		Key StorageKey `json:"key"`
	}
	_, err := c.Do(ctx, http.MethodPost, fmt.Sprintf("cloud/storage/%d/keys", sid), nil, map[string]any{"label": label, "confirm": true}, "", &r)
	return &r.Key, err
}

func (c *Client) DNSRecords(ctx context.Context, domain string) ([]DNSRecord, error) {
	var out []DNSRecord
	_, err := c.Do(ctx, http.MethodGet, "domains/"+url.PathEscape(domain)+"/dns-records", nil, nil, "", &out)
	return out, err
}

func (c *Client) AppStatus(ctx context.Context, serverID int64) (*AppInstall, error) {
	var r struct {
		Install *AppInstall `json:"install"`
	}
	_, err := c.Do(ctx, http.MethodGet, fmt.Sprintf("cloud/servers/%d/app", serverID), nil, nil, "", &r)
	return r.Install, err
}
