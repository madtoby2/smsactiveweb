package main

import (
	"log"
	"net/http"
	"sms-platform/internal/config"
	"sms-platform/internal/store"
	webapp "sms-platform/internal/web"
)

func main() {
	c := config.Load()
	if c.HeroKey == "" {
		log.Fatal("HEROSMS_API_KEY is required")
	}
	s, e := store.Open("data/platform.db")
	if e != nil {
		log.Fatal(e)
	}
	defer s.Close()
	srv := &http.Server{Addr: ":" + c.Port, Handler: webapp.New(c, s).Routes(), ReadHeaderTimeout: 5e9, ReadTimeout: 15e9, WriteTimeout: 30e9, IdleTimeout: 60e9}
	log.Printf("平台已启动: %s", c.BaseURL)
	log.Fatal(srv.ListenAndServe())
}
