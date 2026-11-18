/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package authz

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"
)

// decision is one cached Flow 2b authorization outcome.
type decision struct {
	allow     bool
	subject   string
	callerARN string
	accountID string
}

// entry is a decision plus its expiry.
type entry struct {
	decision decision
	expires  time.Time
}

// decisionCache is an in-process, bounded TTL cache keyed on the SHA-256
// of (token, arn). Tokens are never stored in the clear. Application-level
// (inside the Lambda) rather than API Gateway's authorizer-result cache,
// per the Phase 4 design.
type decisionCache struct {
	mu         sync.Mutex
	ttl        time.Duration
	maxEntries int
	entries    map[string]entry
}

func newDecisionCache(maxEntries int) *decisionCache {
	if maxEntries <= 0 {
		maxEntries = 4096
	}
	return &decisionCache{
		ttl:        5 * time.Minute,
		maxEntries: maxEntries,
		entries:    make(map[string]entry),
	}
}

func cacheKey(token, arn string) string {
	sum := sha256.Sum256([]byte(token + "\x00" + arn))
	return hex.EncodeToString(sum[:])
}

// expiry returns the cache deadline for a decision on a token expiring at
// tokenExpiry: the smaller of the TTL and the token's remaining validity,
// so a cached decision never outlives its token.
func (c *decisionCache) expiry(tokenExpiry time.Time) time.Time {
	ttlDeadline := time.Now().Add(c.ttl)
	if tokenExpiry.Before(ttlDeadline) {
		return tokenExpiry
	}
	return ttlDeadline
}

func (c *decisionCache) get(token, arn string) (decision, bool) {
	key := cacheKey(token, arn)
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok {
		return decision{}, false
	}
	if time.Now().After(e.expires) {
		delete(c.entries, key)
		return decision{}, false
	}
	return e.decision, true
}

func (c *decisionCache) put(token, arn string, d decision, expires time.Time) {
	key := cacheKey(token, arn)
	c.mu.Lock()
	defer c.mu.Unlock()
	// Bound size: drop expired entries first, then evict arbitrary ones.
	if len(c.entries) >= c.maxEntries {
		c.evictLocked()
	}
	if len(c.entries) >= c.maxEntries {
		for k := range c.entries {
			delete(c.entries, k)
			break
		}
	}
	c.entries[key] = entry{decision: d, expires: expires}
}

func (c *decisionCache) evictLocked() {
	now := time.Now()
	for k, e := range c.entries {
		if now.After(e.expires) {
			delete(c.entries, k)
		}
	}
}
