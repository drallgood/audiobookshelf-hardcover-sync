package auth

import (
	"os"
)

// ConfigAuth represents the authentication configuration from config.yaml
// This mirrors the structure in internal/config but avoids circular imports
type ConfigAuth struct {
	Enabled bool `yaml:"enabled"`
	Session struct {
		Secret     string `yaml:"secret"`
		CookieName string `yaml:"cookie_name"`
		MaxAge     int    `yaml:"max_age"`
		Secure     bool   `yaml:"secure"`
		HttpOnly   bool   `yaml:"http_only"`
		SameSite   string `yaml:"same_site"`
	} `yaml:"session"`
	DefaultAdmin struct {
		Username string `yaml:"username"`
		Email    string `yaml:"email"`
		Password string `yaml:"password"`
	} `yaml:"default_admin"`
	Keycloak struct {
		Enabled      bool   `yaml:"enabled"`
		Issuer       string `yaml:"issuer"`
		ClientID     string `yaml:"client_id"`
		ClientSecret string `yaml:"client_secret"`
		RedirectURI  string `yaml:"redirect_uri"`
		Scopes       string `yaml:"scopes"`
		RoleClaim    string `yaml:"role_claim"`
	} `yaml:"keycloak"`
}

// NewAuthConfigFromConfig creates an AuthConfig from the application config
// This function provides a clean interface for config.yaml integration
func NewAuthConfigFromConfig(configAuth *ConfigAuth) AuthConfig {
	if configAuth == nil {
		return getAuthConfigFromEnv()
	}

	config := AuthConfig{
		Enabled: configAuth.Enabled,
		Session: SessionConfig{
			Secret:     configAuth.Session.Secret,
			CookieName: getStringWithFallback(configAuth.Session.CookieName, "audiobookshelf-sync-session"),
			MaxAge:     getIntWithFallback(configAuth.Session.MaxAge, 86400), // 24 hours
			Secure:     configAuth.Session.Secure,
			HttpOnly:   getBoolWithFallback(configAuth.Session.HttpOnly, true),
			SameSite:   getStringWithFallback(configAuth.Session.SameSite, "Lax"),
		},
	}

	// Auto-generate session secret if empty
	if config.Session.Secret == "" {
		config.Session.Secret = generateSessionSecret()
	}

	// Set up providers based on configuration
	config.Providers = []AuthProviderConfig{}

	// Always add local provider if auth is enabled
	if config.Enabled {
		localProvider := AuthProviderConfig{
			Type:    "local",
			Name:    "local",
			Enabled: true,
			Config: map[string]string{
				"default_admin_username": getStringWithFallback(configAuth.DefaultAdmin.Username, "admin"),
				"default_admin_email":    getStringWithFallback(configAuth.DefaultAdmin.Email, "admin@localhost"),
				"default_admin_password": configAuth.DefaultAdmin.Password,
			},
		}
		config.Providers = append(config.Providers, localProvider)
	}

	// Add Keycloak/OIDC provider if enabled
	if configAuth.Keycloak.Enabled {
		keycloakProvider := AuthProviderConfig{
			Type:    "oidc",
			Name:    "keycloak",
			Enabled: true,
			Config: map[string]string{
				"issuer":        configAuth.Keycloak.Issuer,
				"client_id":     configAuth.Keycloak.ClientID,
				"client_secret": configAuth.Keycloak.ClientSecret,
				"redirect_uri":  configAuth.Keycloak.RedirectURI,
				"scopes":        getStringWithFallback(configAuth.Keycloak.Scopes, "openid profile email"),
				"role_claim":    getStringWithFallback(configAuth.Keycloak.RoleClaim, "realm_access.roles"),
			},
		}
		config.Providers = append(config.Providers, keycloakProvider)
	}

	// Override with environment variables if they exist (env takes precedence)
	if isEnvSet("AUTH_ENABLED") {
		envConfig := getAuthConfigFromEnv()
		config.Enabled = envConfig.Enabled
	}
	if isEnvSet("AUTH_SESSION_SECRET") {
		config.Session.Secret = os.Getenv("AUTH_SESSION_SECRET")
	}
	if isEnvSet("AUTH_COOKIE_NAME") {
		config.Session.CookieName = os.Getenv("AUTH_COOKIE_NAME")
	}
	if isEnvSet("AUTH_SESSION_MAX_AGE") {
		if maxAge := getIntFromEnv("AUTH_SESSION_MAX_AGE", 0); maxAge > 0 {
			config.Session.MaxAge = maxAge
		}
	}
	if isEnvSet("AUTH_SESSION_SECURE") {
		config.Session.Secure = getBoolFromEnv("AUTH_SESSION_SECURE", false)
	}
	if isEnvSet("AUTH_SESSION_HTTP_ONLY") {
		config.Session.HttpOnly = getBoolFromEnv("AUTH_SESSION_HTTP_ONLY", true)
	}
	if isEnvSet("AUTH_SESSION_SAME_SITE") {
		config.Session.SameSite = os.Getenv("AUTH_SESSION_SAME_SITE")
	}

	// Override default admin settings from environment
	if len(config.Providers) > 0 && config.Providers[0].Type == "local" {
		if isEnvSet("AUTH_DEFAULT_ADMIN_USERNAME") {
			config.Providers[0].Config["default_admin_username"] = os.Getenv("AUTH_DEFAULT_ADMIN_USERNAME")
		}
		if isEnvSet("AUTH_DEFAULT_ADMIN_EMAIL") {
			config.Providers[0].Config["default_admin_email"] = os.Getenv("AUTH_DEFAULT_ADMIN_EMAIL")
		}
		if isEnvSet("AUTH_DEFAULT_ADMIN_PASSWORD") {
			config.Providers[0].Config["default_admin_password"] = os.Getenv("AUTH_DEFAULT_ADMIN_PASSWORD")
		}
	}

	// Override Keycloak settings from environment
	for i, provider := range config.Providers {
		if provider.Type == "oidc" && provider.Name == "keycloak" {
			if isEnvSet("KEYCLOAK_ENABLED") {
				config.Providers[i].Enabled = getBoolFromEnv("KEYCLOAK_ENABLED", false)
			}
			if isEnvSet("KEYCLOAK_ISSUER") {
				config.Providers[i].Config["issuer"] = os.Getenv("KEYCLOAK_ISSUER")
			}
			if isEnvSet("KEYCLOAK_CLIENT_ID") {
				config.Providers[i].Config["client_id"] = os.Getenv("KEYCLOAK_CLIENT_ID")
			}
			if isEnvSet("KEYCLOAK_CLIENT_SECRET") {
				config.Providers[i].Config["client_secret"] = os.Getenv("KEYCLOAK_CLIENT_SECRET")
			}
			if isEnvSet("KEYCLOAK_REDIRECT_URI") {
				config.Providers[i].Config["redirect_uri"] = os.Getenv("KEYCLOAK_REDIRECT_URI")
			}
			if isEnvSet("KEYCLOAK_SCOPES") {
				config.Providers[i].Config["scopes"] = os.Getenv("KEYCLOAK_SCOPES")
			}
			if isEnvSet("KEYCLOAK_ROLE_CLAIM") {
				config.Providers[i].Config["role_claim"] = os.Getenv("KEYCLOAK_ROLE_CLAIM")
			}
		}
	}

	return config
}

