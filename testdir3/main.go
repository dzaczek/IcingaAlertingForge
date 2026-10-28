package main

import (
	"crypto/tls"
	"net/http"
)

type APIClient struct {
	HTTPClient *http.Client
}

func (c *APIClient) UpdateCredentials(tlsSkipVerify bool) {
    // #nosec G402
	c.HTTPClient.Transport.(*http.Transport).TLSClientConfig.InsecureSkipVerify = tlsSkipVerify
}

func main() {
    _ = tls.Config{}
}
