package deploy_test

import (
	"os"
	"strings"
	"testing"
)

func TestHermesWebhookDeploymentContract(t *testing.T) {
	scriptPath := "../bin/test-hermes-deployment.sh"
	script, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatalf("read %s: %v", scriptPath, err)
	}
	body := string(script)

	for _, expected := range []string{
		"https://hermes.petersimmons.com/webhooks/github-issues",
		"issues.labeled",
		"agent/hermes",
		"agent/hermes/working",
		"X-Hub-Signature-256",
		"WEBHOOK_SECRET",
		"GITHUB_TOKEN",
		"/webhooks/*",
		"8644",
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("%s missing required token %q", scriptPath, expected)
		}
	}

	onboardingPath := "../docs/hermes/onboarding.md"
	onboarding, err := os.ReadFile(onboardingPath)
	if err != nil {
		t.Fatalf("read %s: %v", onboardingPath, err)
	}
	content := string(onboarding)

	for _, expected := range []string{
		"agent/hermes",
		"agent/hermes/working",
		"Closes #N",
		"github-issues",
		"Do NOT broaden Hermes Discord allowlist",
		"Do NOT change cron_mode from deny to auto",
	} {
		if !strings.Contains(content, expected) {
			t.Fatalf("%s missing required section token %q", onboardingPath, expected)
		}
	}
}
