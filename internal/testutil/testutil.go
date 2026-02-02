//go:build integration || e2e

// Package testutil provides test helpers for integration and e2e tests.
package testutil

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
)

// RedisAddr returns the address of the test Redis container (IP:port).
// It first checks NEWTRON_TEST_REDIS_ADDR, then discovers the Docker container IP.
func RedisAddr() string {
	if addr := os.Getenv("NEWTRON_TEST_REDIS_ADDR"); addr != "" {
		return addr
	}

	ip := redisContainerIP()
	if ip == "" {
		return ""
	}
	return ip + ":6379"
}

// RedisIP returns just the IP of the test Redis container (no port).
func RedisIP() string {
	if addr := os.Getenv("NEWTRON_TEST_REDIS_ADDR"); addr != "" {
		// Strip port if present
		if idx := strings.LastIndex(addr, ":"); idx > 0 {
			return addr[:idx]
		}
		return addr
	}
	return redisContainerIP()
}

func redisContainerIP() string {
	out, err := exec.Command("docker", "inspect",
		"--format", "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}",
		"newtron-test-redis").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// SkipIfNoRedis skips the test if the test Redis container is not reachable.
func SkipIfNoRedis(t *testing.T) {
	t.Helper()

	addr := RedisAddr()
	if addr == "" {
		t.Skip("test Redis not available: run ./testlab/setup.sh redis-start")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	client := redis.NewClient(&redis.Options{Addr: addr})
	defer client.Close()

	if err := client.Ping(ctx).Err(); err != nil {
		t.Skipf("test Redis not reachable at %s: %v", addr, err)
	}
}

// SeedPath returns the absolute path to a seed file under testlab/seed/.
func SeedPath(name string) string {
	return filepath.Join(testlabDir(), "seed", name)
}

// SpecsPath returns the absolute path to the testlab/specs/ directory.
func SpecsPath() string {
	return filepath.Join(testlabDir(), "specs")
}

// testlabDir locates the testlab directory relative to the module root.
func testlabDir() string {
	if dir := os.Getenv("NEWTRON_TESTLAB_DIR"); dir != "" {
		return dir
	}
	// Walk up from this file's directory to find the module root
	_, thisFile, _, _ := runtime.Caller(0)
	dir := filepath.Dir(thisFile)
	// internal/testutil -> project root
	root := filepath.Join(dir, "..", "..")
	return filepath.Join(root, "testlab")
}

// ProjectRoot returns the absolute path to the project root.
func ProjectRoot() string {
	_, thisFile, _, _ := runtime.Caller(0)
	dir := filepath.Dir(thisFile)
	return filepath.Join(dir, "..", "..")
}

// RequireRedis is like SkipIfNoRedis but fails the test instead of skipping.
func RequireRedis(t *testing.T) {
	t.Helper()

	addr := RedisAddr()
	if addr == "" {
		t.Fatal("test Redis not available: run ./testlab/setup.sh redis-start")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	client := redis.NewClient(&redis.Options{Addr: addr})
	defer client.Close()

	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatalf("test Redis not reachable at %s: %v", addr, err)
	}
}

// FlushAll flushes all databases on the test Redis instance.
func FlushAll(t *testing.T) {
	t.Helper()

	addr := RedisAddr()
	ctx := context.Background()

	for _, db := range []int{4, 6} {
		client := redis.NewClient(&redis.Options{Addr: addr, DB: db})
		if err := client.FlushDB(ctx).Err(); err != nil {
			t.Fatalf("failed to flush DB %d: %v", db, err)
		}
		client.Close()
	}
}

// KeyCount returns the number of keys in a Redis database.
func KeyCount(t *testing.T, db int) int {
	t.Helper()

	addr := RedisAddr()
	client := redis.NewClient(&redis.Options{Addr: addr, DB: db})
	defer client.Close()

	n, err := client.DBSize(context.Background()).Result()
	if err != nil {
		t.Fatalf("failed to get key count for DB %d: %v", db, err)
	}
	return int(n)
}

// DumpKeys returns all keys in a Redis database (for debugging).
func DumpKeys(t *testing.T, db int) []string {
	t.Helper()

	addr := RedisAddr()
	client := redis.NewClient(&redis.Options{Addr: addr, DB: db})
	defer client.Close()

	keys, err := client.Keys(context.Background(), "*").Result()
	if err != nil {
		t.Fatalf("failed to get keys for DB %d: %v", db, err)
	}
	return keys
}

// Context returns a context with a reasonable timeout for tests.
// The cancel function is registered via t.Cleanup.
func Context(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// ContextWithCancel returns a context with cancel function.
func ContextWithCancel() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 30*time.Second)
}

// MustEnv returns the value of an environment variable or fails the test.
func MustEnv(t *testing.T, key string) string {
	t.Helper()
	v := os.Getenv(key)
	if v == "" {
		t.Fatalf("required environment variable %s not set", key)
	}
	return v
}

// RedisClient returns a redis client for the specified DB.
func RedisClient(t *testing.T, db int) *redis.Client {
	t.Helper()
	addr := RedisAddr()
	if addr == "" {
		t.Fatal("test Redis not available")
	}
	client := redis.NewClient(&redis.Options{Addr: addr, DB: db})
	t.Cleanup(func() { client.Close() })
	return client
}

// WaitForRedis waits until Redis is ready, up to timeout.
func WaitForRedis(timeout time.Duration) error {
	addr := RedisAddr()
	if addr == "" {
		return fmt.Errorf("Redis address not available")
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		client := redis.NewClient(&redis.Options{Addr: addr})
		err := client.Ping(ctx).Err()
		client.Close()
		cancel()
		if err == nil {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("Redis not ready after %v", timeout)
}
