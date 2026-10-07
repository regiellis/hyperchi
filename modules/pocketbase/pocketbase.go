// Package pocketbase provides an HTTP PocketBase client for HyperChi applications.
//
// It supports record CRUD, paginated lists, password authentication for users
// and superusers, and retries for reads. It has no real-time subscriptions or
// file uploads.
//
// Usage:
//
//	import "github.com/regiellis/hyperchi/modules/pocketbase"
//
//	app := hyperchi.New()
//
//	pbModule := pocketbase.New(pocketbase.Config{
//		BaseURL: "http://localhost:8090",
//		AdminEmail: "admin@example.com",
//		AdminPassword: "your-password",
//		AutoAuth: true,
//	})
//
//	if err := pbModule.Init(app); err != nil {
//		log.Fatal(err)
//	}
//
//	app.Get("/api/users", func(c *hyperchi.Context) error {
//		users, err := pbModule.Client().Collection("users").GetFullList()
//		if err != nil {
//			return err
//		}
//		return c.View("users", hyperchi.H{"users": users}) // fields are in .Data
//	})
package pocketbase

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"time"

	"github.com/regiellis/hyperchi/hyperchi"
)

// Module holds the configured Client. Create it with New, then call Init.
type Module struct {
	config Config
	client *Client
}

// Config configures the module. Timeout defaults to 30 seconds.
//
// RetryAttempts is how many more times a GET (GetOne, GetFullList, Filter,
// HealthCheck) is sent after a network error or a 429, 502, 503, or 504
// answer. 0 sends each request once. Writes and authentication are never
// retried: a lost response would make a retry repeat them. The wait before
// each retry starts at RetryDelay (default 500 milliseconds) and doubles.
type Config struct {
	BaseURL       string        `json:"baseUrl"`
	AdminEmail    string        `json:"adminEmail"`
	AdminPassword string        `json:"adminPassword"`
	AutoAuth      bool          `json:"autoAuth"`
	Timeout       time.Duration `json:"timeout"`
	RetryAttempts int           `json:"retryAttempts"`
	RetryDelay    time.Duration `json:"retryDelay"`
}

// Client calls the PocketBase HTTP API. A superuser token, when set, takes
// precedence over a user token. Client is not safe for concurrent use while
// it authenticates or changes tokens.
type Client struct {
	baseURL       string
	token         string
	adminToken    string
	httpClient    *http.Client
	config        Config
	authenticated bool
}

// Record is one PocketBase record.
type Record struct {
	ID             string         `json:"id"`
	CollectionID   string         `json:"collectionId"`
	CollectionName string         `json:"collectionName"`
	Created        string         `json:"created"`
	Updated        string         `json:"updated"`
	Data           map[string]any `json:"-"` // the collection's own fields, encoded beside the system fields
}

// MarshalJSON encodes the record the way PocketBase sends it: the system
// fields and the Data fields in one flat object. A Data key that repeats a
// system field name is overwritten by the system field.
func (r Record) MarshalJSON() ([]byte, error) {
	fields := make(map[string]any, len(r.Data)+5)
	for key, value := range r.Data {
		fields[key] = value
	}
	fields["id"] = r.ID
	fields["collectionId"] = r.CollectionID
	fields["collectionName"] = r.CollectionName
	fields["created"] = r.Created
	fields["updated"] = r.Updated
	return json.Marshal(fields)
}

// UnmarshalJSON fills the system fields and puts every other field in Data.
func (r *Record) UnmarshalJSON(data []byte) error {
	type metadata Record
	var decoded metadata
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for _, key := range []string{"id", "collectionId", "collectionName", "created", "updated"} {
		delete(fields, key)
	}
	decoded.Data = fields
	*r = Record(decoded)
	return nil
}

// Collection reads and writes the records of one collection.
type Collection struct {
	client *Client
	name   string
}

// AuthResponse is the body of a successful auth-with-password call.
type AuthResponse struct {
	Token  string         `json:"token"`
	Record map[string]any `json:"record"`
}

