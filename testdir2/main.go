package main

import (
	"crypto/tls"
	"net/http"
)

type APIClient struct {
	HTTPClient *http.Client
}

func (c *APIClient) UpdateCredentials(tlsSkipVerify bool) {
	c.HTTPClient.Transport.(*http.Transport).TLSClientConfig.InsecureSkipVerify = tlsSkipVerify
}

func main() {}
