package deterministic

import (
	"context"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/petersimmons1972/factvault/internal/db"
	"github.com/petersimmons1972/factvault/internal/extractors"
)

type MoneyExtractor struct{}

var moneyPattern = regexp.MustCompile(`(?i)(?:USD|US\$|\$)\s*([0-9]{1,3}(?:,[0-9]{3})*(?:\.\d+)?|[0-9]+(?:\.\d+)?)(?:\s*(BILLION|MILLION|THOUSAND|BN|B|M|K))?\b`)

func (MoneyExtractor) Extract(ctx context.Context, source *db.Source, rawText string) ([]extractors.ExtractedFact, error) {
	_ = ctx

	subject := sourceLabel(source)
	matches := moneyPattern.FindAllStringSubmatchIndex(rawText, -1)
	facts := make([]extractors.ExtractedFact, 0, len(matches))
	for _, match := range matches {
		if len(match) < 6 {
			continue
		}
		excerptStart, excerptEnd := match[0], match[1]
		amountStart, amountEnd := match[2], match[3]
		suffixStart, suffixEnd := match[4], match[5]

		amount := rawText[amountStart:amountEnd]
		suffix := ""
		if suffixStart >= 0 && suffixEnd >= suffixStart {
			suffix = rawText[suffixStart:suffixEnd]
		}

		facts = append(facts, extractors.ExtractedFact{
			SubjectText:        subject,
			PropertySlug:       "usd_amount",
			Value:              normalizeUSDAmount(amount, suffix),
			ValueType:          "number",
			Excerpt:            rawText[excerptStart:excerptEnd],
			ExcerptOffsetStart: byteOffsetToCharOffset(rawText, excerptStart),
			ExcerptOffsetEnd:   byteOffsetToCharOffset(rawText, excerptEnd),
			ExtractionMethod:   "regex:money-v1",
		})
	}
	return facts, nil
}

func normalizeUSDAmount(amount, suffix string) string {
	normalized := strings.ReplaceAll(strings.TrimSpace(amount), ",", "")
	value, err := strconv.ParseFloat(normalized, 64)
	if err != nil {
		return normalized
	}

	multiplier := 1.0
	switch strings.ToUpper(strings.TrimSpace(suffix)) {
	case "K", "THOUSAND":
		multiplier = 1e3
	case "M", "MILLION":
		multiplier = 1e6
	case "B", "BN", "BILLION":
		multiplier = 1e9
	}

	value *= multiplier
	if math.Mod(value, 1) == 0 {
		return strconv.FormatInt(int64(value), 10)
	}
	return strconv.FormatFloat(value, 'f', -1, 64)
}