// ListResponse is one page of a record list.
type ListResponse struct {
	Page       int      `json:"page"`
	PerPage    int      `json:"perPage"`
	TotalItems int      `json:"totalItems"`
	TotalPages int      `json:"totalPages"`
	Items      []Record `json:"items"`
}

// New returns a Module whose client talks to config.BaseURL. It makes no
// requests; Init does.
func New(config Config) *Module {
	if config.Timeout == 0 {
		config.Timeout = 30 * time.Second
	}
	if config.RetryDelay == 0 {
		config.RetryDelay = 500 * time.Millisecond
	}

	client := &Client{
		baseURL: config.BaseURL,
		httpClient: &http.Client{
			Timeout: config.Timeout,
		},
		config: config,
	}

	return &Module{
		config: config,
		client: client,
	}
}

// Name returns "pocketbase".
func (m *Module) Name() string {
	return "pocketbase"
}

// Client returns the module's HTTP client. The app does not hold it: keep the
// Module, or the Client, where your handlers can reach it.
func (m *Module) Client() *Client {
	return m.client
}

// Init authenticates as a superuser when AutoAuth and both admin credentials
// are set, then fails unless /api/health answers 200. It does not change app;
// the parameter keeps the module's install signature. Reach the client
// through Module.Client.
func (m *Module) Init(_ *hyperchi.HyperChi) error {
	if m.config.AutoAuth && m.config.AdminEmail != "" && m.config.AdminPassword != "" {
		if err := m.client.AdminAuth(m.config.AdminEmail, m.config.AdminPassword); err != nil {
			return fmt.Errorf("failed to authenticate with PocketBase: %w", err)
		}
		log.Printf("PocketBase module: authenticated as admin")
	}

	if err := m.client.HealthCheck(); err != nil {
		return fmt.Errorf("PocketBase health check failed: %w", err)
	}

	log.Printf("PocketBase module initialized: %s", m.config.BaseURL)
	return nil
}

// closeResponseBody reports cleanup failures without replacing the request result.
func closeResponseBody(body io.Closer) {
	if err := body.Close(); err != nil {
		log.Printf("PocketBase: closing response body: %v", err)
	}
}

// HealthCheck returns nil when GET /api/health answers 200.
func (c *Client) HealthCheck() error {
	resp, err := c.makeRequest(http.MethodGet, "/api/health", nil)
	if err != nil {
		return fmt.Errorf("PocketBase not accessible: %w", err)
	}
	defer closeResponseBody(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("PocketBase health check failed with status: %d", resp.StatusCode)
	}

	return nil
}

// AdminAuth authenticates a superuser through the PocketBase v0.23+
// _superusers collection and keeps the token for later requests. The name
// predates the superuser rename.
func (c *Client) AdminAuth(email, password string) error {
	authData := map[string]string{
		"identity": email,
		"password": password,
	}

	jsonData, err := json.Marshal(authData)
	if err != nil {
		return err
	}

	resp, err := c.httpClient.Post(
		c.baseURL+"/api/collections/_superusers/auth-with-password",
		"application/json",
		bytes.NewBuffer(jsonData),
	)
	if err != nil {
		return fmt.Errorf("admin auth request failed: %w", err)
	}
	defer closeResponseBody(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return statusError("admin auth failed", resp)
	}

	var authResp AuthResponse
	if err := json.NewDecoder(resp.Body).Decode(&authResp); err != nil {
		return fmt.Errorf("failed to decode auth response: %w", err)
	}

	c.adminToken = authResp.Token
	c.authenticated = true
	log.Printf("PocketBase admin authenticated successfully")
	return nil
}

// UserAuth authenticates a record of an auth collection and keeps its token.
// The token is used only while no superuser token is set.
func (c *Client) UserAuth(collection, identity, password string) (*AuthResponse, error) {
	authData := map[string]string{
		"identity": identity,
		"password": password,
	}

	jsonData, err := json.Marshal(authData)
	if err != nil {
		return nil, err
	}

	name, err := pathSegment("collection", collection)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Post(
		c.baseURL+"/api/collections/"+name+"/auth-with-password",
		"application/json",
		bytes.NewBuffer(jsonData),
	)
	if err != nil {
		return nil, fmt.Errorf("user auth request failed: %w", err)
	}
	defer closeResponseBody(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return nil, statusError("user auth failed", resp)
	}

	var authResp AuthResponse
	if err := json.NewDecoder(resp.Body).Decode(&authResp); err != nil {
		return nil, fmt.Errorf("failed to decode auth response: %w", err)
	}

	c.token = authResp.Token
	return &authResp, nil
}

