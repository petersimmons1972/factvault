// Package config provides the typed resolver (C1) and the convention registry (C10)
// for the factvault scaffold. See docs/conventions.md for the authoritative spec.
package config

// Entry describes one documented configuration concept in the factvault scaffold.
// The registry table in docs/conventions.md is the source of truth; this struct
// is the machine-checkable encoding of that table.
type Entry struct {
	Concept  string // human-readable label, matches the "concept" column in the registry table
	Flag     string // CLI flag, e.g. "--dsn"; empty string if the concept has no flag
	EnvVar   string // primary environment variable, e.g. "FACTVAULT_DATABASE_URL"
	Default  string // default value as a string; "*required*" if the field is required
	Alias    string // deprecated alias env var, if any; empty string if none
	Required bool   // true if the config must be explicitly provided
	Secret   bool   // true if a _FILE companion variant is supported (C9)
}

// Registry is the machine-readable source of truth for every documented factvault
// configuration concept. Contract tests in contract_test.go assert against this
// slice on every PR. Rules:
//
//   - No two entries may share the same EnvVar (Test5).
//   - Every EnvVar must appear in at least one .go source file (Test1).
//   - No FACTVAULT_* string may appear in .go source without a registry entry (Test2).
var Registry = []Entry{
	{
		Concept:  "database URL",
		Flag:     "--dsn",
		EnvVar:   "FACTVAULT_DATABASE_URL",
		Default:  "*required*",
		Alias:    "DATABASE_URL",
		Required: true,
		Secret:   true,
	},
	{
		Concept:  "migrate DSN",
		Flag:     "--dsn",
		EnvVar:   "FACTVAULT_MIGRATE_DATABASE_URL",
		Default:  "",
		Alias:    "",
		Required: false,
		Secret:   false,
	},
	{
		Concept:  "LLM base URL",
		Flag:     "--llm-base-url",
		EnvVar:   "FACTVAULT_LLM_BASE_URL",
		Default:  "http://localhost:11434/v1",
		Alias:    "FACTVAULT_LLM_URL",
		Required: false,
		Secret:   false,
	},
	{
		Concept:  "LLM model",
		Flag:     "--llm-model",
		EnvVar:   "FACTVAULT_LLM_MODEL",
		Default:  "llama3.1:8b",
		Alias:    "",
		Required: false,
		Secret:   false,
	},
	{
		Concept:  "LLM API key",
		Flag:     "", // no CLI flag; secrets must not appear in process args (X4/C9)
		EnvVar:   "FACTVAULT_LLM_API_KEY",
		Default:  "",
		Alias:    "",
		Required: false,
		Secret:   true,
	},
	{
		Concept:  "embedder URL",
		Flag:     "--embedder-url",
		EnvVar:   "FACTVAULT_EMBEDDER_URL",
		Default:  "http://localhost:8081",
		Alias:    "",
		Required: false,
		Secret:   false,
	},
	{
		Concept:  "Wayback URL",
		Flag:     "--wayback-url",
		EnvVar:   "FACTVAULT_WAYBACK_URL",
		Default:  "https://web.archive.org",
		Alias:    "",
		Required: false,
		Secret:   false,
	},
	{
		Concept:  "JWT public key",
		Flag:     "--jwt-public-key",
		EnvVar:   "FACTVAULT_JWT_PUBLIC_KEY",
		Default:  "*required*",
		Alias:    "",
		Required: true,
		Secret:   true,
	},
	{
		Concept:  "JWT private key",
		Flag:     "--jwt-private-key",
		EnvVar:   "FACTVAULT_JWT_PRIVATE_KEY",
		Default:  "*required*",
		Alias:    "",
		Required: true,
		Secret:   true,
	},
	{
		Concept:  "MCP auth token",
		Flag:     "",
		EnvVar:   "FACTVAULT_MCP_AUTH_TOKEN",
		Default:  "*required*",
		Alias:    "",
		Required: true,
		Secret:   true,
	},
	{
		Concept:  "dev tenant ID",
		Flag:     "--tenant",
		EnvVar:   "FACTVAULT_DEV_TENANT_ID",
		Default:  "11111111-1111-1111-1111-111111111111",
		Alias:    "",
		Required: false,
		Secret:   false,
	},
	{
		Concept:  "API listen addr",
		Flag:     "--addr",
		EnvVar:   "FACTVAULT_API_ADDR",
		Default:  ":8080",
		Alias:    "",
		Required: false,
		Secret:   false,
	},
	{
		Concept:  "briefs list max limit",
		Flag:     "",
		EnvVar:   "FACTVAULT_MAX_BRIEFS_LIMIT",
		Default:  "1000",
		Alias:    "",
		Required: false,
		Secret:   false,
	},
	{
		Concept:  "RSS response max bytes",
		Flag:     "",
		EnvVar:   "FACTVAULT_MAX_RSS_BYTES",
		Default:  "10485760",
		Alias:    "",
		Required: false,
		Secret:   false,
	},
	{
		Concept:  "LLM response max bytes",
		Flag:     "",
		EnvVar:   "FACTVAULT_MAX_LLM_BODY_BYTES",
		Default:  "4194304",
		Alias:    "",
		Required: false,
		Secret:   false,
	},
	{
		Concept:  "decompress output max bytes",
		Flag:     "",
		EnvVar:   "FACTVAULT_MAX_DECOMPRESS_BYTES",
		Default:  "104857600",
		Alias:    "",
		Required: false,
		Secret:   false,
	},
	{
		Concept:  "feeds config path",
		Flag:     "--feeds",
		EnvVar:   "FACTVAULT_FEEDS_PATH",
		Default:  "config/feeds.yaml",
		Alias:    "",
		Required: false,
		Secret:   false,
	},
	{
		Concept:  "worker limit",
		Flag:     "--limit",
		EnvVar:   "FACTVAULT_WORKER_LIMIT",
		Default:  "100",
		Alias:    "",
		Required: false,
		Secret:   false,
	},
	{
		Concept:  "verify age (days)",
		Flag:     "--age-days",
		EnvVar:   "FACTVAULT_VERIFY_AGE_DAYS",
		Default:  "7",
		Alias:    "",
		Required: false,
		Secret:   false,
	},
	{
		Concept:  "auth key directory",
		Flag:     "--key-dir",
		EnvVar:   "FACTVAULT_AUTH_DIR",
		Default:  ".local",
		Alias:    "",
		Required: false,
		Secret:   false,
	},
	{
		Concept:  "confirm cost",
		Flag:     "--confirm-cost",
		EnvVar:   "FACTVAULT_CONFIRM_COST",
		Default:  "false",
		Alias:    "",
		Required: false,
		Secret:   false,
	},
	{
		Concept:  "SearXNG URL",
		Flag:     "--searxng-url",
		EnvVar:   "FACTVAULT_SEARXNG_URL",
		Default:  "https://searxng.example.com",
		Alias:    "",
		Required: false,
		Secret:   false,
	},
	{
		Concept:  "verify token",
		Flag:     "--token",
		EnvVar:   "FACTVAULT_VERIFY_TOKEN",
		Default:  "",
		Alias:    "",
		Required: false,
		Secret:   false,
	},
}
