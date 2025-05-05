package solr

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

type responseHeader struct {
	Status int `json:"status"`
	QTime  int `json:"QTime"`
}

type authentication struct {
	BlockUnknown       bool                `json:"blockUnknown"`
	Class              string              `json:"class"`
	Credentials        map[string]credInfo `json:"credentials"`
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
}

type getAuthResp struct {
	ResponseHeader responseHeader `json:"responseHeader"`
	Enabled        bool           `json:"authorization.enabled"`
	Authorization  authorization  `json:"authorization"`
}

type credInfo struct {
	RawCreds string
	RawHash  string
	RawSalt  string
	Hash     []byte
	Salt     []byte
}

func (ci *credInfo) UnmarshalJSON(data []byte) error {
	return json.Unmarshal(data, &ci.RawCreds)
}

func (ci *credInfo) ensureSplit() error {
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

func (ci *credInfo) checkPassword(candidate string) (bool, error) {
	if err := ci.ensureSplit(); err != nil {
		return false, fmt.Errorf("failed to parse creds: %w", err)
	}

	combinedBytes := append(ci.Salt, []byte(candidate)...)
	round1 := sha256.Sum256(combinedBytes)
	hash := sha256.Sum256(round1[:])

	return bytes.Equal(ci.Hash, hash[:]), nil
}
