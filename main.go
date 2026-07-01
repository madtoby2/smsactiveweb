package main

import (
	"context"
	"log"
	"net/http"
	"os/signal"
	"sms-platform/internal/config"
	"sms-platform/internal/store"
	webapp "sms-platform/internal/web"
	"syscall"
)

func main() {
	c := config.Load()
	if err := c.Validate(); err != nil {
		log.Fatal(err)
	}
	s, e := store.Open("data/platform.db")
	if e != nil {
		log.Fatal(e)
	}
	defer s.Close()
	app := webapp.New(c, s)
	bgCtx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	go app.RunAutoReplace(bgCtx)
	srv := &http.Server{Addr: ":" + c.Port, Handler: app.Routes(), ReadHeaderTimeout: 5e9, ReadTimeout: 15e9, WriteTimeout: 30e9, IdleTimeout: 60e9}
	log.Printf("骞冲彴宸插惎鍔? %s", c.BaseURL)
	log.Fatal(srv.ListenAndServe())
}