// getAuthConfigFromEnv creates auth config from environment variables only
func getAuthConfigFromEnv() AuthConfig {
	config := DefaultAuthConfig()
	
	if isEnvSet("AUTH_ENABLED") {
		config.Enabled = getBoolFromEnv("AUTH_ENABLED", false)
	}
	
	return config
}

// Helper functions
func getStringWithFallback(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func getIntWithFallback(value, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}

func getBoolWithFallback(value, fallback bool) bool {
	// For bool, we use the provided value directly since false is a valid value
	return value
}

func isEnvSet(key string) bool {
	_, exists := os.LookupEnv(key)
	return exists
}

func getIntFromEnv(key string, fallback int) int {
	if value := os.Getenv(key); value != "" {
		if intValue := parseInt(value); intValue > 0 {
			return intValue
		}
	}
	return fallback
}

func getBoolFromEnv(key string, fallback bool) bool {
	if value := os.Getenv(key); value != "" {
		return value == "true" || value == "1" || value == "yes"
	}
	return fallback
}

// parseInt safely parses an integer string
func parseInt(s string) int {
	var result int
	for _, char := range s {
		if char >= '0' && char <= '9' {
			result = result*10 + int(char-'0')
		} else {
			return 0 // Invalid integer
		}
	}
	return result
}
