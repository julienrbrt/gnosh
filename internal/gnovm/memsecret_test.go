// Copyright (C) 2026 Jae Kwon
//
// This file is part of gnosh's GnoVM machine subsystem. It is licensed under
// the GNO Network General Public License, version 6, as published by
// NewTendermint, LLC or its successor, or any later version of that License
// that the Licensor accepts as the proxy under its section 14, by a statement
// published in the canonical copy of internal/gnovm/LICENSE.md; together with
// the Additional Terms (Strong Attribution) in that file.

package gnovm

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// What looks like a secret, and what does not.
func TestLooksSecret(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		text, what string
	}{
		{"the staging API key is sk-staging-9f3a2c", "an API key or token"},
		{"sk-proj-Ab3dEf6hIj9kLm", "an API key or token"},
		{"token ghp_abcdefgh12345678abcdefgh", "an API key or token"},
		{"AKIAIOSFODNN7EXAMPLE", "an API key or token"},
		{"do token: dop_v1_0123abcd4567ef", "an API key or token"},
		{"password: hunter22", "a password with its value"},
		{"the db password is Tr0ub4dor&3", "a password with its value"},
		{`api_key="q7Wz-PLx-99"`, "an API key with its value"},
		{"Secret = 'correct-horse-9'", "a secret with its value"},
		{"-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXk", "a private key"},
		{"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0In0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U", "a signed token (JWT)"},
		{"key Zx8qLm3NvB7tKp2RwY5sHd9fGj4cXa6E", "a random-looking key or token"},

		{"use sk-learn for the model", ""},
		{"run the task-list check", ""},
		{"the staging API key is in the person's password manager", ""},
		{"the token is kept in STAGING_TOKEN", ""},
		{"the private key is ~/.ssh/id_ed25519", ""},
		{"password: see $DB_PASSWORD", ""},
		{"commit 9f3a2c1b8e7d6c5b4a3928170615f4e3d2c1b0a9", ""},
		{"address g1ecsuj0q572jr0dhu29q9njtnmw03hyu7tyyvv6", ""},
		{"id 123e4567-e89b-12d3-a456-426614174000", ""},
		{"TestSubAgentWorktreeKeepsToItsTreeAndMore", ""},
		{"tokens are counted per reply", ""},
		{"the token is session.expiry.mac and is checked in Verify", ""},
		{"password: hunter!!", "a password with its value"},
		{"rotate the secret every month", ""},
		{"İstanbul password: hunter22", "a password with its value"},
		{"", ""},

		// A wallet's recovery phrase, as BIP-0039's own examples have
		// them, written out or numbered as one is written down (the gno
		// review of 2026-11-19: mem kept a 24-word one); and prose,
		// which is held together by words the list has not.
		{"abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about", "a wallet's recovery phrase (seed words)"},
		{"seed: 1. legal 2. winner 3. thank 4. year 5. wave 6. sausage 7. worth 8. useful 9. legal 10. winner 11. thank 12. yellow", "a wallet's recovery phrase (seed words)"},
		{"Letter Advice Cage Absurd Amount Doctor Acoustic Avoid Letter Advice Cage Above", "a wallet's recovery phrase (seed words)"},
		{"legal winner thank year wave sausage worth useful legal winner thank", ""},
		{"the deploy script reads the config file and then restarts the service on every host in the cluster", ""},
		// A private key in hex, and a digest, which is written the same.
		{"validator private key 0x1f2e3d4c5b6a79881f2e3d4c5b6a79881f2e3d4c5b6a79881f2e3d4c5b6a7988", "a private key in hex"},
		{"wallet key: 1f2e3d4c5b6a79881f2e3d4c5b6a79881f2e3d4c5b6a79881f2e3d4c5b6a7988", "a private key in hex"},
		{"the release tarball's sha256 is 1f2e3d4c5b6a79881f2e3d4c5b6a79881f2e3d4c5b6a79881f2e3d4c5b6a7988", ""},
		{"the monkey sha256 1f2e3d4c5b6a79881f2e3d4c5b6a79881f2e3d4c5b6a79881f2e3d4c5b6a7988", ""},
	} {
		what, shown := looksSecret(c.text)
		require.Equal(t, c.what, what, "%q", c.text)
		if what != "" {
			require.True(t, strings.HasSuffix(shown, "…"), "%q is shown as %q", c.text, shown)
			require.LessOrEqual(t, len([]rune(shown)), 11, "%q shows too much of the secret: %q", c.text, shown)
		}
	}
}

// An input of the agent's that would keep a secret keeps nothing and says
// why; the person's own may.
func TestMemKeepsNoSecrets(t *testing.T) {
	t.Parallel()
	h := newTestMemHost(t, t.TempDir())
	res, err := h.call(context.Background(), memRequest{Code: `mem.Fact("staging-api-key", "sk-staging-9f3a2c")`, Session: "s1"})
	var ref *refusal
	require.True(t, errors.As(err, &ref), "the input was not refused: %v %q", err, res.Output)
	require.Contains(t, ref.msg, "mem keeps no secrets")
	require.Contains(t, ref.msg, "the fact staging-api-key")
	require.Contains(t, ref.msg, "sk-st…")
	require.NotContains(t, ref.msg, "9f3a2c")
	require.Zero(t, res.Seq)
	_, err = h.call(context.Background(), memRequest{Code: `println(mem.Fact("staging-api-key"))`, Session: "s1"})
	require.ErrorContains(t, err, "no fact `staging-api-key`", "the refused fact was kept")

	// Keeping that it exists is fine.
	memOK(t, h, `mem.Fact("staging-api-key", "the person keeps it; ask them")`)
	// A secret in code that keeps something is refused too: the journal
	// keeps the code.
	_, err = h.call(context.Background(), memRequest{Code: "// password: hunter22\n" + `mem.Fact("rotation", "monthly")`, Session: "s1"})
	require.True(t, errors.As(err, &ref), "%v", err)
	require.Contains(t, ref.msg, "the input's code")

	// The person's own input is theirs to make.
	res, err = h.call(context.Background(), memRequest{Code: `mem.Fact("my-key", "sk-mine-12345678")`, Session: "s1", User: true})
	require.NoError(t, err, res.Output)
	require.NotZero(t, res.Seq)
}
