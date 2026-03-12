package redis

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
)

const cacheTTL = 2 * 24 * 3600 // 2 days; refreshed on read

// CacheGet fetches a cached value from Redis and writes it to a local file.
// Returns the file path on hit, or ("", nil) on miss.
func CacheGet(u *url.URL, dstDir string) (string, error) {
	key := URLKey(u)
	if key == "" {
		return "", fmt.Errorf("redis cache: empty key")
	}

	c, err := Dial(u)
	if err != nil {
		return "", err
	}
	defer c.Close()

	if err := c.Send("GET", key); err != nil {
		return "", fmt.Errorf("redis GET %q: %w", key, err)
	}
	data, err := c.ReadBulk()
	if err != nil {
		return "", fmt.Errorf("redis GET %q: %w", key, err)
	}
	if data == nil {
		return "", nil // cache miss
	}

	// Refresh TTL so actively-used keys stay alive
	ttl := strconv.Itoa(cacheTTL)
	if err := c.Send("EXPIRE", key, ttl); err == nil {
		_, _ = c.ReadInt() // best-effort
	}

	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return "", err
	}
	file := filepath.Join(dstDir, filepath.Base(key))
	tmp := file + ".part"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, file); err != nil {
		os.Remove(tmp)
		return "", err
	}
	return file, nil
}

// CachePut reads a file and stores it in Redis with a TTL.
func CachePut(u *url.URL, filePath string) error {
	key := URLKey(u)
	if key == "" {
		return fmt.Errorf("redis cache: empty key")
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("redis cache: read %s: %w", filePath, err)
	}

	c, err := Dial(u)
	if err != nil {
		return err
	}
	defer c.Close()

	ttl := strconv.Itoa(cacheTTL)
	// SET key <binary> EX ttl — use SendBinary for the data argument
	if err := c.SendBinary([]string{"SET", key, "", "EX", ttl}, 2, data); err != nil {
		return fmt.Errorf("redis SET %q: %w", key, err)
	}
	if _, err := c.ReadStatus(); err != nil {
		return fmt.Errorf("redis SET %q: %w", key, err)
	}
	return nil
}
