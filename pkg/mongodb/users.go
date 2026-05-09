/*
Copyright 2024 AgentTier Authors.

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

package mongodb

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// UserPreferences stores per-user settings and API keys.
type UserPreferences struct {
	UserID      string              `bson:"userId" json:"userId"`
	Email       string              `bson:"email" json:"email"`
	DisplayName string              `bson:"displayName" json:"displayName"`
	Preferences PreferencesConfig   `bson:"preferences" json:"preferences"`
	APIKeys     []StoredAPIKey      `bson:"apiKeys,omitempty" json:"apiKeys,omitempty"`
	UpdatedAt   time.Time           `bson:"updatedAt" json:"updatedAt"`
}

// PreferencesConfig holds user preference settings.
type PreferencesConfig struct {
	DefaultTemplate  string               `bson:"defaultTemplate,omitempty" json:"defaultTemplate,omitempty"`
	DefaultNamespace string               `bson:"defaultNamespace,omitempty" json:"defaultNamespace,omitempty"`
	Notifications    NotificationPrefs    `bson:"notifications,omitempty" json:"notifications,omitempty"`
	Terminal         TerminalPrefs        `bson:"terminal,omitempty" json:"terminal,omitempty"`
}

// NotificationPrefs defines notification preferences.
type NotificationPrefs struct {
	IdleWarning bool     `bson:"idleWarning" json:"idleWarning"`
	ErrorAlerts bool     `bson:"errorAlerts" json:"errorAlerts"`
	Channels    []string `bson:"channels,omitempty" json:"channels,omitempty"` // "webhook", "email", "slack"
}

// TerminalPrefs defines terminal display preferences.
type TerminalPrefs struct {
	FontSize int    `bson:"fontSize,omitempty" json:"fontSize,omitempty"`
	Theme    string `bson:"theme,omitempty" json:"theme,omitempty"`
}

// StoredAPIKey represents an API key stored in the database.
// The actual key value is never stored — only its SHA-256 hash.
type StoredAPIKey struct {
	KeyHash           string     `bson:"keyHash" json:"keyHash"`
	Name              string     `bson:"name" json:"name"`
	Scopes            []string   `bson:"scopes,omitempty" json:"scopes,omitempty"`
	AllowedNamespaces []string   `bson:"allowedNamespaces,omitempty" json:"allowedNamespaces,omitempty"`
	CreatedAt         time.Time  `bson:"createdAt" json:"createdAt"`
	LastUsedAt        *time.Time `bson:"lastUsedAt,omitempty" json:"lastUsedAt,omitempty"`
	ExpiresAt         *time.Time `bson:"expiresAt,omitempty" json:"expiresAt,omitempty"`
}

// GetUserPreferences retrieves preferences for a user, creating defaults if not found.
func (c *Client) GetUserPreferences(ctx context.Context, userID string) (*UserPreferences, error) {
	var prefs UserPreferences
	err := c.Collection(CollUserPreferences).FindOne(ctx, bson.D{{Key: "userId", Value: userID}}).Decode(&prefs)
	if err != nil {
		// Return empty defaults if not found
		return &UserPreferences{
			UserID: userID,
			Preferences: PreferencesConfig{
				Notifications: NotificationPrefs{
					IdleWarning: true,
					ErrorAlerts: true,
				},
			},
		}, nil
	}
	return &prefs, nil
}

// UpdateUserPreferences updates or creates user preferences (upsert).
func (c *Client) UpdateUserPreferences(ctx context.Context, prefs *UserPreferences) error {
	prefs.UpdatedAt = time.Now()

	filter := bson.D{{Key: "userId", Value: prefs.UserID}}
	opts := options.Replace().SetUpsert(true)
	_, err := c.Collection(CollUserPreferences).ReplaceOne(ctx, filter, prefs, opts)
	return err
}

// AddAPIKey adds a new API key to a user's preferences.
func (c *Client) AddAPIKey(ctx context.Context, userID string, key StoredAPIKey) error {
	key.CreatedAt = time.Now()

	filter := bson.D{{Key: "userId", Value: userID}}
	update := bson.D{
		{Key: "$push", Value: bson.D{{Key: "apiKeys", Value: key}}},
		{Key: "$set", Value: bson.D{{Key: "updatedAt", Value: time.Now()}}},
	}

	_, err := c.Collection(CollUserPreferences).UpdateOne(ctx, filter, update)
	return err
}

// RemoveAPIKey removes an API key from a user's preferences.
func (c *Client) RemoveAPIKey(ctx context.Context, userID, keyHash string) error {
	filter := bson.D{{Key: "userId", Value: userID}}
	update := bson.D{
		{Key: "$pull", Value: bson.D{{Key: "apiKeys", Value: bson.D{{Key: "keyHash", Value: keyHash}}}}},
		{Key: "$set", Value: bson.D{{Key: "updatedAt", Value: time.Now()}}},
	}

	_, err := c.Collection(CollUserPreferences).UpdateOne(ctx, filter, update)
	return err
}

// GetAPIKeyByHash looks up an API key by its hash across all users.
func (c *Client) GetAPIKeyByHash(ctx context.Context, keyHash string) (*StoredAPIKey, error) {
	var prefs UserPreferences
	filter := bson.D{{Key: "apiKeys.keyHash", Value: keyHash}}
	err := c.Collection(CollUserPreferences).FindOne(ctx, filter).Decode(&prefs)
	if err != nil {
		return nil, err
	}

	for _, key := range prefs.APIKeys {
		if key.KeyHash == keyHash {
			return &key, nil
		}
	}
	return nil, nil
}

// ListAPIKeys returns all API keys for a user (without revealing key values).
func (c *Client) ListAPIKeys(ctx context.Context, userID string) ([]StoredAPIKey, error) {
	prefs, err := c.GetUserPreferences(ctx, userID)
	if err != nil {
		return nil, err
	}
	return prefs.APIKeys, nil
}