// Collection returns a handle for the named collection. It makes no request.
func (c *Client) Collection(name string) *Collection {
	return &Collection{
		client: c,
		name:   name,
	}
}

// pathSegment escapes value for use as one URL path segment, so a record ID
// or collection name cannot add segments, a query, or a fragment. An empty
// value, ".", and ".." are rejected: escaping leaves them as they are, and a
// server or proxy may resolve the dot segments.
func pathSegment(what, value string) (string, error) {
	switch value {
	case "", ".", "..":
		return "", fmt.Errorf("invalid %s %q", what, value)
	}
	return url.PathEscape(value), nil
}

// recordsPath returns /api/collections/{collection}/records, with
// /{id} appended when id is given, each segment escaped.
func (col *Collection) recordsPath(id ...string) (string, error) {
	name, err := pathSegment("collection", col.name)
	if err != nil {
		return "", err
	}
	path := "/api/collections/" + name + "/records"
	for _, part := range id {
		segment, err := pathSegment("record id", part)
		if err != nil {
			return "", err
		}
		path += "/" + segment
	}
	return path, nil
}

// statusError reports an unexpected status with the response body. A body
// that cannot be read is reported too.
func statusError(action string, resp *http.Response) error {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("%s (status %d), reading the body: %w", action, resp.StatusCode, err)
	}
	return fmt.Errorf("%s (status %d): %s", action, resp.StatusCode, body)
}

// retryableStatus reports whether a GET answered with code may succeed if sent again.
func retryableStatus(code int) bool {
	switch code {
	case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return false
}

// makeRequest sends a request to endpoint with the current token. A JSON
// content type is set only when there is a body. A GET is retried as
// Config.RetryAttempts describes. The last attempt's response or error is
// returned.
func (c *Client) makeRequest(method, endpoint string, body []byte) (*http.Response, error) {
	retries := 0
	if method == http.MethodGet {
		retries = max(c.config.RetryAttempts, 0)
	}
	delay := c.config.RetryDelay
	for attempt := 0; ; attempt++ {
		resp, err := c.send(method, endpoint, body)
		if attempt == retries {
			return resp, err
		}
		if err == nil {
			if !retryableStatus(resp.StatusCode) {
				return resp, nil
			}
			// Drain the body so the connection can be reused.
			_, drainErr := io.Copy(io.Discard, resp.Body)
			if closeErr := errors.Join(drainErr, resp.Body.Close()); closeErr != nil {
				log.Printf("PocketBase: discarding a %d response before retrying: %v", resp.StatusCode, closeErr)
			}
		}
		time.Sleep(delay)
		delay *= 2
	}
}

// send makes one request.
func (c *Client) send(method, endpoint string, body []byte) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, c.baseURL+endpoint, reader)
	if err != nil {
		return nil, err
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	if c.adminToken != "" {
		req.Header.Set("Authorization", c.adminToken)
	} else if c.token != "" {
		req.Header.Set("Authorization", c.token)
	}

	return c.httpClient.Do(req)
}

// GetFullList fetches every page, 500 records at a time. A failed page
// returns an error rather than a partial list.
func (col *Collection) GetFullList() ([]Record, error) {
	records := make([]Record, 0)
	for page := 1; ; page++ {
		result, err := col.getPage(page)
		if err != nil {
			return nil, err
		}
		records = append(records, result.Items...)
		if page >= result.TotalPages {
			return records, nil
		}
	}
}

