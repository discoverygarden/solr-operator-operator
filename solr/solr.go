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

type responseHeader struct {
	Status int `json:"status"`
	QTime  int `json:"QTime"`
}

type getUsersResp struct {
	ResponseHeader responseHeader `json:"responseHeader"`
	Authentication authentication `json:"authentication"`
}

type authentication struct {
	Credentials map[string]string `json:"Credentials"`
}

type Client struct {
	User     string
	Password string
	Host     string
	Port     string
}

func (c *Client) basicAuth() string {
	auth := c.User + ":" + c.Password
	return base64.StdEncoding.EncodeToString([]byte(auth))
}

func (c *Client) baseUrl() url.URL {
	return url.URL{
		Scheme: "http",
		Host:   fmt.Sprintf("%s:%s", c.Host, c.Port),
	}
}

func (c *Client) CreateUser(name, password string) error {
	body := []byte(fmt.Sprintf("{\"set-user\": {\"%s\":\"%s\"}}", name, password))
	reqURL := c.baseUrl()
	reqURL.Path = "api/cluster/security/authentication"
	req, err := http.NewRequest("POST", reqURL.String(), bytes.NewBuffer(body))
	if err != nil {
		return err
	}
	req.Header.Add("Authorization", "Basic "+c.basicAuth())
	req.Header.Add("Content-Type", "application/json")

	httpClient := &http.Client{}
	_, err = httpClient.Do(req)
	if err != nil {
		return err
	}
	return nil
}

func (c *Client) CheckUser(username string, password string) (bool, error) {
	creds, err := c.getCredentials()
	if err != nil {
		return false, err
	}
	combinedCred, ok := creds[username]
	if !ok {
		return false, nil
	}
	parts := strings.Split(combinedCred, " ")
	if len(parts) != 2 {
		return false, fmt.Errorf("Failed to split credential, should only contain 2 elements")
	}
	hash := parts[0]
	salt := parts[1]
	salt = strings.TrimRight(salt, "=")
	saltBytes, err := base64.RawStdEncoding.DecodeString(salt)
	if err != nil {
		return false, err
	}

	return hash == genHash(password, saltBytes), nil
}

func (c *Client) getCredentials() (map[string]string, error) {
	reqURL := c.baseUrl()
	reqURL.Path = "api/cluster/security/authentication"

	req, err := http.NewRequest("GET", reqURL.String(), nil)
	if err != nil {
		return map[string]string{}, err
	}
	req.Header.Add("Authorization", "Basic "+c.basicAuth())
	req.Header.Add("Content-Type", "application/json")

	httpClient := &http.Client{}
	res, err := httpClient.Do(req)
	if err != nil {
		return map[string]string{}, err
	}

	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		bodyBytes, err := io.ReadAll(res.Body)
		if err != nil {
			return map[string]string{}, err
		}
		return map[string]string{}, fmt.Errorf("failed to get users: %s", string(bodyBytes))
	}

	auth := &getUsersResp{}
	err = json.NewDecoder(res.Body).Decode(auth)
	if err != nil {
		return map[string]string{}, err
	}
	return auth.Authentication.Credentials, nil
}

func genHash(password string, salt []byte) string {
	passwordBytes := []byte(password)
	combinedBytes := append(salt, passwordBytes...)
	round1 := sha256.Sum256(combinedBytes)
	hashBytes := sha256.Sum256(round1[:])
	return base64.StdEncoding.EncodeToString(hashBytes[:])
}
