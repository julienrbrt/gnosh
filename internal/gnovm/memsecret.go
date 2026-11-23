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
	_ "embed"
	"fmt"
	"math"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

// mem keeps no secrets: the text about mem says "Never keep a secret, or
// offer to". A model told so still keeps one now and then. On box 2, Qwen
// with its thinking off kept "the staging API key is sk-staging-9f3a2c" as
// a fact, and a new session read it back to the person. So an input of the
// agent's that would keep text that looks like a secret is refused whole
// and keeps nothing (memHost.call); its code would be journaled with it.
// The person's own inputs (/mem) are not held to it.

// secretPrefixes begin the tokens that services hand out as secrets. A
// token that begins with one, at a word's start, is a secret when at
// least 8 characters follow and one of them is a digit: "sk-learn" and
// "task-list" are not.
var secretPrefixes = []string{
	"sk-", "sk_live_", "sk_test_", "rk_live_", "rk_test_",
	"ghp_", "gho_", "ghu_", "ghs_", "ghr_", "github_pat_", "glpat-",
	"xoxb-", "xoxp-", "xoxa-", "xoxr-", "xapp-",
	"hf_", "npm_", "pypi-", "dop_v1_", "shpat_",
	"AKIA", "ASIA", "AIza", "ya29.",
}

// secretNames are words that name a secret. One of them followed by "=",
// ":", "is" or "was" and a value of 8 characters or more that mixes
// letters with digits or signs is a secret kept with its name: "password:
// hunter22". A path ("~/.ssh/id_ed25519") or a variable's name is where a
// secret is kept, not the secret.
var secretNames = []string{"password", "passwd", "passphrase", "secret", "token", "api key", "api_key", "api-key", "apikey", "access key", "secret key", "private key", "credential", "credentials"}

// secretIn says why an input may not be kept, when the text its ops would
// keep, or its code, holds what looks like a secret; "" when nothing
// does. code is looked at when the input changes mem, since the journal
// keeps it.
func secretIn(ops []memOp, code string, kept bool) string {
	for _, op := range ops {
		for _, text := range []string{op.New, op.Name} {
			if what, shown := looksSecret(text); what != "" {
				where := op.Kind
				if op.Name != "" {
					where += " " + op.Name
				}
				return secretRefusal(what, shown, "the "+where)
			}
		}
	}
	if kept {
		if what, shown := looksSecret(code); what != "" {
			return secretRefusal(what, shown, "the input's code")
		}
	}
	return ""
}

// secretRefusal is what the model is told, with the secret cut short.
func secretRefusal(what, shown, where string) string {
	return fmt.Sprintf("mem keeps no secrets, and this input would keep what looks like %s (%s, in %s), so nothing of it was kept. "+
		"Keep only that the secret exists and where the person keeps it, never its value: for example "+
		`mem.Fact("staging-api-key", "the person keeps it; ask them when it is needed").`, what, shown, where)
}

// looksSecret says what kind of secret text holds, if it seems to hold
// one, and the start of it, cut short; "" when it does not.
func looksSecret(text string) (what, shown string) {
	if text == "" {
		return "", ""
	}
	if i := strings.Index(text, "-----BEGIN"); i >= 0 && strings.Contains(text[i:], "PRIVATE KEY-----") {
		return "a private key", "-----BEGIN…"
	}
	if first, ok := seedPhrase(text); ok {
		return "a wallet's recovery phrase (seed words)", first + " …"
	}
	if tok, ok := hexKey(text); ok {
		return "a private key in hex", cut(tok)
	}
	for _, tok := range tokens(text) {
		for _, p := range secretPrefixes {
			if rest, ok := strings.CutPrefix(tok, p); ok && len(rest) >= 8 && strings.ContainsAny(rest, "0123456789") {
				return "an API key or token", cut(tok)
			}
		}
		if jwt(tok) {
			return "a signed token (JWT)", cut(tok)
		}
		if random(tok) {
			return "a random-looking key or token", cut(tok)
		}
	}
	lower := lowerASCII(text)
	for _, name := range secretNames {
		for at := 0; ; {
			i := strings.Index(lower[at:], name)
			if i < 0 {
				break
			}
			i += at
			at = i + len(name)
			if i > 0 && wordRune(rune(lower[i-1])) || at < len(lower) && wordRune(rune(lower[at])) {
				continue
			}
			if value := valueAfter(text[at:]); secretValue(value) {
				return named(name) + " with its value", cut(value)
			}
		}
	}
	return "", ""
}

// The BIP-0039 English word list, the 2048 words wallets write a recovery
// (seed) phrase in: bip39english.txt, whose SHA-256 is the one BIP-0039
// gives, 2f5eed53a4727b4bf8880d8f3f199efc90e58503646d9ff8eff3a2ed3b24dbda,
// as trezor/python-mnemonic carries it (MIT, LICENSE-BIP39.txt).
//
//go:embed bip39english.txt
var bip39Text string

// bip39 is the word list as a set.
var bip39 = sync.OnceValue(func() map[string]bool {
	words := map[string]bool{}
	for _, w := range strings.Fields(bip39Text) {
		words[w] = true
	}
	return words
})

// seedPhraseWords is how many words of the list in a row make a recovery
// phrase: the shortest a wallet writes is twelve.
const seedPhraseWords = 12

