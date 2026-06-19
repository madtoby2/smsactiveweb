package main

import (
	"context"
	"log"
	"net/http"
	"sms-platform/internal/config"
	"sms-platform/internal/store"
	webapp "sms-platform/internal/web"
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
	go app.RunAutoReplace(context.Background())
	srv := &http.Server{Addr: ":" + c.Port, Handler: app.Routes(), ReadHeaderTimeout: 5e9, ReadTimeout: 15e9, WriteTimeout: 30e9, IdleTimeout: 60e9}
	log.Printf("平台已启动: %s", c.BaseURL)
	log.Fatal(srv.ListenAndServe())
}
