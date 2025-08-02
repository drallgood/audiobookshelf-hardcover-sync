package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
)

var (
	ErrInvalidCiphertext = errors.New("invalid ciphertext")
	ErrInvalidKeySize    = errors.New("invalid key size")
)

// EncryptionManager handles encryption and decryption of sensitive data
type EncryptionManager struct {
	key    []byte
	logger *logger.Logger
}

// NewEncryptionManager creates a new encryption manager
func NewEncryptionManager(log *logger.Logger) (*EncryptionManager, error) {
	key, err := getOrCreateEncryptionKey()
	if err != nil {
		return nil, fmt.Errorf("failed to get encryption key: %w", err)
	}

	return &EncryptionManager{
		key:    key,
		logger: log,
	}, nil
}

// Encrypt encrypts plaintext using AES-256-GCM
func (em *EncryptionManager) Encrypt(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}

	block, err := aes.NewCipher(em.key)
	if err != nil {
		em.logger.Error("Failed to create cipher", map[string]interface{}{
			"error": err.Error(),
		})
		return "", fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		em.logger.Error("Failed to create GCM", map[string]interface{}{
			"error": err.Error(),
		})
		return "", fmt.Errorf("failed to create GCM: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		em.logger.Error("Failed to generate nonce", map[string]interface{}{
			"error": err.Error(),
		})
		return "", fmt.Errorf("failed to generate nonce: %w", err)
	}

	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	encoded := base64.StdEncoding.EncodeToString(ciphertext)

	em.logger.Debug("Successfully encrypted data", map[string]interface{}{
		"plaintext_length": len(plaintext),
		"encoded_length":   len(encoded),
	})

	return encoded, nil
}

// Decrypt decrypts ciphertext using AES-256-GCM
func (em *EncryptionManager) Decrypt(ciphertext string) (string, error) {
	if ciphertext == "" {
		return "", nil
	}

	data, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		em.logger.Error("Failed to decode base64", map[string]interface{}{
			"error": err.Error(),
		})
		return "", fmt.Errorf("failed to decode base64: %w", err)
	}

	block, err := aes.NewCipher(em.key)
	if err != nil {
		em.logger.Error("Failed to create cipher", map[string]interface{}{
			"error": err.Error(),
		})
		return "", fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		em.logger.Error("Failed to create GCM", map[string]interface{}{
			"error": err.Error(),
		})
		return "", fmt.Errorf("failed to create GCM: %w", err)
	}

	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		em.logger.Error("Ciphertext too short", map[string]interface{}{
			"data_length": len(data),
			"nonce_size":  nonceSize,
		})
		return "", ErrInvalidCiphertext
	}

	nonce, cipherData := data[:nonceSize], data[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, cipherData, nil)
	if err != nil {
		em.logger.Error("Failed to decrypt", map[string]interface{}{
			"error": err.Error(),
		})
		return "", fmt.Errorf("failed to decrypt: %w", err)
	}

	em.logger.Debug("Successfully decrypted data", map[string]interface{}{
		"ciphertext_length": len(ciphertext),
		"plaintext_length":  len(plaintext),
	})

	return string(plaintext), nil
}

// getOrCreateEncryptionKey gets the encryption key from environment or creates a new one
func getOrCreateEncryptionKey() ([]byte, error) {
	// First, try to get key from environment variable
	if keyStr := os.Getenv("ENCRYPTION_KEY"); keyStr != "" {
		key, err := base64.StdEncoding.DecodeString(keyStr)
		if err != nil {
			return nil, fmt.Errorf("failed to decode encryption key from environment: %w", err)
		}
		if len(key) != 32 {
			return nil, fmt.Errorf("encryption key must be 32 bytes, got %d", len(key))
		}
		return key, nil
	}

	// Try to load from file
	keyPath := getKeyFilePath()
	if data, err := os.ReadFile(keyPath); err == nil {
		key, err := base64.StdEncoding.DecodeString(string(data))
		if err != nil {
			return nil, fmt.Errorf("failed to decode encryption key from file: %w", err)
		}
		if len(key) != 32 {
			return nil, fmt.Errorf("encryption key must be 32 bytes, got %d", len(key))
		}
		return key, nil
	}

	// Generate new key and save it
	key := make([]byte, 32) // AES-256
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("failed to generate encryption key: %w", err)
	}

	// Save key to file
	encoded := base64.StdEncoding.EncodeToString(key)
	if err := os.WriteFile(keyPath, []byte(encoded), 0600); err != nil {
		return nil, fmt.Errorf("failed to save encryption key: %w", err)
	}

	return key, nil
}

// getKeyFilePath returns the path to the encryption key file
func getKeyFilePath() string {
	dataDir := os.Getenv("DATA_DIR")
	if dataDir == "" {
		dataDir = "./data"
	}
	
	// Ensure data directory exists
	os.MkdirAll(dataDir, 0755)
	
	return fmt.Sprintf("%s/encryption.key", dataDir)
}

// DeriveKeyFromPassword derives an encryption key from a password using SHA-256
// This is used as a fallback or for testing purposes
func DeriveKeyFromPassword(password string) []byte {
	hash := sha256.Sum256([]byte(password))
	return hash[:]
}

// NewEncryptionManagerWithKey creates an encryption manager with a specific key
// This is useful for testing or when you want to provide your own key
func NewEncryptionManagerWithKey(key []byte, log *logger.Logger) (*EncryptionManager, error) {
	if len(key) != 32 {
		return nil, ErrInvalidKeySize
	}

	return &EncryptionManager{
		key:    key,
		logger: log,
	}, nil
}
