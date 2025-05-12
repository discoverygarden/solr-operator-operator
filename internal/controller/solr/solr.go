package solr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/apache/solr-operator/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	authentication_endpoint string = "api/cluster/security/authentication"
	authorization_endpoint  string = "solr/admin/authorization"
	default_content_type    string = "application/json"
)

type ClientInterface interface {
	CreateUser(name string, password string) error
	UpdateUser(name string, password string) error
	CheckUserExistence(username string) (bool, error)
	CheckUser(username string, password string) (bool, error)
	DeleteUser(name string) error
	GetRoles(name string) ([]string, error)
	HasRoles(name string) (bool, error)
	UpsertRoles(name string) error
	DeleteRoles(name string) error
	GetSolrCloud() *v1beta1.SolrCloud
}

type Client struct {
	ClientInterface
	Context   context.Context
	User      string
	Password  string
	Endpoint  string
	SolrCloud *v1beta1.SolrCloud
}

func (c *Client) GetSolrCloud() *v1beta1.SolrCloud {
	return c.SolrCloud
}

func (c *Client) baseUrl() (*url.URL, error) {
	URL, err := url.ParseRequestURI(c.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to parse URL: %w", err)
	}

	return URL, nil
}

func (c *Client) newRequest(method string, path string, body []byte) (*http.Request, error) {
	reqURL, err := c.baseUrl()
	if err != nil {
		return nil, fmt.Errorf("failed to acquire base URL: %w", err)
	}
	reqURL = reqURL.JoinPath(path)

	req, err := http.NewRequest(method, reqURL.String(), bytes.NewBuffer(body))
	if err != nil {
		return nil, fmt.Errorf("failed to build low-level request: %w", err)
	}
	req.SetBasicAuth(c.User, c.Password)
	req.Header.Add("Content-Type", default_content_type)

	return req, nil
}

func (c *Client) doPostRequest(endpoint string, message []byte) error {
	req, err := c.newRequest(
		"POST",
		endpoint,
		message,
	)
	if err != nil {
		return fmt.Errorf("failed to build post request: %w", err)
	}

	httpClient := &http.Client{}
	_, err = httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("http request failed: %w", err)
	}
	return nil
}

func (c *Client) doAuthenticationPost(message []byte) error {
	return c.doPostRequest(authentication_endpoint, message)
}

func (c *Client) doAuthorizationPost(message []byte) error {
	return c.doPostRequest(authorization_endpoint, message)
}

func (c *Client) CreateUser(name string, password string) error {
	message, err := json.Marshal(setUsersMessage{
		Users: map[string]string{
			name: password,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to generate json body for user creation message: %w", err)
	}
	return c.doAuthenticationPost(message)
}

func (c *Client) UpdateUser(name string, password string) error {
	// Effectively an alias for CreateUser.
	return c.CreateUser(name, password)
}

func (c *Client) getCredentialsFromSolr(username string) (*credInfo, bool, error) {
	creds, err := c.getAllCredentials()
	if err != nil {
		return nil, false, fmt.Errorf("failed to scrape solr creds for %s: %w", username, err)
	}
	info, ok := creds[username]
	return &info, ok, nil
}

func (c *Client) CheckUserExistence(username string) (bool, error) {
	_, ok, err := c.getCredentialsFromSolr(username)
	return ok, err
}

func (c *Client) CheckUser(username string, password string) (bool, error) {
	combinedCred, ok, err := c.getCredentialsFromSolr(username)
	if err != nil {
		return false, fmt.Errorf("failed to get credentials to check user: %w", err)
	} else if !ok {
		return false, nil
	} else {
		return combinedCred.checkPassword(password)
	}
}

func (c *Client) deferredClose(to_close io.Closer) {
	log.FromContext(c.Context)
	err := to_close.Close()
	if err != nil {
		log.Log.Error(err, "Error closing.")
	}
}

func (c *Client) doGetRequest(endpoint string) ([]byte, error) {
	req, err := c.newRequest(
		"GET",
		endpoint,
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to build get request: %w", err)
	}

	httpClient := &http.Client{}
	res, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to perform get request: %w", err)
	}

	defer c.deferredClose(res.Body)

	if res.StatusCode != http.StatusOK {
		bodyBytes, err := io.ReadAll(res.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to read (not-ok) response body: %w", err)
		}
		return nil, fmt.Errorf("get request returned not-ok: %s", bodyBytes)
	}

	return io.ReadAll(res.Body)
}

func (c *Client) getAllRoles() (map[string][]string, error) {
	response_body, err := c.doGetRequest(authorization_endpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	auth := &getAuthResp{}
	err = json.Unmarshal(response_body, auth)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal json response: %w", err)
	}
	return auth.Authorization.UserRoles, nil
}

func (c *Client) getAllCredentials() (map[string]credInfo, error) {
	response_body, err := c.doGetRequest(authentication_endpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	auth := &getUsersResp{}
	err = json.Unmarshal(response_body, auth)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal json response: %w", err)
	}
	return auth.Authentication.Credentials, nil
}

func (c *Client) DeleteUser(name string) error {
	message, err := json.Marshal(deleteUsersMessage{
		Users: []string{name},
	})
	if err != nil {
		return fmt.Errorf("failed to generate json body for user deletion message: %w", err)
	}
	return c.doAuthenticationPost(message)
}

type set map[string]struct{}

func (set set) add(value string) {
	set[value] = struct{}{}
}

func (set set) addAll(values []string) {
	for _, value := range values {
		set.add(value)
	}
}

func (set set) keySet() []string {
	_keyset := []string{}

	for key := range set {
		_keyset = append(_keyset, key)
	}

	return _keyset
}

func setify(values []string) map[string]struct{} {
	_map := make(set)
	_map.addAll(values)
	return _map
}

var DefaultRoles = []string{"admin", "k8s"}

func (c *Client) GetRoles(name string) ([]string, error) {
	all_assignments, err := c.getAllRoles()
	if err != nil {
		return nil, fmt.Errorf("failed to get roles: %w", err)
	}
	roles, ok := all_assignments[name]
	if ok {
		return roles, nil
	} else {
		return []string{}, nil
	}
}

func (c *Client) HasRoles(name string) (bool, error) {
	roles, err := c.GetRoles(name)
	if err != nil {
		return false, fmt.Errorf("failed to check roles: %w", err)
	}
	setish := setify(roles)
	for _, value := range DefaultRoles {
		_, ok := setish[value]
		if !ok {
			return false, nil
		}
	}
	return true, nil
}

func (c *Client) UpsertRoles(name string) error {
	roles, err := c.GetRoles(name)
	if err != nil {
		return fmt.Errorf("failed to get roles during upsert: %w", err)
	}
	setish := set{}
	setish.addAll(roles)
	setish.addAll(DefaultRoles)

	message, err := json.Marshal(setUserRoleMessage{
		RoleMap: map[string][]string{
			name: setish.keySet(),
		},
	})
	if err != nil {
		return fmt.Errorf("failed to generate json body for user role update message: %w", err)
	}
	return c.doAuthorizationPost(message)
}

func (c *Client) DeleteRoles(name string) error {
	message, err := json.Marshal(setUserRoleMessage{
		RoleMap: map[string][]string{
			name: nil,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to generate json body for user role delete message: %w", err)
	}
	return c.doAuthorizationPost(message)
}
