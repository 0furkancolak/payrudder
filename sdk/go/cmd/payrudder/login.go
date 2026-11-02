package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	payrudder "github.com/0furkancolak/payrudder/sdk/go"
)

// login stores an API key after the API has accepted it. The key is read from standard input (--api-key-stdin), from $PAYRUDDER_API_KEY, or asked for;
// it is never a flag, which would put it in the process list and the shell history.
func (c *cli) login(ctx context.Context, args []string) error {
	fs := c.flags("login")
	base := fs.String("base-url", "", "the gateway address to use (default $PAYRUDDER_BASE_URL, else http://localhost:42800)")
	stdin := fs.Bool("api-key-stdin", false, "read the API key from standard input instead of asking for it")
	noVerify := fs.Bool("no-verify", false, "store the key without asking the API whether it accepts it")
	if err := parse(fs, args); err != nil {
		return err
	}

	var key string
	switch {
	case *stdin:
		line, err := c.readLine()
		if err != nil {
			return errors.New("no API key on standard input")
		}
		key = line
	case c.getenv(payrudder.EnvAPIKey) != "":
		key = c.getenv(payrudder.EnvAPIKey)
	default:
		// The prompt cannot hide what is typed (that needs a terminal library, and the CLI has no dependencies); --api-key-stdin avoids the echo.
		fmt.Fprint(c.err, "API key (qk_test_... or qk_live_...): ")
		line, err := c.readLine()
		if err != nil {
			return errors.New("no API key given")
		}
		key = line
	}
	key = strings.TrimSpace(key)
	if (!strings.HasPrefix(key, "qk_test_") && !strings.HasPrefix(key, "qk_live_")) || strings.ContainsAny(key, " \t") {
		return errors.New("that is not a Payrudder API key: it starts with qk_test_ (sandbox) or qk_live_ (live)")
	}

	prev, _ := c.loadConfig()
	baseURL := firstNonEmpty(*base, c.getenv(payrudder.EnvBaseURL), prev.BaseURL)
	client, err := c.newClient(key, baseURL)
	if err != nil {
		return err
	}
	note := ""
	if !*noVerify {
		// Any key that authenticates is good: an ingest-only key is refused (403) the read it is checked with, and that still proves the key and the address.
		_, err := client.Meters.ListMeters(ctx)
		var perr *payrudder.Error
		switch {
		case err == nil:
		case errors.As(err, &perr) && perr.Kind == payrudder.KindPermission:
			note = " (the key cannot read meters; it was accepted)"
		case errors.As(err, &perr) && perr.Kind == payrudder.KindAuthentication:
			return fmt.Errorf("the API refused this key (%s): nothing was stored", perr.Error())
		default:
			return fmt.Errorf("could not check the key against %s: %w (use --no-verify to store it anyway)", firstNonEmpty(baseURL, payrudder.DefaultBaseURL), err)
		}
	}

	path, err := c.saveConfig(storedConfig{APIKey: key, BaseURL: baseURL})
	if err != nil {
		return err
	}
	env := "live"
	if strings.HasPrefix(key, "qk_test_") {
		env = "sandbox"
	}
	fmt.Fprintf(c.out, "Signed in to %s with the %s key %s%s.\nStored in %s (readable by you only).\n", firstNonEmpty(baseURL, payrudder.DefaultBaseURL), env, maskKey(key), note, path)
	return nil
}

func (c *cli) logout() error {
	p, err := c.configPath()
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fmt.Fprintln(c.out, "No stored login.")
			return nil
		}
		return err
	}
	fmt.Fprintf(c.out, "Removed %s. (The key itself is still valid: revoke it in the console if it should stop working.)\n", p)
	return nil
}

// maskKey shows the kind and the public prefix of a key (qk_test_ab12cd34_…), never the secret part.
func maskKey(k string) string {
	if parts := strings.Split(k, "_"); len(parts) == 4 {
		return strings.Join(parts[:3], "_") + "_…"
	}
	return k[:min(len(k), 8)] + "…"
}
