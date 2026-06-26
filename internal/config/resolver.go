// Package config provides typed resolvers (C1) for the flag > env > default
// precedence chain documented in docs/conventions.md, and the machine-readable
// convention registry (C10).
//
// Resolution rules:
//
//  1. If the pflag.Flag has Changed=true (explicitly set on the command line),
//     use the flag value. This beats env and default.
//  2. If the environment variable is present (os.LookupEnv returns ok=true),
//     use the env value — even if the value is an empty string. This is the
//     critical distinction from a naive firstNonEmpty helper, which silently
//     treats "" as "not set".
//  3. If required=true and neither flag nor env provided a value, return an error.
//  4. Otherwise return defaultVal.
package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/spf13/pflag"
)

// ResolveString resolves a string config value following flag > env > default.
// Pass flag=nil for config concepts that have no CLI flag (e.g. MCP auth token).
func ResolveString(flag *pflag.Flag, envVar string, defaultVal string, required bool) (string, error) {
	if flag != nil && flag.Changed {
		return flag.Value.String(), nil
	}
	if val, ok := os.LookupEnv(envVar); ok {
		return val, nil
	}
	if required {
		return "", requiredErr(flag, envVar)
	}
	return defaultVal, nil
}

// ResolveInt resolves an integer config value following flag > env > default.
func ResolveInt(flag *pflag.Flag, envVar string, defaultVal int, required bool) (int, error) {
	if flag != nil && flag.Changed {
		v, err := strconv.Atoi(flag.Value.String())
		if err != nil {
			return 0, fmt.Errorf("invalid int for flag --%s: %w", flag.Name, err)
		}
		return v, nil
	}
	if raw, ok := os.LookupEnv(envVar); ok {
		v, err := strconv.Atoi(raw)
		if err != nil {
			return 0, fmt.Errorf("invalid int for env %s=%q: %w", envVar, raw, err)
		}
		return v, nil
	}
	if required {
		return 0, requiredErr(flag, envVar)
	}
	return defaultVal, nil
}

// ResolveBool resolves a boolean config value following flag > env > default.
// Accepted env values: "1", "t", "T", "true", "TRUE", "True", "0", "f", "F",
// "false", "FALSE", "False" (same as strconv.ParseBool).
func ResolveBool(flag *pflag.Flag, envVar string, defaultVal bool, required bool) (bool, error) {
	if flag != nil && flag.Changed {
		v, err := strconv.ParseBool(flag.Value.String())
		if err != nil {
			return false, fmt.Errorf("invalid bool for flag --%s: %w", flag.Name, err)
		}
		return v, nil
	}
	if raw, ok := os.LookupEnv(envVar); ok {
		v, err := strconv.ParseBool(raw)
		if err != nil {
			return false, fmt.Errorf("invalid bool for env %s=%q: %w", envVar, raw, err)
		}
		return v, nil
	}
	if required {
		return false, requiredErr(flag, envVar)
	}
	return defaultVal, nil
}

// ResolveDuration resolves a time.Duration config value following flag > env > default.
// Env values are parsed by time.ParseDuration (e.g. "15m", "1h30m").
func ResolveDuration(flag *pflag.Flag, envVar string, defaultVal time.Duration, required bool) (time.Duration, error) {
	if flag != nil && flag.Changed {
		v, err := time.ParseDuration(flag.Value.String())
		if err != nil {
			return 0, fmt.Errorf("invalid duration for flag --%s: %w", flag.Name, err)
		}
		return v, nil
	}
	if raw, ok := os.LookupEnv(envVar); ok {
		v, err := time.ParseDuration(raw)
		if err != nil {
			return 0, fmt.Errorf("invalid duration for env %s=%q: %w", envVar, raw, err)
		}
		return v, nil
	}
	if required {
		return 0, requiredErr(flag, envVar)
	}
	return defaultVal, nil
}

// ResolveURL resolves a URL config value following flag > env > default.
// The resolved string is parsed by url.Parse; an empty resolved string returns nil, nil.
func ResolveURL(flag *pflag.Flag, envVar string, defaultVal string, required bool) (*url.URL, error) {
	raw, err := ResolveString(flag, envVar, defaultVal, required)
	if err != nil {
		return nil, err
	}
	if raw == "" {
		return nil, nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid URL for %s=%q: %w", envVar, raw, err)
	}
	return u, nil
}

// ResolveStringWithAlias resolves a string config value with a deprecated alias
// env var fallback. Resolution order:
//
//  1. flag.Changed → flag value (isAlias=false)
//  2. envVar present → env value (isAlias=false)
//  3. aliasEnvVar present → alias value (isAlias=true) — caller should warn
//  4. required with nothing set → error
//  5. defaultVal (isAlias=false)
//
// The returned isAlias=true signals the caller to emit a deprecation warning.
func ResolveStringWithAlias(flag *pflag.Flag, envVar, aliasEnvVar, defaultVal string, required bool) (string, bool, error) {
	if flag != nil && flag.Changed {
		return flag.Value.String(), false, nil
	}
	if val, ok := os.LookupEnv(envVar); ok {
		return val, false, nil
	}
	if val, ok := os.LookupEnv(aliasEnvVar); ok {
		return val, true, nil
	}
	if required {
		return "", false, requiredErr(flag, envVar)
	}
	return defaultVal, false, nil
}

// requiredErr returns a standardised error message for a missing required config.
func requiredErr(flag *pflag.Flag, envVar string) error {
	flagName := ""
	if flag != nil {
		flagName = flag.Name
	}
	return fmt.Errorf("required config %q is not set (flag --%s or env %s)", envVar, flagName, envVar)
}