// getPage fetches one 500-record page.
func (col *Collection) getPage(page int) (*ListResponse, error) {
	path, err := col.recordsPath()
	if err != nil {
		return nil, err
	}
	endpoint := fmt.Sprintf("%s?page=%d&perPage=500", path, page)
	resp, err := col.client.makeRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch records page %d: %w", page, err)
	}
	defer closeResponseBody(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return nil, statusError(fmt.Sprintf("failed to fetch records page %d", page), resp)
	}

	var result ListResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode records page %d: %w", page, err)
	}
	return &result, nil
}

// GetOne fetches the record with id. Any status but 200 is an error.
func (col *Collection) GetOne(id string) (*Record, error) {
	path, err := col.recordsPath(id)
	if err != nil {
		return nil, err
	}
	resp, err := col.client.makeRequest(http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch record: %w", err)
	}
	defer closeResponseBody(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return nil, statusError("failed to fetch record", resp)
	}

	var record Record
	if err := json.NewDecoder(resp.Body).Decode(&record); err != nil {
		return nil, fmt.Errorf("failed to decode record: %w", err)
	}

	return &record, nil
}

// Create posts data as a new record and returns it as stored.
func (col *Collection) Create(data map[string]any) (*Record, error) {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}

	path, err := col.recordsPath()
	if err != nil {
		return nil, err
	}
	resp, err := col.client.makeRequest(http.MethodPost, path, jsonData)
	if err != nil {
		return nil, fmt.Errorf("failed to create record: %w", err)
	}
	defer closeResponseBody(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return nil, statusError("failed to create record", resp)
	}

	var record Record
	if err := json.NewDecoder(resp.Body).Decode(&record); err != nil {
		return nil, fmt.Errorf("failed to decode created record: %w", err)
	}

	return &record, nil
}

// Update patches the record with id and returns it as stored.
func (col *Collection) Update(id string, data map[string]any) (*Record, error) {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}

	path, err := col.recordsPath(id)
	if err != nil {
		return nil, err
	}
	resp, err := col.client.makeRequest(http.MethodPatch, path, jsonData)
	if err != nil {
		return nil, fmt.Errorf("failed to update record: %w", err)
	}
	defer closeResponseBody(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return nil, statusError("failed to update record", resp)
	}

	var record Record
	if err := json.NewDecoder(resp.Body).Decode(&record); err != nil {
		return nil, fmt.Errorf("failed to decode updated record: %w", err)
	}

	return &record, nil
}

// Delete removes the record with id. 200 and 204 both count as success.
func (col *Collection) Delete(id string) error {
	path, err := col.recordsPath(id)
	if err != nil {
		return err
	}
	resp, err := col.client.makeRequest(http.MethodDelete, path, nil)
	if err != nil {
		return fmt.Errorf("failed to delete record: %w", err)
	}
	defer closeResponseBody(resp.Body)

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return statusError("failed to delete record", resp)
	}

	return nil
}

// Filter returns the first page of records matching a PocketBase filter
// expression. Use GetFullList for every record.
func (col *Collection) Filter(filter string) ([]Record, error) {
	path, err := col.recordsPath()
	if err != nil {
		return nil, err
	}
	resp, err := col.client.makeRequest(http.MethodGet, path+"?filter="+url.QueryEscape(filter), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to filter records: %w", err)
	}
	defer closeResponseBody(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return nil, statusError("failed to filter records", resp)
	}

	var listResp ListResponse
	if err := json.NewDecoder(resp.Body).Decode(&listResp); err != nil {
		return nil, fmt.Errorf("failed to decode filter response: %w", err)
	}

	return listResp.Items, nil
}

// Subscribe always returns an error: real-time subscriptions are not
// supported. Poll with GetFullList or Filter instead.
func (col *Collection) Subscribe(callback func(Record)) error {
	return fmt.Errorf("real-time subscriptions not yet implemented in this version - use polling as alternative")
}

// GetBaseURL returns the PocketBase base URL.
func (c *Client) GetBaseURL() string {
	return c.baseURL
}

// IsConnected reports whether HealthCheck succeeds. It makes a request.
func (c *Client) IsConnected() bool {
	return c.HealthCheck() == nil
}

// SetToken sets the user token sent when no superuser token is set.
func (c *Client) SetToken(token string) {
	c.token = token
}
