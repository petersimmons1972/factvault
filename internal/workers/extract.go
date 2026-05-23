package workers

import (
	"context"
	"log/slog"
	"os"
	"unicode/utf8"
)

// VerifyExcerptOffset validates that the excerpt appears at the claimed offsets in the source text.
// This is the anti-hallucination gate: an LLM that fabricates an excerpt will be rejected
// because the offsets won't match the actual text.
//
// Rules:
// - offsetStart and offsetEnd are character positions (rune indices) into rawText
// - the function validates that offsets land on valid UTF-8 rune boundaries
// - rawText[offsetStart:offsetEnd] must exactly equal excerpt
// - no case folding, Unicode normalization, or trimming
// - exact string equality only
func VerifyExcerptOffset(rawText, excerpt string, offsetStart, offsetEnd int) bool {
	if offsetStart < 0 || offsetEnd < 0 {
		return false
	}

	if offsetStart >= offsetEnd {
		return false
	}

	// Count runes to map character positions to byte positions
	// This ensures we respect UTF-8 boundaries
	runeCount := 0
	startBytePos := -1
	endBytePos := -1

	for bytePos := 0; bytePos < len(rawText); {
		r, size := utf8.DecodeRuneInString(rawText[bytePos:])
		if r == utf8.RuneError {
			return false // Invalid UTF-8
		}

		if runeCount == offsetStart && startBytePos == -1 {
			startBytePos = bytePos
		}
		if runeCount == offsetEnd {
			endBytePos = bytePos
		}

		bytePos += size
		runeCount++
	}

	// If we couldn't find the end position, check if it's at the end of the string
	if endBytePos == -1 && offsetEnd == runeCount {
		endBytePos = len(rawText)
	}

	// Validate bounds
	if startBytePos == -1 || endBytePos == -1 {
		return false
	}

	// Extract and compare
	extracted := rawText[startBytePos:endBytePos]
	return extracted == excerpt
}

// ExtractOnce processes all archived sources and extracts facts from them.
// This is a placeholder implementation that demonstrates the structure.
//
// Full implementation would:
// 1. Fetch archived sources
// 2. Run deterministic extractors
// 3. Run LLM extractor
// 4. Apply offset gate to LLM proposals
// 5. Insert verified facts into statements + statement_sources
func ExtractOnce(ctx context.Context, logger *slog.Logger) error {
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(os.Stdout, nil))
	}

	logger.InfoContext(ctx, "extract worker started")
	// Full implementation TBD
	return nil
}