// seedPhrase says whether text holds a wallet's recovery phrase, and its
// first word: twelve or more words of the BIP-0039 list in a row, numbers
// between them aside, as a phrase is often written down ("1. legal 2.
// winner …"). The gno review of 2026-11-19 found mem kept a 24-word one.
// Prose does not run so: the words it is held together by (the, and, of,
// to, is) are not in the list.
func seedPhrase(text string) (first string, ok bool) {
	words := bip39()
	run := 0
	for _, f := range strings.FieldsFunc(lowerASCII(text), func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	}) {
		switch {
		case strings.Trim(f, "0123456789") == "":
			// A number between the words.
		case words[f]:
			if run == 0 {
				first = f
			}
			if run++; run >= seedPhraseWords {
				return first, true
			}
		default:
			run = 0
		}
	}
	return "", false
}

// keyWords are the words that, in a text that holds a 64-digit hex number,
// say it is a key and not a hash: a private key is written so, and a
// digest of a file is too.
var keyWords = []string{"priv", "secret", "seed", "wallet", "signing", "mnemonic", "keystore", "key"}

// hexKey says whether text holds a private key written in hex, and the
// key: 64 hex digits (0x before them or not) in a text that also names a
// key. A sha256 digest alone, a checksum, is not one.
func hexKey(text string) (string, bool) {
	var found string
	for _, tok := range tokens(text) {
		hex := strings.TrimPrefix(strings.TrimPrefix(tok, "0x"), "0X")
		if len(hex) == 64 && strings.Trim(strings.ToLower(hex), "0123456789abcdef") == "" {
			found = tok
			break
		}
	}
	if found == "" {
		return "", false
	}
	lower := lowerASCII(text)
	for _, w := range keyWords {
		for at := 0; ; {
			i := strings.Index(lower[at:], w)
			if i < 0 {
				break
			}
			i += at
			at = i + len(w)
			// The start of a word ("private", "keystore"), not the inside
			// of one ("monkey").
			if i == 0 || !wordRune(rune(lower[i-1])) {
				return found, true
			}
		}
	}
	return "", false
}

// lowerASCII is text with its ASCII capitals made small, every byte where
// it was, so that a place found in it is the same place in text.
func lowerASCII(text string) string {
	b := []byte(text)
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}

// named is how a secret's name is said: "a password", "an API key".
func named(name string) string {
	switch name {
	case "api key", "api_key", "api-key", "apikey":
		return "an API key"
	case "access key":
		return "an access key"
	case "credentials":
		return "a credential"
	}
	return "a " + name
}

// tokens are the runs of characters a key or token is made of.
func tokens(text string) []string {
	return strings.FieldsFunc(text, func(r rune) bool { return !tokenRune(r) })
}

// tokenRune says whether r can stand in a key or token.
func tokenRune(r rune) bool {
	return r < utf8.RuneSelf && (unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("_-.+/=", r))
}

// wordRune says whether r is part of a word, for telling a secret's name
// from a longer word that holds it ("tokens" aside).
func wordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' }

// valueAfter is the value that follows a secret's name: after spaces, one
// of "=", ":", "is" or "was", and spaces again, the next run of
// characters without a space, its quotes taken off.
func valueAfter(s string) string {
	s = strings.TrimLeft(s, " \t")
	switch {
	case strings.HasPrefix(s, "="), strings.HasPrefix(s, ":"):
		s = s[1:]
	case strings.HasPrefix(s, "is "), strings.HasPrefix(s, "was "):
		_, s, _ = strings.Cut(s, " ")
	default:
		return ""
	}
	s = strings.TrimLeft(s, " \t")
	if i := strings.IndexAny(s, " \t\n"); i >= 0 {
		s = s[:i]
	}
	return strings.Trim(s, "\"'`,;.")
}

// secretValue says whether a value after a secret's name is one: 8
// characters or more, letters with digits or with signs other than those
// that join a name's parts (".", "-", "_", ":"), and neither a path nor a
// variable's name. "session.expiry.mac" and "STAGING_TOKEN" name where a
// secret is; "hunter22" and "hunter!!" are one.
func secretValue(v string) bool {
	if len(v) < 8 || strings.ContainsAny(v, "/\\") || strings.HasPrefix(v, "~") || strings.HasPrefix(v, "$") {
		return false
	}
	var letter, digit, sign bool
	for _, r := range v {
		switch {
		case unicode.IsLetter(r):
			letter = true
		case unicode.IsDigit(r):
			digit = true
		case !strings.ContainsRune(".-_:", r):
			sign = true
		}
	}
	return letter && (digit || sign)
}

// jwt says whether tok is a JSON Web Token: three parts of base64, the
// first a JSON object.
func jwt(tok string) bool {
	parts := strings.Split(tok, ".")
	if len(parts) != 3 || !strings.HasPrefix(parts[0], "eyJ") {
		return false
	}
	for _, p := range parts {
		if len(p) < 10 {
			return false
		}
	}
	return true
}

// random says whether tok looks like a key made at random: 32 characters
// or more, upper and lower case letters and digits all in it, and spread
// over them as random text is. A hash in hex, a long name and a path are
// not.
func random(tok string) bool {
	if len(tok) < 32 || strings.Contains(tok, "/") && strings.Count(tok, "/") > 2 {
		return false
	}
	var upper, lower, digit bool
	counts := map[rune]int{}
	for _, r := range tok {
		counts[r]++
		switch {
		case unicode.IsUpper(r):
			upper = true
		case unicode.IsLower(r):
			lower = true
		case unicode.IsDigit(r):
			digit = true
		}
	}
	if !upper || !lower || !digit {
		return false
	}
	entropy := 0.0
	for _, c := range counts {
		p := float64(c) / float64(len(tok))
		entropy -= p * math.Log2(p)
	}
	return entropy >= 4.3
}

// cut is the start of a secret, enough to say which it is.
func cut(s string) string {
	if utf8.RuneCountInString(s) <= 6 {
		return "…"
	}
	return string([]rune(s)[:5]) + "…"
}
