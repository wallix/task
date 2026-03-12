package redis_test

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-task/task/v3/internal/redis"
)

// fakeRedis is a minimal in-memory Redis server for testing.
type fakeRedis struct {
	ln   net.Listener
	mu   sync.Mutex
	data map[string][]byte
}

func newFakeRedis(t *testing.T) (*fakeRedis, *url.URL) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeRedis{ln: ln, data: make(map[string][]byte)}
	go f.serve()
	t.Cleanup(func() { ln.Close() })

	u := &url.URL{
		Scheme: "redis",
		Host:   ln.Addr().String(),
	}
	return f, u
}

func (f *fakeRedis) serve() {
	for {
		conn, err := f.ln.Accept()
		if err != nil {
			return
		}
		go f.handle(conn)
	}
}

func (f *fakeRedis) handle(conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReader(conn)

	for {
		args, err := readRESPArray(r)
		if err != nil {
			return
		}
		if len(args) == 0 {
			continue
		}

		cmd := strings.ToUpper(args[0])
		f.mu.Lock()
		switch cmd {
		case "GET":
			if len(args) < 2 {
				conn.Write([]byte("-ERR wrong number of arguments\r\n"))
			} else {
				val, ok := f.data[args[1]]
				if !ok {
					conn.Write([]byte("$-1\r\n"))
				} else {
					conn.Write([]byte(fmt.Sprintf("$%d\r\n", len(val))))
					conn.Write(val)
					conn.Write([]byte("\r\n"))
				}
			}
		case "SET":
			if len(args) < 3 {
				conn.Write([]byte("-ERR wrong number of arguments\r\n"))
			} else {
				// Check for NX
				nx := false
				for i, a := range args {
					if strings.ToUpper(a) == "NX" && i >= 3 {
						nx = true
					}
				}
				key := args[1]
				if nx {
					if _, exists := f.data[key]; exists {
						conn.Write([]byte("$-1\r\n"))
					} else {
						f.data[key] = []byte(args[2])
						conn.Write([]byte("+OK\r\n"))
					}
				} else {
					f.data[key] = []byte(args[2])
					conn.Write([]byte("+OK\r\n"))
				}
			}
		case "DEL":
			if len(args) >= 2 {
				_, ok := f.data[args[1]]
				delete(f.data, args[1])
				if ok {
					conn.Write([]byte(":1\r\n"))
				} else {
					conn.Write([]byte(":0\r\n"))
				}
			}
		case "EXPIRE":
			conn.Write([]byte(":1\r\n"))
		case "EVAL":
			// Minimal Lua eval: support release and renew scripts
			if len(args) >= 5 {
				key := args[3]
				ownerVal := args[4]
				stored := string(f.data[key])
				if stored == ownerVal {
					if strings.Contains(args[1], "del") {
						delete(f.data, key)
						conn.Write([]byte(":1\r\n"))
					} else if strings.Contains(args[1], "expire") {
						conn.Write([]byte(":1\r\n"))
					} else {
						conn.Write([]byte(":0\r\n"))
					}
				} else {
					conn.Write([]byte(":0\r\n"))
				}
			} else {
				conn.Write([]byte(":0\r\n"))
			}
		default:
			conn.Write([]byte("-ERR unknown command\r\n"))
		}
		f.mu.Unlock()
	}
}

// readRESPArray reads a RESP array (handles both inline and *N arrays with
// binary-safe bulk strings).
func readRESPArray(r *bufio.Reader) ([]string, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return nil, err
	}
	line = strings.TrimRight(line, "\r\n")

	if len(line) == 0 {
		return nil, nil
	}
	if line[0] != '*' {
		// Inline command
		return strings.Fields(line), nil
	}

	n, err := strconv.Atoi(line[1:])
	if err != nil {
		return nil, err
	}

	args := make([]string, 0, n)
	for range n {
		line, err = r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if len(line) == 0 || line[0] != '$' {
			return nil, fmt.Errorf("expected bulk string, got %q", line)
		}
		sz, err := strconv.Atoi(line[1:])
		if err != nil {
			return nil, err
		}
		data := make([]byte, sz+2)
		if _, err := io.ReadFull(r, data); err != nil {
			return nil, err
		}
		args = append(args, string(data[:sz]))
	}
	return args, nil
}

func TestRedisCacheGetPut(t *testing.T) {
	f, baseURL := newFakeRedis(t)
	_ = f

	// Create a test file to cache
	srcDir := t.TempDir()
	srcFile := filepath.Join(srcDir, "test.zip")
	testData := []byte("hello redis cache")
	if err := os.WriteFile(srcFile, testData, 0o644); err != nil {
		t.Fatal(err)
	}

	// PUT
	u := *baseURL
	u.Path = "/cache:build:abc123.zip"
	if err := redis.CachePut(&u, srcFile); err != nil {
		t.Fatalf("CachePut: %v", err)
	}

	// GET
	dstDir := t.TempDir()
	path, err := redis.CacheGet(&u, dstDir)
	if err != nil {
		t.Fatalf("CacheGet: %v", err)
	}
	if path == "" {
		t.Fatal("CacheGet: expected hit, got miss")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(testData) {
		t.Fatalf("got %q, want %q", got, testData)
	}
}

func TestRedisCacheMiss(t *testing.T) {
	_, baseURL := newFakeRedis(t)

	u := *baseURL
	u.Path = "/nonexistent-key"
	dstDir := t.TempDir()
	path, err := redis.CacheGet(&u, dstDir)
	if err != nil {
		t.Fatalf("CacheGet: %v", err)
	}
	if path != "" {
		t.Fatalf("expected miss, got %q", path)
	}
}

func TestRedisLockerLockUnlock(t *testing.T) {
	_, baseURL := newFakeRedis(t)

	u := *baseURL
	u.Path = "/locks"
	locker := redis.NewLocker(&u)

	unlock, err := locker.Lock("test-task", nil)
	if err != nil {
		t.Fatalf("Lock: %v", err)
	}

	// Second lock on same name should block — test with a goroutine
	done := make(chan struct{})
	go func() {
		u2, _ := locker.Lock("test-task", nil)
		u2.Unlock()
		close(done)
	}()

	// Give it a moment, then release
	time.Sleep(100 * time.Millisecond)
	select {
	case <-done:
		t.Fatal("second lock should not have acquired yet")
	default:
	}

	unlock.Unlock()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("second lock should have acquired after release")
	}
}

func TestRedisLockerDifferentNames(t *testing.T) {
	_, baseURL := newFakeRedis(t)

	u := *baseURL
	u.Path = "/locks"
	locker := redis.NewLocker(&u)

	u1, err := locker.Lock("task-a", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer u1.Unlock()

	// Different name should not block
	done := make(chan struct{})
	go func() {
		u2, err := locker.Lock("task-b", nil)
		if err != nil {
			t.Error(err)
			return
		}
		u2.Unlock()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("different names should not block each other")
	}
}
