package convert

import "strings"

// ModelProfile maps a client-facing model name onto the upstream model id and
// the reasoning depth it implies.
//
// The Antigravity backend exposes a single tiered model whose depth is chosen
// by thinkingLevel; the older "-low/-medium/-high" ids are client-side sugar.
type ModelProfile struct {
	Upstream        string
	ThinkingLevel   string // "", "LOW", "MEDIUM", "HIGH"
	IncludeThoughts bool
}

// tieredUpstream is the backend id that serves gemini-3.8-flash-{low,medium,high}.
const tieredUpstream = "gemini-3.8-flash-tiered"

// ResolveModel applies alias and reasoning-depth rules. Unknown names are
// forwarded unchanged so new upstream models work without a code change.
func ResolveModel(model string) ModelProfile {
	name := strings.ToLower(strings.TrimSpace(model))
	if name == "" {
		name = "gemini-3.8-flash-medium"
	}

	// Gemini 3.8 flash family, including the legacy gemini-4-argon aliases.
	if strings.HasPrefix(name, "gemini-3.8-flash") || strings.HasPrefix(name, "gemini-4-argon") {
		return ModelProfile{
			Upstream:        tieredUpstream,
			ThinkingLevel:   effortSuffix(name, "MEDIUM"),
			IncludeThoughts: true,
		}
	}

	switch {
	case strings.HasPrefix(name, "claude-sonnet-5-5"), strings.HasPrefix(name, "claude-sonnet-4-6"):
		return ModelProfile{Upstream: "claude-sonnet-4-6"}
	case strings.HasPrefix(name, "claude-opus-5-5"), strings.HasPrefix(name, "claude-opus-4-6"):
		return ModelProfile{Upstream: "claude-opus-4-6-thinking"}
	case strings.HasPrefix(name, "gpt-oss-120b"):
		return ModelProfile{Upstream: "gpt-oss-120b-medium"}
	}

	return ModelProfile{Upstream: model}
}

// effortSuffix extracts the trailing -low/-medium/-high and falls back to def.
func effortSuffix(name, def string) string {
	switch {
	case strings.HasSuffix(name, "-low"):
		return "LOW"
	case strings.HasSuffix(name, "-high"):
		return "HIGH"
	case strings.HasSuffix(name, "-medium"):
		return "MEDIUM"
	}
	return def
}
