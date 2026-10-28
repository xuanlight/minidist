// cmd/echo/main.go
package main

import (
	"errors"
	"flag"
	"io"
	"log"
	"net"
	"runtime"
	"time"
)

const idleTimeout = 30 * time.Second

func main() {
	addr := flag.String("addr", ":9000", "listen address")
	mode := flag.String("mode", "blocking", "blocking | goroutine")
	flag.Parse()

	if *mode != "blocking" && *mode != "goroutine" {
		log.Fatalf("unknown mode %q (want blocking or goroutine)", *mode)
	}

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	log.Printf("listening on %s (mode=%s)", ln.Addr(), *mode)

	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Printf("accept: %v", err)
			continue
		}
		log.Printf("accepted %s (goroutines=%d)", conn.RemoteAddr(), runtime.NumGoroutine())

		if *mode == "goroutine" {
			go handle(conn)
		} else {
			handle(conn)
		}
	}
}

func handle(c net.Conn) {
	defer c.Close()

	start := time.Now()
	log.Printf("echo %s <-> %s", c.RemoteAddr(), c.LocalAddr())

	src := &deadlineReader{conn: c, idle: idleTimeout}
	if _, err := io.Copy(c, src); err != nil {
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			log.Printf("idle timeout %s after %s", c.RemoteAddr(), time.Since(start).Round(time.Second))
			return
		}
		log.Printf("copy %s: %v", c.RemoteAddr(), err)
	}
	log.Printf("closed %s after %s", c.RemoteAddr(), time.Since(start).Round(time.Second))
}

// deadlineReader 每次 Read 前把读 deadline 推后 idle，实现"空闲超时"。
type deadlineReader struct {
	conn net.Conn
	idle time.Duration
}

func (r *deadlineReader) Read(p []byte) (int, error) {
	if err := r.conn.SetReadDeadline(time.Now().Add(r.idle)); err != nil {
		return 0, err
	}
	return r.conn.Read(p)
}
