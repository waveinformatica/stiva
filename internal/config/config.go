package config

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"

	"registry/internal/auth"
)

// Config is the full registry configuration.
type Config struct {
	ListenAddr  string `json:"listen_addr"`
	PostgresDSN string `json:"postgres_dsn"`
	UploadsDir  string `json:"uploads_dir"`
	// Blob is NOT consulted at runtime. Storage comes from the named blob store
	// each registry references, and from nowhere else. This field exists only to
	// migrate deployments that predate named stores, where the registry declared
	// a type and inherited bucket and credentials from the process environment.
	// Once every registry references a store it is never read again.
	BasePath string           `json:"base_path"`
	Auth     *auth.AuthConfig `json:"auth"`

	// AdminBootstrap is used only at startup to create the first local admin.
	AdminUser string `json:"-"`
	AdminPass string `json:"-"`
}

// Default returns sane defaults (file blobs, no auth, anonymous access).
func Default() Config {
	return Config{
		ListenAddr:  ":8080",
		PostgresDSN: envOr("REGISTRY_POSTGRES", "postgres://postgres:postgres@localhost:5432/registry?sslmode=disable"),
		UploadsDir:  envOr("REGISTRY_UPLOADS", os.TempDir()+"/registry/uploads"),
		BasePath:    "/v2",
		Auth: &auth.AuthConfig{
			TokenTTL:       envOr("REGISTRY_AUTH_TTL", "24h"),
			TokenSecret:    os.Getenv("REGISTRY_AUTH_SECRET"),
			TrustedProxies: envIntOr("REGISTRY_TRUSTED_PROXIES", 0),
			Realms:         []string{"local"},
			Local:          &auth.LocalConfig{},
		},
		AdminUser: envOr("REGISTRY_ADMIN_USER", ""),
		AdminPass: envOr("REGISTRY_ADMIN_PASS", ""),
	}
}

// Load resolves configuration from an optional JSON file, flags and environment.
func Load() Config {
	cfg := Default()

	configFile := flag.String("config", envOr("REGISTRY_CONFIG", ""), "path to JSON config file")
	flag.StringVar(&cfg.ListenAddr, "listen", cfg.ListenAddr, "HTTP listen address")
	flag.StringVar(&cfg.PostgresDSN, "postgres", cfg.PostgresDSN, "PostgreSQL DSN (metadata store)")
	flag.StringVar(&cfg.UploadsDir, "uploads", cfg.UploadsDir, "Local directory for in-progress uploads")
	flag.StringVar(&cfg.AdminUser, "admin-user", cfg.AdminUser, "Bootstrap local admin username")
	flag.StringVar(&cfg.AdminPass, "admin-pass", cfg.AdminPass, "Bootstrap local admin password")
	flag.Parse()

	if *configFile != "" {
		if b, err := os.ReadFile(*configFile); err == nil {
			var fileCfg Config
			if json.Unmarshal(b, &fileCfg) == nil {
				mergeConfig(&cfg, &fileCfg)
			}
		}
	}
	return cfg
}

// mergeConfig overlays non-zero fields from src onto dst.
func mergeConfig(dst, src *Config) {
	if src.ListenAddr != "" {
		dst.ListenAddr = src.ListenAddr
	}
	if src.PostgresDSN != "" {
		dst.PostgresDSN = src.PostgresDSN
	}
	if src.UploadsDir != "" {
		dst.UploadsDir = src.UploadsDir
	}
	if src.BasePath != "" {
		dst.BasePath = src.BasePath
	}
	if src.Auth != nil {
		dst.Auth = src.Auth
	}
}

// Validate checks the configuration for consistency.
func (c Config) Validate() error {
	if c.ListenAddr == "" {
		return fmt.Errorf("listen address must not be empty")
	}
	if c.PostgresDSN == "" {
		return fmt.Errorf("postgres DSN is required (no local metadata store is supported)")
	}
	if c.Auth == nil {
		return fmt.Errorf("auth config must not be nil")
	}
	return nil
}

func envOr(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

// envIntOr reads a small integer setting.
func envIntOr(key string, def int) int {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envBoolOr(key string, def bool) bool {
	if v, ok := os.LookupEnv(key); ok {
		return v == "1" || v == "true" || v == "yes"
	}
	return def
}
