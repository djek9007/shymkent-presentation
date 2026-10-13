package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"school-platform/internal/app"
	"syscall"
	"time"
)

func main() {
	a, err := app.New()
	if err != nil {
		log.Fatal(err)
	}
	defer a.DB.Close()
	if len(os.Args) > 1 {
		if err = a.Command(os.Args[1:]); err != nil {
			log.Fatal(err)
		}
		return
	}
	s := &http.Server{Addr: a.Addr, Handler: a.Handler(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 60 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 90 * time.Second}
	go func() {
		log.Printf("listening on %s", a.Addr)
		if err := s.ListenAndServe(); err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = s.Shutdown(shutdown)
}
