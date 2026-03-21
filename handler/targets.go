package handler

import (
	"fmt"
	"sort"

	"icinga-webhook-bridge/config"
)

func sortedTargets(targets map[string]config.TargetConfig) []config.TargetConfig {
	list := make([]config.TargetConfig, 0, len(targets))
	for _, target := range targets {
		list = append(list, target)
	}

	sort.Slice(list, func(i, j int) bool {
		if list[i].HostName == list[j].HostName {
			return list[i].ID < list[j].ID
		}
		return list[i].HostName < list[j].HostName
	})

	return list
}

func resolveKnownHost(targets map[string]config.TargetConfig, host string) (config.TargetConfig, bool) {
	for _, target := range targets {
		if target.HostName == host {
			return target, true
		}
	}
	return config.TargetConfig{}, false
}

func resolveScopedHosts(targets map[string]config.TargetConfig, requestedHost string) ([]config.TargetConfig, error) {
	if requestedHost != "" {
		target, ok := resolveKnownHost(targets, requestedHost)
		if !ok {
			return nil, fmt.Errorf("unknown host: %s", requestedHost)
		}
		return []config.TargetConfig{target}, nil
	}
	return sortedTargets(targets), nil
}

func resolveSingleHost(targets map[string]config.TargetConfig, requestedHost string) (config.TargetConfig, error) {
	if requestedHost != "" {
		target, ok := resolveKnownHost(targets, requestedHost)
		if !ok {
			return config.TargetConfig{}, fmt.Errorf("unknown host: %s", requestedHost)
		}
		return target, nil
	}

	hostTargets := sortedTargets(targets)
	if len(hostTargets) == 1 {
		return hostTargets[0], nil
	}

	return config.TargetConfig{}, fmt.Errorf("host query parameter is required when multiple targets are configured")
}

func targetHostNames(targets map[string]config.TargetConfig) []string {
	hostTargets := sortedTargets(targets)
	names := make([]string, 0, len(hostTargets))
	for _, target := range hostTargets {
		names = append(names, target.HostName)
	}
	return names
}
