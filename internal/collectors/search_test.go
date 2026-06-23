package collectors

import "testing"

func TestSearchCollectorUsesConfiguredURL(t *testing.T) {
	t.Setenv(SearXNGURLEnvVar, "https://env.example.com")

	collector := NewSearchCollector(ResolveSearchCollectorURL("https://flag.example.com"))
	if got, want := collector.SearchURL(), "https://flag.example.com/search"; got != want {
		t.Fatalf("SearchURL() = %q, want %q", got, want)
	}
}

func TestSearchCollectorEnvFallback(t *testing.T) {
	t.Setenv(SearXNGURLEnvVar, "https://env.example.com")

	collector := NewSearchCollector(ResolveSearchCollectorURL(""))
	if got, want := collector.SearchURL(), "https://env.example.com/search"; got != want {
		t.Fatalf("SearchURL() = %q, want %q", got, want)
	}
}

func TestSearchCollectorFlagOverridesEnv(t *testing.T) {
	t.Setenv(SearXNGURLEnvVar, "https://env.example.com")

	collector := NewSearchCollector(ResolveSearchCollectorURL("https://flag.example.com"))
	if got, want := collector.SearchURL(), "https://flag.example.com/search"; got != want {
		t.Fatalf("SearchURL() = %q, want %q", got, want)
	}
}
