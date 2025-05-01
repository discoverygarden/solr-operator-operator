package solr

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const (
	authenication_endpoint string = "api/cluster/security/authentication"
	authorization_endpoint string = "solr/admin/authorization"
	default_content_type   string = "application/json"
)

type responseHeader struct {
	Status int `json:"status"`
	QTime  int `json:"QTime"`
}

type authentication struct {
	BlockUnknown       bool                `json:"blockUnknown"`
	Class              string              `json:"class"`
	Credentials        map[string]CredInfo `json:"credentials"`
	Realm              string              `json:"realm"`
	ForwardCredentials bool                `json:"forwardCredentials"`
}

type getUsersResp struct {
	ResponseHeader responseHeader `json:"responseHeader"`
	Enabled        bool           `json:"authentication.enabled"`
	Authentication authentication `json:"authentication"`
}

type authorization struct {
	Class     string              `json:"class"`
	UserRoles map[string][]string `json:"user-role"`
	// Permissions []permission `json:"permissions"`
}

type getAuthResp struct {
	ResponseHeader responseHeader `json:"responseHeader"`
	Enabled        bool           `json:"authorization.enabled"`
	Authorization  authorization  `json:"authorization"`
}

type CredInfo struct {
	RawCreds string
	RawHash  string
	RawSalt  string
	Hash     []byte
	Salt     []byte
}

func (ci *CredInfo) UnmarshalJSON(data []byte) error {
	return json.Unmarshal(data, &ci.RawCreds)
}

func (ci *CredInfo) ensureSplit() error {
	if ci.Salt != nil {
		// Salt is the last thing assigned. If already set, no need to reparse things.
		return nil
	}

	parts := strings.Split(ci.RawCreds, " ")
	part_count := len(parts)
	if part_count != 2 {
		return fmt.Errorf("cred string contained incorrect number of parts; got %d but expected %d", part_count, 2)
	}
	ci.RawHash = parts[0]
	ci.RawSalt = parts[1]

	hash, err := base64.StdEncoding.DecodeString(ci.RawHash)
	if err != nil {
		return fmt.Errorf("failed to base64 decode hash: %w", err)
	} else {
		ci.Hash = hash
	}

	salt, err := base64.StdEncoding.DecodeString(ci.RawSalt)
	if err != nil {
		return fmt.Errorf("failed to base64 decode salt: %w", err)
	} else {
		ci.Salt = salt
	}

	return nil
}

func (ci *CredInfo) checkPassword(candidate string) (bool, error) {
	if err := ci.ensureSplit(); err != nil {
		return false, fmt.Errorf("failed to parse creds: %w", err)
	}

	combinedBytes := append(ci.Salt, []byte(candidate)...)
	round1 := sha256.Sum256(combinedBytes)
	hash := sha256.Sum256(round1[:])

	return bytes.Equal(ci.Hash, hash[:]), nil
}

type Client struct {
	User     string
	Password string
	Endpoint string
}

func (c *Client) baseUrl() (*url.URL, error) {
	URL, err := url.ParseRequestURI(c.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to parse URL: %w", err)
	}

	return URL, nil
}

func (c *Client) newRequest(method string, path string, body []byte, content_type string) (*http.Request, error) {
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
	req.Header.Add("Content-Type", content_type)

	return req, nil
}

type SetUsersMessage struct {
	Users map[string]string `json:"set-user"`
}
type DeleteUsersMessage struct {
	Users []string `json:"delete-user"`
}

