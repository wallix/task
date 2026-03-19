// lock-holder is a test helper that acquires a lock, creates a ready file,
// then sleeps forever so the test can kill it to verify lock release.
package main

import (
	"flag"
	"os"
	"time"

	"github.com/wallix/task/v3/internal/lock"
)

func main() {
	dir := flag.String("dir", "", "lock directory")
	name := flag.String("name", "", "lock name")
	ready := flag.String("ready", "", "path to create when lock is held")
	flag.Parse()

	locker, err := lock.NewFlock(*dir)
	if err != nil {
		panic(err)
	}
	_, err = locker.Lock(*name, nil)
	if err != nil {
		panic(err)
	}

	// Signal that we hold the lock.
	if err := os.WriteFile(*ready, []byte("ok"), 0o644); err != nil {
		panic(err)
	}

	// Sleep forever; the test will kill us.
	for {
		time.Sleep(time.Hour)
	}
}
