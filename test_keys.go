package main

import (
    "fmt"
    "icinga-webhook-bridge/config"
    "icinga-webhook-bridge/auth"
)

func main() {
    ks := auth.NewKeyStore(map[string]config.WebhookRoute{
        "default-key": config.WebhookRoute{TargetID: "team-a", Source: "grafana-test"},
    })
    _, ok := ks.ValidateKey("default-key")
    fmt.Println(ok)
}
