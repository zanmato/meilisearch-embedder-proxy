package hash

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode"

	"go.uber.org/zap"
)

type Hasher struct {
	logger *zap.Logger
}

func New(logger *zap.Logger) *Hasher {
	return &Hasher{
		logger: logger,
	}
}

func (h *Hasher) GenerateInputHash(inputText, modelName string) string {
	normalizedInput := h.normalizeInput(inputText)

	data := fmt.Sprintf("%s|%s", normalizedInput, modelName)

	hash := sha256.Sum256([]byte(data))
	hashHex := hex.EncodeToString(hash[:])

	h.logger.Debug("Generated input hash",
		zap.String("input_preview", h.truncateForLog(normalizedInput, 50)),
		zap.String("model", modelName),
		zap.String("hash", hashHex[:16]+"..."),
		zap.Int("input_length", len(normalizedInput)))

	return hashHex
}

func (h *Hasher) normalizeInput(input string) string {
	input = strings.TrimSpace(input)

	input = h.normalizeUnicode(input)

	input = h.normalizeWhitespace(input)

	if len(input) > 10000 {
		h.logger.Warn("Input text truncated for hashing",
			zap.Int("original_length", len(input)),
			zap.Int("truncated_length", 10000))
		input = input[:10000]
	}

	return input
}

func (h *Hasher) normalizeUnicode(input string) string {
	var normalized strings.Builder

	for _, r := range input {
		if unicode.IsControl(r) && r != '\t' && r != '\n' && r != '\r' {
			continue
		}
		normalized.WriteRune(r)
	}

	return normalized.String()
}

func (h *Hasher) normalizeWhitespace(input string) string {
	input = strings.ReplaceAll(input, "\r\n", "\n")
	input = strings.ReplaceAll(input, "\r", "\n")

	lines := strings.Split(input, "\n")
	var normalizedLines []string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			normalizedLines = append(normalizedLines, trimmed)
		}
	}

	return strings.Join(normalizedLines, " ")
}

func (h *Hasher) ValidateHash(hash string) bool {
	if len(hash) != 64 {
		return false
	}

	_, err := hex.DecodeString(hash)
	return err == nil
}

func (h *Hasher) truncateForLog(input string, maxLen int) string {
	if len(input) <= maxLen {
		return input
	}

	if maxLen <= 3 {
		return input[:maxLen]
	}

	return input[:maxLen-3] + "..."
}

func (h *Hasher) GetHashMetadata(inputText, modelName string) map[string]interface{} {
	normalizedInput := h.normalizeInput(inputText)

	return map[string]interface{}{
		"original_length":    len(inputText),
		"normalized_length":  len(normalizedInput),
		"model_name":        modelName,
		"has_newlines":      strings.Contains(inputText, "\n"),
		"has_tabs":          strings.Contains(inputText, "\t"),
		"has_extra_spaces":  strings.Contains(inputText, "  "),
		"truncated":         len(inputText) > 10000,
	}
}