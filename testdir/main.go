package main

import (
	"crypto/tls"
	"net/http"
)

func main() {
	_ = &http.Transport{
		// #nosec G402
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
}
