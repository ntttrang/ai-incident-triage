// Command seed replays Jira webhook fixtures through the real HTTP endpoint,
// signing each body exactly like Jira Cloud would.
package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/joho/godotenv"
)

type seedResult struct {
	Created   int
	Updated   int
	Duplicate int
	Rejected  int
}

func main() {
	baseURL := flag.String("url", "http://localhost:8085", "API base URL")
	fixturesDir := flag.String("fixtures", "testdata/fixtures/jira", "directory of Jira fixture bodies")
	flag.Parse()

	if err := godotenv.Load(); err != nil && !os.IsNotExist(err) {
		fatal("load .env: %v", err)
	}
	// Same fallback as docker compose, so `make up && make seed` works on a
	// fresh clone; a WEBHOOK_SECRET in .env or the shell overrides both.
	secret := os.Getenv("WEBHOOK_SECRET")
	if secret == "" {
		// Public by design: matches the compose dev fallback so a fresh clone
		// works end to end; the API itself refuses to start without a real
		// WEBHOOK_SECRET. Not a credential.
		secret = "dev-webhook-secret-change-me" // #nosec G101 -- shared dev default, not a secret
		fmt.Println("note: WEBHOOK_SECRET unset, using compose dev default")
	}

	entries, err := os.ReadDir(*fixturesDir)
	if err != nil {
		fatal("read fixtures dir: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".json" {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		fatal("no fixtures found in %s", *fixturesDir)
	}
	sort.Strings(names)

	client := &http.Client{Timeout: 15 * time.Second}
	res := seedResult{}

	for _, name := range names {
		// #nosec G304 -- the path is a fixture file from the operator-supplied
		// -fixtures directory; reading those files is this tool's purpose.
		body, err := os.ReadFile(filepath.Join(*fixturesDir, name))
		if err != nil {
			fatal("read fixture %s: %v", name, err)
		}

		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(body)
		sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

		// Deterministic delivery id: same bytes = same delivery, so the replay
		// fixture (a byte copy of the update fixture) exercises dedup.
		digest := sha256.Sum256(body)
		deliveryID := hex.EncodeToString(digest[:8])

		outcome := postFixture(client, *baseURL, body, sig, deliveryID, name, &res)
		fmt.Printf("%-32s %s\n", name, outcome)
	}

	fmt.Printf("\nsummary: %d created, %d updated, %d duplicate, %d rejected (%d fixtures)\n",
		res.Created, res.Updated, res.Duplicate, res.Rejected, len(names))
	if res.Rejected > 0 {
		os.Exit(1)
	}
}

func postFixture(client *http.Client, baseURL string, body []byte, sig, deliveryID, name string, res *seedResult) string {
	req, err := http.NewRequest(http.MethodPost, baseURL+"/api/v1/webhooks/jira", bytes.NewReader(body))
	if err != nil {
		fatal("build request for %s: %v", name, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Hub-Signature", sig)
	req.Header.Set("X-Atlassian-Webhook-Identifier", deliveryID)

	resp, err := client.Do(req)
	if err != nil {
		res.Rejected++
		return fmt.Sprintf("ERROR: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var payload struct {
		Outcome string `json:"outcome"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&payload)

	switch {
	case resp.StatusCode == http.StatusAccepted && payload.Outcome == "created":
		res.Created++
		return "created"
	case resp.StatusCode == http.StatusOK && payload.Outcome == "updated":
		res.Updated++
		return "updated"
	case resp.StatusCode == http.StatusOK && payload.Outcome == "duplicate":
		res.Duplicate++
		return "duplicate"
	default:
		res.Rejected++
		return fmt.Sprintf("rejected: HTTP %d %q", resp.StatusCode, payload.Outcome)
	}
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "seed: "+format+"\n", args...)
	os.Exit(1)
}
