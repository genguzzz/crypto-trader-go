package store

// APIKey is an external credential for the open API / MCP server (internal/devapi). Only the
// SHA-256 hash of the secret is stored; the secret itself is shown once on creation.
type APIKey struct {
	ID         int64  `json:"id"`
	Kind       string `json:"kind"` // key (long-lived) | token (short-lived, always expires)
	Name       string `json:"name"`
	Prefix     string `json:"prefix"` // first characters of the secret, for recognition
	Hash       string `json:"-"`
	RatePerMin int    `json:"ratePerMin"`
	Burst      int    `json:"burst"`
	CreatedAt  int64  `json:"createdAt"`
	ExpiresAt  int64  `json:"expiresAt"` // 0 = never
	RevokedAt  int64  `json:"revokedAt"` // 0 = active
	LastUsedAt int64  `json:"lastUsedAt"`
	Requests   int64  `json:"requests"`
	Limited    int64  `json:"limited"` // requests rejected by the rate limiter
}

const apiKeySchema = `CREATE TABLE IF NOT EXISTS api_keys(id INTEGER PRIMARY KEY AUTOINCREMENT, kind TEXT NOT NULL, name TEXT NOT NULL,
  prefix TEXT NOT NULL, hash TEXT NOT NULL UNIQUE, rate_per_min INTEGER NOT NULL, burst INTEGER NOT NULL, created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL DEFAULT 0, revoked_at INTEGER NOT NULL DEFAULT 0, last_used_at INTEGER NOT NULL DEFAULT 0,
  requests INTEGER NOT NULL DEFAULT 0, limited INTEGER NOT NULL DEFAULT 0);`

func (s *Store) APIKeys() ([]APIKey, error) {
	rows, err := s.DB.Query(`SELECT id,kind,name,prefix,hash,rate_per_min,burst,created_at,expires_at,revoked_at,last_used_at,requests,limited FROM api_keys ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []APIKey
	for rows.Next() {
		var k APIKey
		if err := rows.Scan(&k.ID, &k.Kind, &k.Name, &k.Prefix, &k.Hash, &k.RatePerMin, &k.Burst, &k.CreatedAt, &k.ExpiresAt, &k.RevokedAt, &k.LastUsedAt, &k.Requests, &k.Limited); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

func (s *Store) AddAPIKey(k APIKey) (int64, error) {
	res, err := s.DB.Exec(`INSERT INTO api_keys(kind,name,prefix,hash,rate_per_min,burst,created_at,expires_at) VALUES(?,?,?,?,?,?,?,?)`,
		k.Kind, k.Name, k.Prefix, k.Hash, k.RatePerMin, k.Burst, k.CreatedAt, k.ExpiresAt)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) UpdateAPIKey(k APIKey) error {
	_, err := s.DB.Exec(`UPDATE api_keys SET name=?,rate_per_min=?,burst=?,revoked_at=? WHERE id=?`, k.Name, k.RatePerMin, k.Burst, k.RevokedAt, k.ID)
	return err
}

// PutAPIKeyUsage persists usage counters (flushed periodically, not per request).
func (s *Store) PutAPIKeyUsage(id, lastUsed, requests, limited int64) error {
	_, err := s.DB.Exec(`UPDATE api_keys SET last_used_at=?,requests=?,limited=? WHERE id=?`, lastUsed, requests, limited, id)
	return err
}

func (s *Store) DeleteAPIKey(id int64) error {
	_, err := s.DB.Exec(`DELETE FROM api_keys WHERE id=?`, id)
	return err
}
