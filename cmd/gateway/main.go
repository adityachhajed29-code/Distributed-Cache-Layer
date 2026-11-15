package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func main() {
	address := flag.String("addr", ":8080", "HTTP listen address")
	stopFile := flag.String("stop_file", "", "optional graceful shutdown signal file")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if *stopFile != "" {
		go func() {
			ticker := time.NewTicker(200 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					if _, err := os.Stat(*stopFile); err == nil {
						stop()
						return
					}
				}
			}
		}()
	}
	gateway, err := newGateway(strings.Split(os.Getenv("SHARDS"), ","), os.Getenv("DISCOVER_MEMBERS") != "false")
	if err != nil {
		log.Fatal(err)
	}
	gateway.refresh(ctx)
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				gateway.refresh(ctx)
			}
		}
	}()
	srv := &http.Server{Addr: *address, Handler: gateway, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(c)
	}()
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
