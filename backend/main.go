package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
	"time"

	"beacon/internal/config"
	"beacon/internal/geoip"
	"beacon/internal/render"
)

var ipv4ShapeRe = regexp.MustCompile(`^\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}$`)

type ipCheck int

const (
	ipValid ipCheck = iota
	ipMalformed
	ipOctetOutOfRange
)

func main() {
	log.SetFlags(log.LstdFlags)

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	log.Printf("GeoIP update interval set to %d hour(s)", cfg.UpdatePeriodHours)

	gi, err := geoip.New(cfg)
	if err != nil {
		log.Fatalf("geoip: %v", err)
	}
	defer gi.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go gi.RefreshLoop(ctx)
	log.Println("started periodic GeoIP database update task")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		render.FromLookup(ip, gi.Lookup(ip)).Write(w, r.Header.Get("Accept"))
	})
	mux.HandleFunc("GET /{ip...}", func(w http.ResponseWriter, r *http.Request) {
		ipPath := r.PathValue("ip")
		if c := checkIP(ipPath); c != ipValid {
			status, detail := ipCheckError(c)
			writeError(w, status, detail)
			return
		}
		render.FromLookup(ipPath, gi.Lookup(ipPath)).Write(w, r.Header.Get("Accept"))
	})

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		log.Printf("listening on %s", cfg.ListenAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("shutting down...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("graceful shutdown failed: %v", err)
	}
}

func checkIP(p string) ipCheck {
	if net.ParseIP(p) != nil {
		return ipValid
	}
	if ipv4ShapeRe.MatchString(p) {
		return ipOctetOutOfRange
	}
	return ipMalformed
}

func ipCheckError(c ipCheck) (int, string) {
	switch c {
	case ipMalformed:
		return http.StatusNotFound, "Invalid IP address format"
	case ipOctetOutOfRange:
		return http.StatusBadRequest, "Invalid IP address"
	default:
		return http.StatusInternalServerError, "internal error"
	}
}

func clientIP(r *http.Request) string {
	if xri := strings.TrimSpace(r.Header.Get("X-Real-IP")); xri != "" {
		return xri
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func writeError(w http.ResponseWriter, status int, detail string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"detail": detail})
}
