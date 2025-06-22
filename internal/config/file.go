package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v3"
)

// LoadFromFile loads configuration from a YAML file.

// getWorkingDir returns the current working directory
func getWorkingDir() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return wd
}

// LoadFromFile loads configuration from a YAML file.
func LoadFromFile(path string) (*Config, error) {
	log.Debug().
		Str("path", path).
		Str("working_dir", getWorkingDir()).
		Msg("Loading configuration from file")

	// If path is relative, make it absolute based on the working directory
	if !filepath.IsAbs(path) {
		abspath, err := filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("failed to get absolute path: %w", err)
		}
		path = abspath
	}

	// Check if file exists
	fileInfo, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("config file does not exist: %w", err)
	}

	log.Debug().
		Str("config_file", path).
		Str("file_mode", fileInfo.Mode().String()).
		Int64("file_size", fileInfo.Size()).
		Time("file_mod_time", fileInfo.ModTime()).
		Msg("Config file details")

	// Read the file
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	// Log the first 500 bytes of the file for debugging
	previewSize := 500
	if len(data) < previewSize {
		previewSize = len(data)
	}
	preview := string(data[:previewSize])
	log.Debug().
		Str("start_of_file", preview).
		Int("total_bytes", len(data)).
		Msg("Read config file content")

	// Unmarshal the YAML into our config struct
	var cfg Config
	log.Debug().Msg("Starting YAML unmarshaling")

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		log.Error().Err(err).Msg("Failed to unmarshal YAML config")
		return nil, fmt.Errorf("failed to unmarshal config file: %w", err)
	}

	// Log the loaded configuration (without sensitive data)
	log.Debug().
		Str("config_file", path).
		Str("audiobookshelf_url", cfg.Audiobookshelf.URL).
		Bool("has_audiobookshelf_token", cfg.Audiobookshelf.Token != "").
		Bool("has_hardcover_token", cfg.Hardcover.Token != "").
		Msg("Successfully parsed configuration file")

	// Return the config
	return &cfg, nil
}