func (c *Client) DoAuthenticationPost(message []byte) error {
	req, err := c.newRequest(
		"POST",
		authenication_endpoint,
		message,
		default_content_type,
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

func (c *Client) DoAuthorizationPost(message []byte) error {
	req, err := c.newRequest(
		"POST",
		authorization_endpoint,
		message,
		default_content_type,
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

func (c *Client) CreateUser(name string, password string) error {
	message, err := json.Marshal(SetUsersMessage{
		Users: map[string]string{
			name: password,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to generate json body for user creation message: %w", err)
	}
	return c.DoAuthenticationPost(message)
}

func (c *Client) UpdateUser(name string, password string) error {
	// Effectively an alias for CreateUser.
	return c.CreateUser(name, password)
}

func (c *Client) getCredentials(username string) (*CredInfo, bool, error) {
	creds, err := c.getAllCredentials()
	if err != nil {
		return nil, false, fmt.Errorf("failed to scrape solr creds for %s: %w", username, err)
	}
	info, ok := creds[username]
	return &info, ok, nil
}

func (c *Client) CheckUserExistence(username string) (bool, error) {
	_, ok, err := c.getCredentials(username)
	return ok, err
}

func (c *Client) CheckUser(username string, password string) (bool, error) {
	combinedCred, ok, err := c.getCredentials(username)
	if err != nil {
		return false, fmt.Errorf("failed to get credentials to check user: %w", err)
	} else if !ok {
		return false, nil
	} else {
		return combinedCred.checkPassword(password)
	}
}

func (c *Client) getAllRoles() (map[string][]string, error) {
	req, err := c.newRequest(
		"GET",
		authorization_endpoint,
		nil,
		default_content_type,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to build request for roles: %w", err)
	}

	httpClient := &http.Client{}
	res, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to perform request for roles: %w", err)
	}

	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		bodyBytes, err := io.ReadAll(res.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to read (not-ok) response body: %w", err)
		}
		return nil, fmt.Errorf("failed to get users: %s", bodyBytes)
	}

	response_body, err := io.ReadAll(res.Body)
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

func (c *Client) getAllCredentials() (map[string]CredInfo, error) {
	req, err := c.newRequest(
		"GET",
		authenication_endpoint,
		nil,
		default_content_type,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to build request for credentials: %w", err)
	}

	httpClient := &http.Client{}
	res, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to perform request for credentials: %w", err)
	}

	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		bodyBytes, err := io.ReadAll(res.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to read (not-ok) response body: %w", err)
		}
		return nil, fmt.Errorf("failed to get users: %s", bodyBytes)
	}

	response_body, err := io.ReadAll(res.Body)
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
	message, err := json.Marshal(DeleteUsersMessage{
		Users: []string{name},
	})
	if err != nil {
		return fmt.Errorf("failed to generate json body for user deletion message: %w", err)
	}
	return c.DoAuthenticationPost(message)
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

	for key, _ := range set {
		_keyset = append(_keyset, key)
	}

	return _keyset
}

func setify(values []string) map[string]struct{} {
	_map := make(set)
	_map.addAll(values)
	return _map
}

var default_roles = []string{"admin", "k8s"}
var default_roles_map = setify(default_roles)

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
	for _, value := range default_roles {
		_, ok := setish[value]
		if !ok {
			return false, nil
		}
	}
	return true, nil
}

type SetUserRoleMessage struct {
	RoleMap map[string][]string `json:"set-user-role"`
}

func (c *Client) UpsertRoles(name string) error {
	roles, err := c.GetRoles(name)
	if err != nil {
		return fmt.Errorf("failed to get roles during upsert: %w", err)
	}
	setish := set{}
	setish.addAll(roles)
	setish.addAll(default_roles)

	message, err := json.Marshal(SetUserRoleMessage{
		RoleMap: map[string][]string{
			name: setish.keySet(),
		},
	})
	if err != nil {
		return fmt.Errorf("failed to generate json body for user role update message: %w", err)
	}
	return c.DoAuthorizationPost(message)
}

func (c *Client) DeleteRoles(name string) error {
	message, err := json.Marshal(SetUserRoleMessage{
		RoleMap: map[string][]string{
			name: nil,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to generate json body for user role delete message: %w", err)
	}
	return c.DoAuthorizationPost(message)
}
