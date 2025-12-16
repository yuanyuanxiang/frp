// Copyright 2024 fatedier, fatedier@gmail.com
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package sub

import (
	"crypto/md5"
	"encoding/hex"
	"strconv"
	"time"
)

// CalculatePrivilegeKey calculates the privilegeKey using the same algorithm as FRP.
// This is a helper function that can be used on a trusted server to pre-calculate
// authentication credentials for clients.
//
// Algorithm: MD5(token + timestamp)
//
// Parameters:
//   - token: The original FRP authentication token
//   - timestamp: Unix timestamp (if 0, current time will be used)
//
// Returns:
//   - privilegeKey: The calculated MD5 hash (hex encoded)
//   - timestamp: The timestamp used for calculation
//
// Example:
//
//	privilegeKey, timestamp := CalculatePrivilegeKey("my_secret_token", 0)
//	// Now you can distribute privilegeKey and timestamp to the client
//	// without exposing the original token
func CalculatePrivilegeKey(token string, timestamp int64) (privilegeKey string, actualTimestamp int64) {
	// Use current time if timestamp is not provided
	if timestamp == 0 {
		timestamp = time.Now().Unix()
	}

	// Calculate MD5(token + timestamp)
	md5Ctx := md5.New()
	md5Ctx.Write([]byte(token))
	md5Ctx.Write([]byte(strconv.FormatInt(timestamp, 10)))
	privilegeKey = hex.EncodeToString(md5Ctx.Sum(nil))

	return privilegeKey, timestamp
}

// CalculatePrivilegeKeyWithDuration calculates a privilegeKey with a specific validity duration.
// This is useful when you want to create time-limited credentials.
//
// Parameters:
//   - token: The original FRP authentication token
//   - validDuration: How long the credentials should be valid (e.g., 24*time.Hour)
//
// Returns:
//   - privilegeKey: The calculated MD5 hash
//   - timestamp: The timestamp when credentials will expire
//   - expiresAt: Human-readable expiration time
//
// Example:
//
//	// Create credentials valid for 24 hours
//	privilegeKey, timestamp, expiresAt := CalculatePrivilegeKeyWithDuration(
//	    "my_secret_token",
//	    24 * time.Hour,
//	)
//	fmt.Printf("Credentials expire at: %s\n", expiresAt)
func CalculatePrivilegeKeyWithDuration(token string, validDuration time.Duration) (
	privilegeKey string,
	timestamp int64,
	expiresAt time.Time,
) {
	// Calculate expiration time
	expiresAt = time.Now().Add(validDuration)
	timestamp = expiresAt.Unix()

	// Calculate privilegeKey
	privilegeKey, _ = CalculatePrivilegeKey(token, timestamp)

	return privilegeKey, timestamp, expiresAt
}

// ValidatePrivilegeKey validates if a privilegeKey matches the expected value
// for a given token and timestamp. This can be used on the server side to verify
// credentials before distributing them.
//
// Parameters:
//   - token: The original FRP authentication token
//   - timestamp: The timestamp used to calculate the privilegeKey
//   - providedKey: The privilegeKey to validate
//
// Returns:
//   - true if the providedKey is valid, false otherwise
func ValidatePrivilegeKey(token string, timestamp int64, providedKey string) bool {
	expectedKey, _ := CalculatePrivilegeKey(token, timestamp)
	return expectedKey == providedKey
}

// IsTimestampExpired checks if a timestamp has expired based on the current time
// and a maximum allowed age.
//
// Parameters:
//   - timestamp: The timestamp to check
//   - maxAge: Maximum allowed age (e.g., 24*time.Hour)
//
// Returns:
//   - true if expired, false otherwise
func IsTimestampExpired(timestamp int64, maxAge time.Duration) bool {
	t := time.Unix(timestamp, 0)
	age := time.Since(t)
	return age > maxAge
}
