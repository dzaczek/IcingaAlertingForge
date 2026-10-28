package main

import (
	"crypto/tls"
	"net/http"
)

func main() {
	_ = &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
}
