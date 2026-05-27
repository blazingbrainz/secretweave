package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	ParentNamespace  string
	AnnotationKey    string
	AnnotationValue  string
	SyncInterval     time.Duration
	FullSyncInterval time.Duration
	IncludeNamespaces []string
	ExcludeNamespaces []string
	LogDir            string
	LogRetentionDays  int
	WorkerCount       int
	DeleteOnRemove    bool
}

func Load() *Config {
	return &Config{
		ParentNamespace:  getEnv("PARENT_NAMESPACE", "default"),
		AnnotationKey:    getEnv("ANNOTATION_KEY", "secretweave.io/sync"),
		AnnotationValue:  getEnv("ANNOTATION_VALUE", "true"),
		SyncInterval:     getDuration("SYNC_INTERVAL", "30s"),
		FullSyncInterval: getDuration("FULL_SYNC_INTERVAL", "5m"),
		IncludeNamespaces: getStringSlice("INCLUDE_NAMESPACES"),
		ExcludeNamespaces: getStringSlice("EXCLUDE_NAMESPACES"),
		LogDir:           getEnv("LOG_DIR", "/var/log/secretweave"),
		LogRetentionDays: getInt("LOG_RETENTION_DAYS", 30),
		WorkerCount:      getInt("WORKER_COUNT", 20),
		DeleteOnRemove:   getBool("DELETE_ON_REMOVE", true),
	}
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getDuration(key, def string) time.Duration {
	v := getEnv(key, def)
	d, err := time.ParseDuration(v)
	if err != nil {
		d, _ = time.ParseDuration(def)
	}
	return d
}

func getInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func getStringSlice(key string) []string {
	v := os.Getenv(key)
	if v == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func getBool(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}
