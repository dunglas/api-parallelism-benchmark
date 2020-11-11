package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/caddyserver/certmagic"
	"github.com/gorilla/handlers"
	"github.com/tcnksm/go-casper"
)

// type: push, casper or compound
// push: set to 1 to push related resources using HTTP/2 Server Push
// nb: the number of documents to send
// bytes: the size of each document to send
// delay: the delay to add representing the time necessary to generate a single resource
// hits: the number of resources stored in cache (no delay for generating this resource)
func handler(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	typ := query.Get("type")
	nb, err := strconv.Atoi(query.Get("nb"))
	if err != nil {
		http.Error(w, `The "nb" parameter must be provided and must be an int`, http.StatusBadRequest)
		return
	}

	bytes, err := strconv.Atoi(query.Get("bytes"))
	if err != nil {
		http.Error(w, `The "bytes" parameter must be provided and must be an int`, http.StatusBadRequest)
		return
	}

	delay, err := strconv.Atoi(query.Get("delay"))
	if err != nil {
		http.Error(w, `The "delay" parameter must be provided and must be an int`, http.StatusBadRequest)
		return
	}

	hits, err := strconv.Atoi(query.Get("hits"))
	if err != nil {
		http.Error(w, `The "hits" parameter must be provided and must be an int`, http.StatusBadRequest)
		return
	}

	if typ == "compound" {
		time.Sleep(time.Duration((nb-hits)*delay) * time.Millisecond)
		sendChunked(w, bytes*nb)
		return
	}

	if typ == "push" || typ == "casper" {
		var casperPusher *casper.Casper
		if typ == "casper" {
			casperPusher = casper.New(1<<6, nb)
		}

		var toPush []string
		for i := 1; i < nb; i++ {
			var hit int
			if i < hits {
				hit = 1
			}

			u := fmt.Sprintf("/api?type=%s&nb=1&bytes=%d&delay=%d&hits=%d&id=%d", typ, bytes, delay, hit, i)
			if casperPusher == nil {
				if err := w.(http.Pusher).Push(u, nil); err != nil {
					log.Printf(`failed to push "%s"`, u)
				}
			} else {
				toPush = append(toPush, u)
			}
		}

		if casperPusher != nil && len(toPush) != 0 {
			if _, err := casperPusher.Push(w, r, toPush, nil); err != nil {
				log.Printf("failed to push %v", toPush)
			}
		}
	}

	if hits == 0 {
		time.Sleep(time.Duration(delay) * time.Millisecond)
	}

	sendChunked(w, bytes)
}

func sendChunked(w http.ResponseWriter, bytes int) {
	w.Header().Set("Content-Length", strconv.Itoa(bytes))
	w.Header().Set("Content-Type", "text/plain")

	// Chunks of 1kb
	for i := 0; i < bytes/1024; i++ {
		if _, err := io.WriteString(w, strings.Repeat("x", 1024)); err != nil {
			log.Print(err) // this error can be safely ignored, seems related to https://github.com/gin-gonic/gin/issues/2336
			return
		}
		w.(http.Flusher).Flush()
	}

	// send the rest
	if _, err := io.WriteString(w, strings.Repeat("x", bytes%1024)); err != nil {
		log.Print(err)
	}
}

func main() {
	var h http.Handler
	h = http.HandlerFunc(handler)
	if os.Getenv("COMPRESS") == "1" {
		h = handlers.CompressHandler(h)
	}

	http.Handle("/api", h)
	fs := http.FileServer(http.Dir("./static"))
	http.Handle("/", fs)

	certFile := os.Getenv("CERT_FILE")
	keyFile := os.Getenv("KEY_FILE")
	if certFile == "" || keyFile == "" {
		certmagic.DefaultACME.Email = os.Getenv("EMAIL")
		if err := certmagic.HTTPS([]string{os.Getenv("DOMAIN_NAME")}, nil); err != nil {
			log.Panic(err)
		}
		return
	}

	cm := certmagic.NewDefault()
	if err := cm.CacheUnmanagedCertificatePEMFile(certFile, keyFile, []string{}); err != nil {
		log.Panic(err)
	}

	httpsServer := &http.Server{
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      2 * time.Minute,
		IdleTimeout:       5 * time.Minute,
		TLSConfig:         cm.TLSConfig(),
	}

	log.Printf("Serving HTTPS on https://localhost using a custom certificate")
	if err := httpsServer.ListenAndServeTLS("", ""); err != nil {
		log.Panic(err)
	}
}
