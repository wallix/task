package redis

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/wallix/task/v3/internal/lock"
)

const (
	lockTTL       = 30   // seconds; kept alive by heartbeat
	heartbeatFreq = 10   // seconds between renewals
	retryInterval = 5    // seconds between acquire retries
	acquireMax    = 3600 // 1 hour max wait for contention
	connectGiveUp = 30   // seconds; give up on connection errors (triggers local fallback)
)

// Lua: release only if we own the lock.
const releaseLua = `if redis.call("get",KEYS[1])==ARGV[1] then return redis.call("del",KEYS[1]) else return 0 end`

// Lua: renew TTL only if we own the lock.
const renewLua = `if redis.call("get",KEYS[1])==ARGV[1] then return redis.call("expire",KEYS[1],ARGV[2]) else return 0 end`

// Locker implements lock.Locker using Redis SET NX EX with a heartbeat.
// Each Lock call parses the given name as a Redis URL (redis://host/key).
type Locker struct {
	url        *url.URL
	acquireMax time.Duration // max wait for contention; 0 = use default (1h)
}

// NewLocker creates a Redis-backed locker from a parsed URL.
// The URL path prefix is combined with the lock name to form the Redis key.
func NewLocker(u *url.URL) *Locker {
	return &Locker{url: u}
}

// NewLockerWithTimeout creates a Redis-backed locker with a custom
// contention timeout. If timeout is 0, the default (1h) is used.
func NewLockerWithTimeout(u *url.URL, timeout time.Duration) *Locker {
	return &Locker{url: u, acquireMax: timeout}
}

// Lock acquires a distributed lock. The name is appended to the base URL
// path to form the Redis key. Blocks until acquired or timeout.
func (l *Locker) Lock(name string, onContention func()) (lock.Unlocker, error) {
	u := *l.url // copy
	if u.Path == "" || u.Path == "/" {
		u.Path = "/" + name
	} else {
		u.Path = u.Path + ":" + name
	}

	key := URLKey(&u)
	if key == "" {
		return nil, fmt.Errorf("redis lock: empty key")
	}

	owner := fmt.Sprintf("%d:%d", os.Getpid(), time.Now().UnixNano())
	ttl := strconv.Itoa(lockTTL)
	maxWait := l.acquireMax
	if maxWait == 0 {
		maxWait = acquireMax * time.Second
	}
	contentionDeadline := time.Now().Add(maxWait)
	connectDeadline := time.Now().Add(connectGiveUp * time.Second)
	notified := false

	for {
		c, err := Dial(l.url)
		if err != nil {
			// Connection error — give up quickly so the caller can
			// fall back to local locking.
			if time.Now().After(connectDeadline) {
				return nil, fmt.Errorf("redis lock: connect failed for %q: %w", key, err)
			}
			time.Sleep(retryInterval * time.Second)
			continue
		}
		// Connected — reset the connect deadline on every successful dial
		// so only consecutive failures count toward the 30s budget.
		connectDeadline = time.Now().Add(connectGiveUp * time.Second)

		if err := c.Send("SET", key, owner, "NX", "EX", ttl); err != nil {
			c.Close()
			return nil, fmt.Errorf("redis SET NX: %w", err)
		}
		reply, err := c.ReadAny()
		c.Close()
		if err != nil {
			return nil, fmt.Errorf("redis SET NX: %w", err)
		}

		if reply == "+OK" {
			rl := &redisLock{url: l.url, key: key, owner: owner, stop: make(chan struct{})}
			go rl.heartbeat()
			return rl, nil
		}

		// Lock held by another process — normal contention.
		if !notified && onContention != nil {
			onContention()
			notified = true
		}
		if time.Now().After(contentionDeadline) {
			return nil, fmt.Errorf("redis lock: timeout acquiring %q", key)
		}
		time.Sleep(retryInterval * time.Second)
	}
}

type redisLock struct {
	url   *url.URL
	key   string
	owner string
	stop  chan struct{}
}

func (rl *redisLock) heartbeat() {
	ttl := strconv.Itoa(lockTTL)
	ticker := time.NewTicker(heartbeatFreq * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-rl.stop:
			return
		case <-ticker.C:
			c, err := Dial(rl.url)
			if err != nil {
				continue // best-effort
			}
			_ = c.Send("EVAL", renewLua, "1", rl.key, rl.owner, ttl)
			_, _ = c.ReadInt()
			c.Close()
		}
	}
}

func (rl *redisLock) Unlock() error {
	if rl == nil {
		return nil
	}
	select {
	case <-rl.stop:
	default:
		close(rl.stop)
	}

	c, err := Dial(rl.url)
	if err != nil {
		return err
	}
	defer c.Close()

	if err := c.Send("EVAL", releaseLua, "1", rl.key, rl.owner); err != nil {
		return fmt.Errorf("redis lock release: %w", err)
	}
	_, err = c.ReadInt()
	return err
}
